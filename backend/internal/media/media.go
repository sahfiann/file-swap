package media

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sahfiann/file-swap/internal/files"
)

type VideoProgress struct {
	Frame, TotalFrames int64
	FPS, Speed         float64
	Elapsed, Remaining string
	Percent            int
}

type videoInfo struct {
	Duration float64
	FPS      float64
	Frames   int64
	Width    int
	Height   int
}

type Capabilities struct {
	Encoder   string   `json:"encoder"`
	Hardware  bool     `json:"hardware"`
	Available []string `json:"available"`
}

func CapabilitiesInfo() Capabilities {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return Capabilities{Encoder: "unavailable"}
	}
	encoders, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return Capabilities{Encoder: "libopenh264"}
	}
	text := string(encoders)
	available := make([]string, 0, 3)
	for _, encoder := range []string{"h264_nvenc", "h264_qsv", "h264_vaapi"} {
		if strings.Contains(text, encoder) {
			available = append(available, encoder)
		}
	}
	if strings.Contains(text, "h264_nvenc") && hasDevice("/dev/nvidia0") {
		return Capabilities{Encoder: "h264_nvenc", Hardware: true, Available: available}
	}
	// QSV and VAAPI need upload/device arguments that vary by host. Report them
	// without selecting them so a detected encoder cannot break normal jobs.
	return Capabilities{Encoder: "libopenh264", Available: available}
}

func hasDevice(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func Run(input, kind, quality, outputFormat string) (string, string, error) {
	return RunContext(context.Background(), input, kind, quality, outputFormat)
}

func RunContext(ctx context.Context, input, kind, quality, outputFormat string) (string, string, error) {
	return RunContextWithProgress(ctx, input, kind, quality, outputFormat, nil)
}

func RunContextWithProgress(ctx context.Context, input, kind, quality, outputFormat string, onProgress func(VideoProgress)) (string, string, error) {
	work, err := os.MkdirTemp("", "fileswap-media-*")
	if err != nil {
		return "", "", err
	}

	switch strings.ToLower(kind) {
	case "image":
		if !isImage(input) {
			os.RemoveAll(work)
			return "", "", fmt.Errorf("choose a PNG, JPG, WEBP, GIF, or SVG image")
		}
		format, err := normalizeImageFormat(outputFormat)
		if err != nil {
			os.RemoveAll(work)
			return "", "", err
		}
		if format == "svg" && files.Ext(input) != ".svg" {
			os.RemoveAll(work)
			return "", "", fmt.Errorf("SVG output is available only when the source image is SVG")
		}
		out := filepath.Join(work, files.Stem(input)+"-optimized."+format)
		args := []string{input, "-auto-orient", "-strip"}
		if format != "svg" {
			args = append(args, imageQualityArgs(format, quality)...)
		}
		args = append(args, out)
		// Dimensions are retained. Each format uses its native quality/compression
		// controls instead of applying JPEG-style quality to every encoder.
		cmd := exec.CommandContext(ctx, "magick", args...)
		if log, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(work)
			return "", "", fmt.Errorf("image conversion failed: %v\n%s", err, log)
		}
		return out, imageMime(format), nil
	case "video":
		if !isVideo(input) {
			os.RemoveAll(work)
			return "", "", fmt.Errorf("choose an MP4, MOV, MKV, or WEBM video")
		}
		out := filepath.Join(work, files.Stem(input)+"-optimized.mp4")
		bitrate := "4M"
		if quality == "fast" {
			bitrate = "1.5M"
		} else if quality == "medium" {
			bitrate = "2M"
		}
		// OpenH264 is available in the supported FFmpeg build. Bitrate controls
		// provide predictable quality because OpenH264 does not use x264 CRF.
		info := probeVideo(ctx, input)
		totalFrames := info.Frames
		capability := CapabilitiesInfo()
		log.Printf("FFmpeg initialized")
		log.Printf("Input: %s", filepath.Base(input))
		log.Printf("Duration: %.2fs", info.Duration)
		log.Printf("Resolution: %dx%d", info.Width, info.Height)
		log.Printf("Encoder: %s", capability.Encoder)
		log.Printf("Profile: %s", strings.ToUpper(videoProfile(quality)))
		videoArgs := []string{"-c:v", capability.Encoder, "-b:v", bitrate, "-pix_fmt", "yuv420p"}
		if capability.Encoder == "h264_vaapi" || capability.Encoder == "h264_qsv" {
			videoArgs = []string{"-c:v", capability.Encoder, "-b:v", bitrate}
		}
		args := append([]string{"-hide_banner", "-nostats", "-y", "-threads", "0", "-i", input, "-map", "0:v:0", "-map", "0:a?"}, videoArgs...)
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", "-progress", "pipe:2", out)
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		stderr, err := cmd.StderrPipe()
		if err != nil {
			os.RemoveAll(work)
			return "", "", err
		}
		if err := cmd.Start(); err != nil {
			os.RemoveAll(work)
			return "", "", fmt.Errorf("video conversion failed: %w", err)
		}
		progressDone := make(chan struct{})
		go func() {
			parseProgress(stderr, totalFrames, info.Duration, onProgress)
			close(progressDone)
		}()
		waitErr := cmd.Wait()
		<-progressDone
		if waitErr != nil {
			os.RemoveAll(work)
			return "", "", fmt.Errorf("video conversion failed: %w", waitErr)
		}
		return out, "video/mp4", nil
	default:
		os.RemoveAll(work)
		return "", "", fmt.Errorf("unsupported media operation: %s", kind)
	}
}

