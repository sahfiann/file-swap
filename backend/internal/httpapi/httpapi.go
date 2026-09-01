package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sahfiann/file-swap/internal/compress"
	"github.com/sahfiann/file-swap/internal/convert"
	"github.com/sahfiann/file-swap/internal/files"
	"github.com/sahfiann/file-swap/internal/job"
	"github.com/sahfiann/file-swap/internal/media"
	"github.com/sahfiann/file-swap/internal/merge"
	"github.com/sahfiann/file-swap/internal/processor"
)

var engine = job.NewEngine()
var processors = processor.NewRegistry()

func NewMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", health)
	mux.HandleFunc("POST /api/convert", convertHandler)
	mux.HandleFunc("POST /api/merge", mergeHandler)
	mux.HandleFunc("POST /api/compress", compressHandler)
	mux.HandleFunc("POST /api/media", mediaHandler)
	mux.HandleFunc("GET /api/jobs/{id}", jobHandler)
	mux.HandleFunc("GET /api/jobs/{id}/events", jobEventsHandler)
	mux.HandleFunc("GET /api/jobs/{id}/output", jobOutputHandler)
	mux.HandleFunc("POST /api/jobs", asyncJobHandler)
	mux.HandleFunc("POST /api/process", asyncJobHandler)
	mux.HandleFunc("GET /api/media/capabilities", mediaCapabilitiesHandler)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", cancelJobHandler)
	mux.HandleFunc("GET /api/jobs", queueHandler)
	mux.HandleFunc("GET /api/processors", processorsHandler)
	return withCORS(mux)
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func mediaCapabilitiesHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, media.CapabilitiesInfo())
}

func convertHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(files.MaxUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "could not read upload")
		return
	}
	fh := firstFile(r, "file", "files")
	if fh == nil {
		writeErr(w, http.StatusBadRequest, "choose a file to convert")
		return
	}
	target := r.FormValue("target")
	if target == "" {
		ext := files.Ext(fh.Filename)
		if ext == ".docx" || ext == ".doc" {
			target = "pdf"
		} else {
			target = "docx"
		}
	}

	dir, err := os.MkdirTemp("", "fileswap-in-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	defer os.RemoveAll(dir)

	in, err := files.SaveUpload(fh, dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	out, measurement, currentJob, err := engine.Run(r.Context(), job.Spec{UserID: userID(r), InputFiles: []string{in}, Operation: "CONVERT", Processor: "document-conversion", Priority: priority(r)}, func(ctx context.Context) (string, error) {
		return convert.RunContext(ctx, in, target)
	})
	setJobHeaders(w, currentJob)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeMeasurement(w, measurement)
	serveFile(w, out, downloadName(files.Stem(in)+"."+strings.ToLower(target)))
}

func mergeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(files.MaxUploadBytes * 4); err != nil {
		writeErr(w, http.StatusBadRequest, "could not read uploads")
		return
	}
	list := collectFiles(r)
	if len(list) < 2 {
		writeErr(w, http.StatusBadRequest, "upload at least two files to merge")
		return
	}

	dir, err := os.MkdirTemp("", "fileswap-in-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	defer os.RemoveAll(dir)

	paths, err := files.SaveAll(list, dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, measurement, currentJob, err := engine.Run(r.Context(), job.Spec{UserID: userID(r), InputFiles: paths, Operation: "MERGE", Processor: "document-merge", Priority: priority(r)}, func(ctx context.Context) (string, error) {
		return merge.RunContext(ctx, paths)
	})
	setJobHeaders(w, currentJob)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeMeasurement(w, measurement)
	serveFile(w, out, merge.Label(paths))
}

func compressHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(files.MaxUploadBytes * 4); err != nil {
		writeErr(w, http.StatusBadRequest, "could not read uploads")
		return
	}
	list := collectFiles(r)
	if len(list) == 0 {
		writeErr(w, http.StatusBadRequest, "choose files to compress")
		return
	}

	dir, err := os.MkdirTemp("", "fileswap-in-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	defer os.RemoveAll(dir)

	paths, err := files.SaveAll(list, dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	quality := strings.ToLower(strings.TrimSpace(r.FormValue("quality")))
	if quality == "" {
		quality = "medium"
	}
	var mime, out string
	var measurement job.Measurement
	out, measurement, currentJob, err := engine.Run(r.Context(), job.Spec{UserID: userID(r), InputFiles: paths, Operation: "COMPRESS", Processor: "quality-" + quality, Priority: priority(r)}, func(ctx context.Context) (string, error) {
		var processErr error
		out, mime, processErr = compress.RunContext(ctx, paths, quality)
		return out, processErr
	})
	setJobHeaders(w, currentJob)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	name := "compressed" + filepath.Ext(out)
	if len(paths) == 1 && mime == "application/pdf" {
		name = files.Stem(paths[0]) + "-compressed.pdf"
	} else if len(paths) == 1 && mime == "application/zip" {
		name = files.Stem(paths[0]) + ".zip"
	}
	w.Header().Set("Content-Type", mime)
	writeMeasurement(w, measurement)
	serveFile(w, out, name)
}

func mediaHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(files.MaxUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "could not read upload")
		return
	}
	fh := firstFile(r, "file")
	if fh == nil {
		writeErr(w, http.StatusBadRequest, "choose an image or video")
		return
	}
	dir, err := os.MkdirTemp("", "fileswap-in-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	defer os.RemoveAll(dir)
	in, err := files.SaveUpload(fh, dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var mime, out string
	var measurement job.Measurement
	processor := "media-" + r.FormValue("quality")
	if strings.EqualFold(r.FormValue("kind"), "image") {
		processor = "image-" + r.FormValue("quality")
	}
	out, measurement, currentJob, err := engine.Run(r.Context(), job.Spec{UserID: userID(r), InputFiles: []string{in}, Operation: "MEDIA", Processor: processor, Priority: priority(r)}, func(ctx context.Context) (string, error) {
		var processErr error
		out, mime, processErr = media.RunContext(ctx, in, r.FormValue("kind"), r.FormValue("quality"), r.FormValue("format"))
		return out, processErr
	})
	setJobHeaders(w, currentJob)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Content-Type", mime)
	writeMeasurement(w, measurement)
	serveFile(w, out, filepath.Base(out))
}

func writeMeasurement(w http.ResponseWriter, measurement job.Measurement) {
	w.Header().Set("X-Original-Bytes", fmt.Sprintf("%d", measurement.InputBytes))
	w.Header().Set("X-Result-Bytes", fmt.Sprintf("%d", measurement.OutputBytes))
}

func setJobHeaders(w http.ResponseWriter, current *job.Job) {
	if current == nil {
		return
	}
	w.Header().Set("X-Job-ID", current.ID)
	w.Header().Set("X-Job-Status", string(current.Status))
}

func jobHandler(w http.ResponseWriter, r *http.Request) {
	current, ok := engine.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func jobEventsHandler(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	log.Printf("[SSE] client connected job=%s", jobID)
	defer log.Printf("[SSE] client disconnected job=%s", jobID)
	ch, unsubscribe, ok := engine.Subscribe(jobID)
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "SSE is not supported")
		return
	}
	if current, found := engine.Get(jobID); found {
		writeSSE(w, "snapshot", job.JobEvent{Type: "snapshot", Job: current})
		flusher.Flush()
		if current.Status == job.Completed || current.Status == job.Failed || current.Status == job.Cancelled {
			return
		}
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, open := <-ch:
			if !open {
				return
			}
			writeSSE(w, event.Type, event)
			flusher.Flush()
			if event.Type == "progress" {
				log.Printf("[SSE] progress sent job=%s percent=%d", jobID, event.Job.Progress)
			}
			if event.Type == "completed" || event.Type == "failed" || event.Type == "cancelled" {
				return
			}
		case <-heartbeat.C:
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSE(w io.Writer, event string, value any) {
	payload, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

func jobOutputHandler(w http.ResponseWriter, r *http.Request) {
	current, ok := engine.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if current.Status != job.Completed {
		writeErr(w, http.StatusConflict, "job output is not ready")
		return
	}
	output, ok := engine.Output(current.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "job output expired")
		return
	}
	serveFile(w, output, filepath.Base(output))
}

func asyncJobHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(files.MaxUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "could not read upload")
		return
	}
	fh := firstFile(r, "file")
	if fh == nil {
		writeErr(w, http.StatusBadRequest, "choose an image or video")
		return
	}
	dir, err := os.MkdirTemp("", "fileswap-in-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	in, err := files.SaveUpload(fh, dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := strings.ToLower(r.FormValue("kind"))
	if kind == "" {
		kind = "video"
	}
	quality := r.FormValue("quality")
	if quality == "" {
		quality = "high"
	}
	format := r.FormValue("format")
	user := userID(r)
	jobPriority := priority(r)
	processorName := "media-" + quality
	if kind == "image" {
		processorName = "image-" + quality
	}
	idReady := make(chan struct{})
	var id string
	current, err := engine.RunAsync(job.Spec{UserID: user, InputFiles: []string{in}, Operation: "MEDIA", Processor: processorName, Priority: jobPriority}, func(ctx context.Context) (string, error) {
		<-idReady
		if kind == "video" {
			output, _, err := media.RunContextWithProgress(ctx, in, kind, quality, format, func(p media.VideoProgress) {
				engine.UpdateProgress(id, job.Progress{Frame: p.Frame, TotalFrames: p.TotalFrames, FPS: p.FPS, Speed: p.Speed, Elapsed: p.Elapsed, Remaining: p.Remaining, Percent: p.Percent})
			})
			return output, err
		}
		output, _, err := media.RunContext(ctx, in, kind, quality, format)
		return output, err
	}, func(job.Progress) {})
	if err != nil {
		cleanupAsyncInput(in)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id = current.ID
	close(idReady)
	setJobHeaders(w, current)
	writeJSON(w, http.StatusAccepted, current)
}

func cleanupAsyncInput(path string) { _ = os.RemoveAll(filepath.Dir(path)) }

func cancelJobHandler(w http.ResponseWriter, r *http.Request) {
	current, err := engine.Cancel(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, current)
}

func queueHandler(w http.ResponseWriter, _ *http.Request) {
	items := engine.Queue()
	totalWorkers, activeWorkers := engine.WorkerStats()
	queues := map[string][]*job.Job{"HIGH": {}, "MEDIUM": {}, "LOW": {}}
	for _, current := range items {
		level := "LOW"
		if current.Priority >= 2 {
			level = "HIGH"
		} else if current.Priority == 1 {
			level = "MEDIUM"
		}

		queues[level] = append(queues[level], current)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"queues":        queues,
		"workers":       map[string]int{"available": totalWorkers - activeWorkers, "active": activeWorkers, "total": totalWorkers},
		"workerManager": engine.Workers(),
		"resources":     engine.Resources(),
	})
}

func processorsHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"processors": processors.List()})
}

func userID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-User-ID")); value != "" {
		return value
	}
	return "local-user"
}

func priority(r *http.Request) int {
	value, err := strconv.Atoi(r.FormValue("priority"))
	if err != nil || value < 0 {
		return 1
	}
	return value
}

func collectFiles(r *http.Request) []*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	var list []*multipart.FileHeader
	list = append(list, r.MultipartForm.File["files"]...)
	list = append(list, r.MultipartForm.File["file"]...)
	return list
}

func firstFile(r *http.Request, keys ...string) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	for _, k := range keys {
		if list := r.MultipartForm.File[k]; len(list) > 0 {
			return list[0]
		}
	}
	return nil
}

func serveFile(w http.ResponseWriter, path, name string) {
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not open result")
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read result")
		return
	}

	if w.Header().Get("Content-Type") == "" {
		switch files.Ext(path) {
		case ".pdf":
			w.Header().Set("Content-Type", "application/pdf")
		case ".docx":
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		case ".zip":
			w.Header().Set("Content-Type", "application/zip")
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
		}
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("X-Filename", name)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func downloadName(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, `"`, "")
	return name
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, X-Filename, X-Original-Bytes, X-Result-Bytes, X-Job-ID, X-Job-Status")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
