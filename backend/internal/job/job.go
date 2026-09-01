package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sahfiann/file-swap/internal/analyzer"
	"github.com/sahfiann/file-swap/internal/compress"
	"github.com/sahfiann/file-swap/internal/media"
)

type State string

const (
	Created         State = "CREATED"
	Queued          State = "QUEUED"
	Analyzing       State = "ANALYZING"
	Planned         State = "PLANNED"
	Processing      State = "PROCESSING"
	Validating      State = "VALIDATING"
	Completed       State = "COMPLETED"
	Failed          State = "FAILED"
	Cancelled       State = "CANCELLED"
	ResultRetention       = 60 * time.Minute
)

type Job struct {
	ID           string                           `json:"jobId"`
	UserID       string                           `json:"userId"`
	InputFiles   []string                         `json:"inputFiles"`
	Operation    string                           `json:"operation"`
	Processor    string                           `json:"processor"`
	Priority     int                              `json:"priority"`
	Resource     string                           `json:"resourceRequirement,omitempty"`
	Status       State                            `json:"status"`
	Progress     int                              `json:"progress"`
	StartedAt    time.Time                        `json:"startedAt,omitempty"`
	CompletedAt  time.Time                        `json:"completedAt,omitempty"`
	Error        string                           `json:"error,omitempty"`
	Output       string                           `json:"output,omitempty"`
	Analysis     analyzer.Report                  `json:"analysis"`
	Video        *media.VideoMetadata             `json:"videoMetadata,omitempty"`
	Strategy     *media.CompressionStrategy       `json:"strategy,omitempty"`
	PDF          *compress.PDFMetadata            `json:"pdfMetadata,omitempty"`
	PDFStrategy  *compress.PDFCompressionStrategy `json:"compressionStrategy,omitempty"`
	PDFBenchmark *compress.PDFBenchmark           `json:"pdfBenchmark,omitempty"`
	Schedule     string                           `json:"schedule,omitempty"`
	Worker       string                           `json:"worker,omitempty"`
	Frame        int64                            `json:"frame,omitempty"`
	TotalFrames  int64                            `json:"totalFrames,omitempty"`
	FPS          float64                          `json:"fps,omitempty"`
	Speed        float64                          `json:"speed,omitempty"`
	Elapsed      string                           `json:"elapsed,omitempty"`
	Remaining    string                           `json:"remaining,omitempty"`
	Encoder      string                           `json:"encoder,omitempty"`
	OutputBytes  int64                            `json:"outputBytes,omitempty"`
}

type Progress struct {
	Frame       int64
	TotalFrames int64
	FPS         float64
	Speed       float64
	Elapsed     string
	Remaining   string
	Percent     int
}

type Measurement struct{ InputBytes, OutputBytes int64 }

type Spec struct {
	UserID               string
	InputFiles           []string
	Operation, Processor string
	Priority             int
	Resource             string
}

type WorkerCategory string

const (
	WorkerPDF      WorkerCategory = "PDF"
	WorkerDocument WorkerCategory = "DOCUMENT"
	WorkerImage    WorkerCategory = "IMAGE"
	WorkerMedia    WorkerCategory = "MEDIA"
	WorkerOCR      WorkerCategory = "OCR"
)

type WorkerStats struct {
	Category  WorkerCategory `json:"category"`
	Total     int            `json:"total"`
	Active    int            `json:"active"`
	Available int            `json:"available"`
}

type ResourceStats struct {
	MaxCPU  int `json:"maxCPU"`
	MaxRAM  int `json:"maxRAM"`
	MaxDisk int `json:"maxDisk"`
	CPU     int `json:"cpu"`
	RAM     int `json:"ram"`
	Disk    int `json:"disk"`
}

type taskResult struct {
	output      string
	measurement Measurement
	job         *Job
	err         error
}
type task struct {
	jobID     string
	spec      Spec
	ctx       context.Context
	process   func(context.Context) (string, error)
	result    chan taskResult
	done      chan struct{}
	cancelled bool
	progress  func(Progress)
}