func parseProgress(r io.Reader, totalFrames int64, duration float64, onProgress func(VideoProgress)) {
	if onProgress == nil {
		return
	}
	scanner := bufio.NewScanner(r)
	values := make(map[string]string)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[parts[0]] = parts[1]
		if parts[0] != "progress" {
			continue
		}
		log.Printf("[FFMPEG_PROGRESS_RAW] frame=%s fps=%s out_time_us=%s out_time_ms=%s out_time=%s speed=%s progress=%s",
			values["frame"], values["fps"], values["out_time_us"], values["out_time_ms"], values["out_time"], values["speed"], values["progress"])
		frame, _ := strconv.ParseInt(values["frame"], 10, 64)
		fps, _ := strconv.ParseFloat(values["fps"], 64)
		speed, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(values["speed"], "x")), 64)
		outMicros, _ := strconv.ParseInt(values["out_time_us"], 10, 64)
		if outMicros == 0 {
			outMicros, _ = strconv.ParseInt(values["out_time_ms"], 10, 64)
		}
		if outMicros == 0 {
			outMicros = parseTimestampMicros(values["out_time"])
		}
		elapsed := time.Duration(outMicros) * time.Microsecond
		percent := 0
		if duration > 0 {
			percent = int((elapsed.Seconds() / duration) * 100)
		} else if totalFrames > 0 {
			percent = int(float64(frame) * 100 / float64(totalFrames))
		}
		if values["progress"] == "end" {
			percent = 100
		} else if percent < 0 {
			percent = 0
		} else if percent > 99 {
			percent = 99
		}
		remaining := ""
		if percent > 0 && elapsed > 0 {
			estimatedTotal := elapsed / time.Duration(percent) * 100
			remainingDuration := estimatedTotal - elapsed
			if remainingDuration > 0 {
				remaining = formatDuration(remainingDuration)
			} else {
				remaining = "00:00"
			}
		}
		onProgress(VideoProgress{Frame: frame, TotalFrames: totalFrames, FPS: fps, Speed: speed, Percent: percent, Elapsed: formatDuration(elapsed), Remaining: remaining})
		outTime := values["out_time"]
		if outTime == "" {
			outTime = formatDuration(elapsed)
		}
		log.Printf("Progress: %d%% Frame: %d FPS: %.2f Speed: %.2fx Time: %s", percent, frame, fps, speed, outTime)
		values = make(map[string]string)
	}

	if scanner.Err() != nil {
		return
	}
}

func videoProfile(quality string) string {
	switch strings.ToLower(quality) {
	case "fast":
		return "FAST"
	case "high":
		return "HIGH"
	default:
		return "MEDIUM"
	}
}

func parseTimestampMicros(value string) int64 {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0
	}
	hours, errH := strconv.ParseInt(parts[0], 10, 64)
	minutes, errM := strconv.ParseInt(parts[1], 10, 64)
	seconds, errS := strconv.ParseFloat(parts[2], 64)
	if errH != nil || errM != nil || errS != nil {
		return 0
	}
	return (hours*3600+minutes*60)*1_000_000 + int64(seconds*1_000_000)
}

func probeVideo(ctx context.Context, input string) videoInfo {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=nb_frames,r_frame_rate,duration,width,height", "-show_entries", "format=duration", "-of", "json", input).Output()
	if err != nil {
		return videoInfo{}
	}
	var data struct {
		Streams []struct {
			Frames   string `json:"nb_frames"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Rate     string `json:"r_frame_rate"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &data) != nil || len(data.Streams) == 0 {
		return videoInfo{}
	}
	stream := data.Streams[0]
	info := videoInfo{}
	info.Frames, _ = strconv.ParseInt(stream.Frames, 10, 64)
	info.Width, info.Height = stream.Width, stream.Height
	info.Duration, _ = strconv.ParseFloat(stream.Duration, 64)
	if info.Duration == 0 {
		info.Duration, _ = strconv.ParseFloat(data.Format.Duration, 64)
	}
	rate := strings.Split(stream.Rate, "/")
	if len(rate) == 2 {
		numerator, _ := strconv.ParseFloat(rate[0], 64)
		denominator, _ := strconv.ParseFloat(rate[1], 64)
		if denominator > 0 {
			info.FPS = numerator / denominator
		}
	}
	return info
}

func formatDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	total := int(value.Round(time.Second) / time.Second)
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func isImage(path string) bool {
	switch files.Ext(path) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg":
		return true
	}
	return false
}

func normalizeImageFormat(value string) (string, error) {
	format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
	if format == "" {
		format = "webp"
	}
	switch format {
	case "jpg", "jpeg":
		return "jpg", nil
	case "png", "webp", "gif", "svg":
		return format, nil
	default:
		return "", fmt.Errorf("unsupported image output format: %s (use JPG, PNG, WEBP, GIF, or SVG)", value)
	}
}

func imageQualityArgs(format, quality string) []string {
	if strings.ToLower(quality) == "medium" {
		switch format {
		case "png":
			return []string{"-define", "png:compression-level=9"}
		case "gif":
			return []string{"-layers", "Optimize"}
		default:
			return []string{"-quality", "82"}
		}
	}
	switch format {
	case "png":
		return []string{"-define", "png:compression-level=6"}
	case "gif":
		return []string{"-layers", "Optimize"}
	default:
		return []string{"-quality", "92"}
	}
}

func imageMime(format string) string {
	switch format {
	case "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "avif":
		return "image/avif"
	case "gif":
		return "image/gif"
	case "svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func isVideo(path string) bool {
	switch files.Ext(path) {
	case ".mp4", ".mov", ".mkv", ".webm":
		return true
	}
	return false
}