type JobEvent struct {
	Type string `json:"type"`
	Job  *Job   `json:"job"`
}

type Engine struct {
	mu              sync.Mutex
	jobs            map[string]*Job
	queue           []*task
	activeUsers     map[string]int
	activeWorkers   map[WorkerCategory]int
	workerResources map[string]WorkerCategory
	workerQueues    map[WorkerCategory]chan *task
	workerTotals    map[WorkerCategory]int
	cancelJobs      map[string]context.CancelFunc
	tasks           map[string]*task
	quota           int
	notify          chan struct{}
	resource        ResourceStats
	subscribers     map[string]map[chan JobEvent]struct{}
	outputs         map[string]string
}

func NewEngine() *Engine {
	e := &Engine{
		jobs: make(map[string]*Job), activeUsers: make(map[string]int),
		activeWorkers: make(map[WorkerCategory]int), workerResources: map[string]WorkerCategory{
			"document-conversion": WorkerDocument, "document-merge": WorkerPDF, "quality-high": WorkerPDF,
			"quality-medium": WorkerPDF, "media-fast": WorkerMedia, "media-high": WorkerMedia, "media-medium": WorkerMedia,
			"image-fast": WorkerImage, "image-high": WorkerImage, "image-medium": WorkerImage,
		},
		workerQueues: make(map[WorkerCategory]chan *task),
		workerTotals: map[WorkerCategory]int{WorkerPDF: 2, WorkerDocument: 2, WorkerImage: 4, WorkerMedia: 1, WorkerOCR: 1},
		cancelJobs:   make(map[string]context.CancelFunc),
		tasks:        make(map[string]*task),
		quota:        2, notify: make(chan struct{}, 1),
		resource:    ResourceStats{MaxCPU: 80, MaxRAM: 70, MaxDisk: 85},
		subscribers: make(map[string]map[chan JobEvent]struct{}),
		outputs:     make(map[string]string),
	}
	for category := range e.workerTotals {
		e.workerQueues[category] = make(chan *task)
	}
	for category, total := range e.workerTotals {
		for i := 0; i < total; i++ {
			go e.worker(category, i+1)
		}
	}
	go e.scheduler()
	go e.cleanupLoop()
	e.scanOrphans()
	return e
}

func (e *Engine) Create(spec Spec) (*Job, error) {
	if len(spec.InputFiles) == 0 || spec.Operation == "" || spec.Processor == "" {
		return nil, fmt.Errorf("could not create processing job")
	}
	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("could not create job ID: %w", err)
	}
	if spec.UserID == "" {
		spec.UserID = "local-user"
	}
	category := e.workerResources[spec.Processor]
	if category == "" {
		category = WorkerDocument
	}
	if spec.Resource == "" {
		spec.Resource = string(category)
	}
	j := &Job{ID: id, UserID: spec.UserID, InputFiles: fileNames(spec.InputFiles), Operation: spec.Operation, Processor: spec.Processor, Priority: spec.Priority, Resource: spec.Resource, Status: Created}
	e.mu.Lock()
	e.jobs[id] = j
	e.mu.Unlock()
	return clone(j), nil
}

func (e *Engine) Run(ctx context.Context, spec Spec, process func(context.Context) (string, error)) (string, Measurement, *Job, error) {
	j, err := e.Create(spec)
	if err != nil {
		return "", Measurement{}, nil, err
	}

	e.update(j.ID, Analyzing, 10)
	report, analysisErr := analyzer.Analyze(spec.InputFiles)
	if analysisErr != nil {
		cleanupArtifacts(spec.InputFiles...)
		return e.fail(j.ID, analysisErr, Failed)
	}
	e.mu.Lock()
	e.jobs[j.ID].Analysis = report
	e.jobs[j.ID].Schedule = scheduleFor(report, len(spec.InputFiles))
	if report.Type == "MP4" && report.Size >= 1024*1024*1024 {
		e.jobs[j.ID].Resource = "HIGH RESOURCE"
	}
	if report.Complexity == "HIGH" && spec.Priority < 2 {
		e.jobs[j.ID].Priority = 2
	}
	e.mu.Unlock()
	if err := e.analyzeVideoPlan(ctx, j.ID, spec, report); err != nil {
		cleanupArtifacts(spec.InputFiles...)
		return e.fail(j.ID, err, failureState(ctx))
	}
	if err := e.analyzePDFPlan(ctx, j.ID, spec, report); err != nil {
		cleanupArtifacts(spec.InputFiles...)
		return e.fail(j.ID, err, failureState(ctx))
	}
	var inputBytes int64
	for _, input := range spec.InputFiles {
		select {
		case <-ctx.Done():
			return e.fail(j.ID, ctx.Err(), Cancelled)
		default:
		}
		info, statErr := os.Stat(input)
		if statErr != nil {
			return e.fail(j.ID, fmt.Errorf("analyze input: %w", statErr), Failed)
		}
		if info.Size() == 0 {
			return e.fail(j.ID, fmt.Errorf("input file is empty"), Failed)
		}
		inputBytes += info.Size()
	}
	e.update(j.ID, Planned, 20)
	e.update(j.ID, Queued, 25)
	t := &task{jobID: j.ID, spec: spec, process: process, result: make(chan taskResult, 1), done: make(chan struct{})}
	jobCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	t.ctx = jobCtx
	e.queue = append(e.queue, t)
	e.cancelJobs[j.ID] = cancel
	e.tasks[j.ID] = t
	e.mu.Unlock()
	e.signal()
	select {
	case result := <-t.result:
		return result.output, result.measurement, result.job, result.err
	case <-ctx.Done():
		e.mu.Lock()
		t.cancelled = true
		e.mu.Unlock()
		e.signal()
		return e.fail(j.ID, ctx.Err(), Cancelled)
	}
}

func (e *Engine) RunAsync(spec Spec, process func(context.Context) (string, error), progress func(Progress)) (*Job, error) {
	j, err := e.Create(spec)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancelJobs[j.ID] = cancel
	e.mu.Unlock()
	go func() { _, _, _, _ = e.runCreated(ctx, j.ID, spec, process, progress) }()
	result, ok := e.Get(j.ID)
	if !ok {
		return nil, fmt.Errorf("job disappeared after creation")
	}
	return result, nil
}

func (e *Engine) runCreated(ctx context.Context, id string, spec Spec, process func(context.Context) (string, error), progress func(Progress)) (string, Measurement, *Job, error) {
	if ctx.Err() != nil {
		return e.fail(id, ctx.Err(), Cancelled)
	}
	e.update(id, Analyzing, 10)
	report, analysisErr := analyzer.Analyze(spec.InputFiles)
	if analysisErr != nil {
		cleanupArtifacts(spec.InputFiles...)
		return e.fail(id, analysisErr, Failed)
	}
	e.mu.Lock()
	e.jobs[id].Analysis = report
	e.jobs[id].Schedule = scheduleFor(report, len(spec.InputFiles))
	e.mu.Unlock()
	if err := e.analyzeVideoPlan(ctx, id, spec, report); err != nil {
		cleanupArtifacts(spec.InputFiles...)
		return e.fail(id, err, failureState(ctx))
	}
	if err := e.analyzePDFPlan(ctx, id, spec, report); err != nil {
		cleanupArtifacts(spec.InputFiles...)
		return e.fail(id, err, failureState(ctx))
	}
	if ctx.Err() != nil {
		return e.fail(id, ctx.Err(), Cancelled)
	}

	for _, input := range spec.InputFiles {
		info, statErr := os.Stat(input)
		if statErr != nil {
			return e.fail(id, fmt.Errorf("analyze input: %w", statErr), Failed)
		}
		if info.Size() == 0 {
			return e.fail(id, fmt.Errorf("input file is empty"), Failed)
		}
	}
	e.update(id, Planned, 0)
	e.update(id, Queued, 0)
	jobCtx, cancel := context.WithCancel(ctx)
	t := &task{jobID: id, spec: spec, process: process, progress: progress, result: make(chan taskResult, 1), done: make(chan struct{}), ctx: jobCtx}
	e.mu.Lock()
	e.queue = append(e.queue, t)
	e.cancelJobs[id] = cancel
	e.tasks[id] = t
	e.mu.Unlock()
	e.signal()
	result := <-t.result
	return result.output, result.measurement, result.job, result.err
}

func (e *Engine) analyzeVideoPlan(ctx context.Context, id string, spec Spec, report analyzer.Report) error {
	if !isVideoType(report.Type) {
		return nil
	}
	metadata, err := media.AnalyzeVideo(ctx, spec.InputFiles[0])
	if err != nil {
		return err
	}
	strategy, err := media.SelectStrategy(metadata, profileFromProcessor(spec.Processor))
	if err != nil {
		return err
	}
	logVideoPlan(id, metadata, strategy)
	e.mu.Lock()
	e.jobs[id].Video = &metadata
	e.jobs[id].Strategy = &strategy
	e.mu.Unlock()
	return nil
}

func (e *Engine) analyzePDFPlan(ctx context.Context, id string, spec Spec, report analyzer.Report) error {
	if spec.Operation != "COMPRESS" || report.Type != "PDF" {
		return nil
	}
	metadata, err := compress.AnalyzePDF(ctx, spec.InputFiles[0])
	if err != nil {
		return err
	}
	strategy, err := compress.SelectPDFStrategy(metadata, profileFromProcessor(spec.Processor))
	if err != nil {
		return err
	}
	log.Printf("[PDF_ANALYZER] job=%s size=%d pages=%v images=%d formats=%v", id, metadata.SizeBytes, metadata.PageCount, metadata.ImageCount, metadata.ImageFormats)
	log.Printf("[PDF_STRATEGY] job=%s profile=%s classification=%s setting=%s reason=%s", id, strategy.Profile, strategy.Classification, strategy.Setting, strategy.Reason)
	e.mu.Lock()
	e.jobs[id].PDF = &metadata
	e.jobs[id].PDFStrategy = &strategy
	e.mu.Unlock()
	return nil
}

func failureState(ctx context.Context) State {
	if ctx.Err() != nil {
		return Cancelled
	}
	return Failed
}

func profileFromProcessor(processor string) string {
	parts := strings.Split(processor, "-")
	return strings.ToUpper(parts[len(parts)-1])
}

func logVideoPlan(id string, metadata media.VideoMetadata, strategy media.CompressionStrategy) {
	resolution := "unknown"
	if metadata.Width != nil && metadata.Height != nil {
		resolution = fmt.Sprintf("%dx%d", *metadata.Width, *metadata.Height)
	}
	bitrate := "unknown"
	if metadata.VideoBitrate != nil {
		bitrate = fmt.Sprintf("%d", *metadata.VideoBitrate)
	}
	fps := "unknown"
	if metadata.FPS != nil {
		fps = fmt.Sprintf("%.3f", *metadata.FPS)
	}
	duration := "unknown"
	if metadata.DurationSeconds != nil {
		duration = fmt.Sprintf("%.3f", *metadata.DurationSeconds)
	}
	targetResolution := "unknown"
	if strategy.TargetWidth != nil && strategy.TargetHeight != nil {
		targetResolution = fmt.Sprintf("%dx%d", *strategy.TargetWidth, *strategy.TargetHeight)
	}
	log.Printf("[ANALYZER] job=%s size=%d duration=%ss resolution=%s codec=%s bitrate=%s fps=%s", id, metadata.SizeBytes, duration, resolution, metadata.VideoCodec, bitrate, fps)
	log.Printf("[STRATEGY] job=%s profile=%s targetCodec=%s targetQuality=%s targetResolution=%s reason=%s", id, strategy.Profile, strategy.TargetCodec, strategy.TargetQuality, targetResolution, strategy.Reason)
}

func (e *Engine) Cancel(id string) (*Job, error) {
	e.mu.Lock()
	j, ok := e.jobs[id]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("job not found")
	}
	if j.Status == Completed || j.Status == Failed || j.Status == Cancelled {
		result := clone(j)
		e.mu.Unlock()
		return result, nil
	}
	cancel := e.cancelJobs[id]
	task := e.tasks[id]
	for _, queued := range e.queue {
		if queued.jobID == id {
			queued.cancelled = true
			j.Status = Cancelled
			j.CompletedAt = time.Now().UTC()
			break
		}
	}
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if task == nil {
		e.mu.Lock()
		if current := e.jobs[id]; current != nil && current.Status != Completed && current.Status != Failed {
			current.Status = Cancelled
			current.CompletedAt = time.Now().UTC()
		}
		e.mu.Unlock()
	}
	e.signal()
	if task != nil {
		<-task.done
	}
	result, _ := e.Get(id)
	return result, nil
}

func (e *Engine) scheduler() {
	for {
		<-e.notify
		for {
			e.mu.Lock()
			index := e.nextTaskLocked()
			if index < 0 {
				e.mu.Unlock()
				break
			}
			t := e.queue[index]
			e.queue = append(e.queue[:index], e.queue[index+1:]...)
			if t.cancelled {
				result := clone(e.jobs[t.jobID])
				e.mu.Unlock()
				t.result <- taskResult{job: result, err: context.Canceled}
				close(t.done)
				continue
			}
			e.activeUsers[t.spec.UserID]++
			category := e.workerResources[t.spec.Processor]
			if category == "" {
				category = WorkerDocument
			}
			e.activeWorkers[category]++
			e.mu.Unlock()
			e.workerQueues[category] <- t
		}
	}
}

func (e *Engine) nextTaskLocked() int {
	best := -1
	for i, t := range e.queue {
		if t.cancelled {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			return i
		}
		category := e.workerResources[t.spec.Processor]
		if category == "" {
			category = WorkerDocument
		}
		if e.activeUsers[t.spec.UserID] >= e.quota || e.activeWorkers[category] >= e.workerTotals[category] {
			continue
		}
		if !e.resourcesAvailableLocked(category) {
			continue
		}
		currentPriority := e.jobs[t.jobID].Priority
		bestPriority := -1
		if best >= 0 {
			bestPriority = e.jobs[e.queue[best].jobID].Priority
		}

		if best < 0 || currentPriority > bestPriority {
			best = i
		}
	}
	return best
}

func (e *Engine) resourcesAvailableLocked(category WorkerCategory) bool {
	if category != WorkerMedia {
		return true
	}
	stats := e.resourceStats()
	return stats.CPU < stats.MaxCPU && stats.RAM < stats.MaxRAM && stats.Disk < stats.MaxDisk
}

func (e *Engine) Resources() ResourceStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resourceStats()
}

func (e *Engine) resourceStats() ResourceStats {
	stats := e.resource
	stats.CPU = minInt(100, runtime.NumGoroutine())
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	stats.RAM = minInt(100, int(mem.Alloc*100/maxUint64(1, mem.Sys)))
	var disk syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &disk); err == nil && disk.Blocks > 0 {
		stats.Disk = int((disk.Blocks - disk.Bavail) * 100 / disk.Blocks)
	}
	return stats
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func (e *Engine) work(t *task, workerName string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("worker panic: %v", recovered)
			log.Printf("[WORKER_PANIC] job=%s error=%v", t.jobID, err)
			_, _, current, _ := e.fail(t.jobID, err, Failed)
			e.release(t)
			cleanupArtifacts(t.spec.InputFiles...)
			t.result <- taskResult{job: current, err: err}
		}
	}()
	e.workUnsafe(t, workerName)
}

func (e *Engine) workUnsafe(t *task, workerName string) {
	defer close(t.done)
	e.mu.Lock()
	if current := e.jobs[t.jobID]; current != nil {
		current.Worker = workerName
	}
	e.mu.Unlock()
	if t.ctx.Err() != nil {
		_, _, j, _ := e.fail(t.jobID, t.ctx.Err(), Cancelled)
		e.release(t)
		t.result <- taskResult{job: j, err: t.ctx.Err()}
		return
	}
	if t.progress != nil {
		e.update(t.jobID, Processing, 0)
	} else {
		e.update(t.jobID, Processing, 30)
	}
	pdfSink := compress.NewPDFBenchmarkSink()
	decisionMetadata := compress.NewPDFDecisionMetadata()
	processCtx := compress.WithPDFBenchmarkSink(t.ctx, pdfSink)
	processCtx = compress.WithPDFDecisionMetadata(processCtx, decisionMetadata)
	output, processErr := t.process(processCtx)
	if processErr != nil {
		state := Failed
		if t.ctx.Err() != nil {
			processErr = t.ctx.Err()
			state = Cancelled
		}

		_, _, j, _ := e.fail(t.jobID, processErr, state)
		e.release(t)
		cleanupArtifacts(append(t.spec.InputFiles, output)...)
		t.result <- taskResult{job: j, err: processErr}
		return
	}
	validationProgress := 85
	if t.progress != nil {
		validationProgress = 100
	}
	e.update(t.jobID, Validating, validationProgress)
	info, statErr := os.Stat(output)
	if statErr != nil {
		_, _, j, _ := e.fail(t.jobID, fmt.Errorf("validate result: %w", statErr), Failed)
		e.release(t)
		cleanupArtifacts(append(t.spec.InputFiles, output)...)
		t.result <- taskResult{job: j, err: statErr}
		return
	}
	if info.Size() == 0 {
		_, _, j, _ := e.fail(t.jobID, fmt.Errorf("validate result: processing produced an empty file"), Failed)
		e.release(t)
		cleanupArtifacts(append(t.spec.InputFiles, output)...)
		t.result <- taskResult{job: j, err: fmt.Errorf("empty output")}
		return
	}
	e.mu.Lock()
	j := e.jobs[t.jobID]
	j.Status = Completed
	j.Progress = 100
	j.Output = filepath.Base(output)
	j.OutputBytes = info.Size()
	if j.PDF != nil && j.PDFStrategy != nil {
		savedBytes := fileSize(t.spec.InputFiles) - info.Size()
		savedPercent := float64(0)
		if original := fileSize(t.spec.InputFiles); original > 0 {
			savedPercent = float64(savedBytes) * 100 / float64(original)
		}
		j.PDFBenchmark = &compress.PDFBenchmark{
			OriginalBytes: fileSize(t.spec.InputFiles), OutputBytes: info.Size(),
			SavedBytes: savedBytes, SavedPercent: savedPercent,
			PageCount: j.PDF.PageCount, ProcessingTimeMs: time.Since(j.StartedAt).Milliseconds(),
			Classification: j.PDFStrategy.Classification, Profile: j.PDFStrategy.Profile,
			Strategy: *j.PDFStrategy, Quality: compress.ClassifyPDFQuality(savedPercent, *j.PDFStrategy),
			CandidateCount:   len(j.PDFStrategy.Candidates),
			CandidateResults: pdfSink.Results(),
		}
		if j.PDFStrategy.Classification == compress.AlreadyOptimized || len(j.PDFStrategy.Candidates) == 0 {
			j.PDFBenchmark.Decision = compress.RetainOriginal
			j.PDFBenchmark.DecisionReason = "PDF is already optimized; original retained without encoding"
			j.PDFBenchmark.SelectedCandidate = "retain-original"
			j.PDFBenchmark.FinalQuality = compress.PDFQualityResult{
				Metric: "NONE", Status: compress.QualityUnassessed,
				SamplePages: j.PDFBenchmark.SamplePages, DPI: j.PDFBenchmark.SampleDPI,
				Reasons: []string{"already optimized fast path; visual comparison not applicable"},
			}
		}
		decision := decisionMetadata.Snapshot()
		j.PDFBenchmark.AttemptCount = decision.AttemptCount
		j.PDFBenchmark.PrimaryCandidate = decision.PrimaryCandidate
		j.PDFBenchmark.FallbackCandidate = decision.FallbackCandidate
		j.PDFBenchmark.FallbackUsed = decision.FallbackUsed
		j.PDFBenchmark.PrimaryDecision = decision.PrimaryDecision
		j.PDFBenchmark.PrimaryReason = decision.PrimaryReason
		j.PDFBenchmark.FallbackDecision = decision.FallbackDecision
		j.PDFBenchmark.FallbackReason = decision.FallbackReason
		if decision.FinalDecision != "" {
			j.PDFBenchmark.Decision = decision.FinalDecision
			j.PDFBenchmark.DecisionReason = decision.FinalReason
			j.PDFBenchmark.SelectedCandidate = decision.SelectedCandidate
			j.PDFBenchmark.FinalQuality = decision.FinalQuality
			j.PDFBenchmark.Quality = decision.FinalQuality.Status
		}
		j.PDFBenchmark.SampleUsed = j.PDF.SizeBytes >= compress.LargePDFThreshold &&
			(j.PDFStrategy.Classification == compress.ImageHeavy || j.PDFStrategy.Classification == compress.ScanDocument)
		if j.PDFBenchmark.SampleUsed {
			j.PDFBenchmark.SampleDPI = compress.PDFQualityDPI
			j.PDFBenchmark.SamplePages = compress.SamplePDFPages(j.PDF.PageCount)
		}
		if j.PDFBenchmark.Quality == compress.QualityUnassessed && j.PDFBenchmark.QualityReason == "" {
			j.PDFBenchmark.QualityReason = "Visual quality metric is unavailable; structural validation passed."
		}
		log.Printf("[PDF_OPTIMIZER] job=%s input=%d output=%d saved_percent=%.2f classification=%s profile=%s quality=%s",
			j.ID, fileSize(t.spec.InputFiles), info.Size(), savedPercent, j.PDFStrategy.Classification, j.PDFStrategy.Profile, j.PDFBenchmark.Quality)
	}
	e.outputs[t.jobID] = output
	j.CompletedAt = time.Now().UTC()
	result := clone(j)
	e.mu.Unlock()
	e.release(t)
	e.scheduleCleanup(t.spec.InputFiles, output)
	e.publish(t.jobID, JobEvent{Type: "completed", Job: result})
	t.result <- taskResult{output: output, measurement: Measurement{InputBytes: fileSize(t.spec.InputFiles), OutputBytes: info.Size()}, job: result}
}

func (e *Engine) UpdateProgress(id string, p Progress) {
	e.mu.Lock()
	j := e.jobs[id]
	if j == nil {
		e.mu.Unlock()
		return
	}
	j.Progress = p.Percent
	j.Frame, j.TotalFrames, j.FPS, j.Speed = p.Frame, p.TotalFrames, p.FPS, p.Speed
	j.Elapsed, j.Remaining = p.Elapsed, p.Remaining
	log.Printf("[JOB_PROGRESS] id=%s percent=%d frame=%d fps=%.2f speed=%.2fx", id, p.Percent, p.Frame, p.FPS, p.Speed)
	event := JobEvent{Type: "progress", Job: clone(j)}
	e.mu.Unlock()
	e.publish(id, event)
}

func (e *Engine) Subscribe(id string) (<-chan JobEvent, func(), bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.jobs[id]; !ok {
		return nil, func() {}, false
	}
	ch := make(chan JobEvent, 16)
	if e.subscribers[id] == nil {
		e.subscribers[id] = make(map[chan JobEvent]struct{})
	}
	e.subscribers[id][ch] = struct{}{}
	return ch, func() { e.mu.Lock(); delete(e.subscribers[id], ch); close(ch); e.mu.Unlock() }, true
}

func (e *Engine) publish(id string, event JobEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subscribers[id] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (e *Engine) Output(id string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	output, ok := e.outputs[id]
	return output, ok
}

func (e *Engine) scheduleCleanup(inputs []string, output string) {
	go func() {
		timer := time.NewTimer(ResultRetention)
		defer timer.Stop()
		<-timer.C
		cleanupArtifacts(append(inputs, output)...)
	}()
}

func (e *Engine) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		e.scanOrphans()
	}
}

func (e *Engine) scanOrphans() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-ResultRetention)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "fileswap-") {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(os.TempDir(), entry.Name()))
		}
	}
}

func cleanupArtifacts(paths ...string) {
	seen := make(map[string]struct{})
	for _, path := range paths {
		dir := filepath.Dir(path)
		if !strings.HasPrefix(filepath.Base(dir), "fileswap-") {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		_ = os.RemoveAll(dir)
	}
}

func (e *Engine) worker(category WorkerCategory, number int) {
	for t := range e.workerQueues[category] {
		e.work(t, fmt.Sprintf("Worker #%02d", number))
	}
}

func (e *Engine) release(t *task) {
	e.mu.Lock()
	e.activeUsers[t.spec.UserID]--
	category := e.workerResources[t.spec.Processor]
	if category == "" {
		category = WorkerDocument
	}
	e.activeWorkers[category]--
	delete(e.cancelJobs, t.jobID)
	e.mu.Unlock()
	e.signal()
}
func (e *Engine) update(id string, state State, progress int) {
	e.mu.Lock()
	if j := e.jobs[id]; j != nil {
		j.Status = state
		j.Progress = progress
		if state == Processing {
			j.StartedAt = time.Now().UTC()
		}
	}
	e.mu.Unlock()
	if current, ok := e.Get(id); ok {
		e.publish(id, JobEvent{Type: strings.ToLower(string(state)), Job: current})
	}
}
func (e *Engine) fail(id string, cause error, state State) (string, Measurement, *Job, error) {
	e.mu.Lock()
	j := e.jobs[id]
	j.Status = state
	j.Error = cause.Error()
	j.CompletedAt = time.Now().UTC()
	result := clone(j)
	e.mu.Unlock()
	e.publish(id, JobEvent{Type: strings.ToLower(string(state)), Job: result})
	return "", Measurement{}, result, cause
}

func (e *Engine) Get(id string) (*Job, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, ok := e.jobs[id]
	if !ok {
		return nil, false
	}
	return clone(j), true
}
func (e *Engine) Queue() []*Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*Job, 0, len(e.queue))
	for _, t := range e.queue {
		if !t.cancelled {
			result = append(result, clone(e.jobs[t.jobID]))
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Priority > result[j].Priority })
	return result
}
func (e *Engine) WorkerStats() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := 0
	for _, count := range e.activeWorkers {
		active += count
	}
	total := 0
	for _, count := range e.workerTotals {
		total += count
	}
	return total, active
}

func (e *Engine) Workers() []WorkerStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]WorkerStats, 0, len(e.workerTotals))
	for category, total := range e.workerTotals {
		active := e.activeWorkers[category]
		result = append(result, WorkerStats{Category: category, Total: total, Active: active, Available: total - active})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Category < result[j].Category })
	return result
}
func (e *Engine) signal() {
	select {
	case e.notify <- struct{}{}:
	default:
	}
}

func scheduleFor(report analyzer.Report, fileCount int) string {
	if isVideoType(report.Type) && report.Size >= 1024*1024*1024 {
		return "HIGH RESOURCE -> MEDIA WORKER"
	}
	if report.Type == "JPEG" && fileCount > 1 {
		return "BATCH IMAGE -> IMAGE WORKER"
	}
	return report.Complexity + " RESOURCE -> WORKER"
}

func isVideoType(value string) bool {
	switch value {
	case "MP4", "MOV", "MKV", "WEBM":
		return true
	default:
		return false
	}
}

func fileSize(paths []string) int64 {
	var total int64
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			total += info.Size()
		}
	}
	return total
}
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "job_" + hex.EncodeToString(b), nil
}
func clone(j *Job) *Job { c := *j; c.InputFiles = append([]string(nil), j.InputFiles...); return &c }
func fileNames(paths []string) []string {
	n := make([]string, len(paths))
	for i, p := range paths {
		n[i] = filepath.Base(p)
	}
	return n
}
