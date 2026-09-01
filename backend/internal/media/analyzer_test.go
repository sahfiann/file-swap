package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAnalyzeVideoReadsRealMetadata(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not available")
	}
	input := filepath.Join(t.TempDir(), "fixture.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=640x480:rate=24", "-t", "2", "-pix_fmt", "yuv420p", input)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %v: %s", err, output)
	}
	metadata, err := AnalyzeVideo(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.SizeBytes <= 0 || metadata.DurationSeconds == nil || *metadata.DurationSeconds <= 0 {
		t.Fatalf("missing real size/duration: %+v", metadata)
	}
	if metadata.Width == nil || *metadata.Width != 640 || metadata.Height == nil || *metadata.Height != 480 {
		t.Fatalf("unexpected real resolution: %+v", metadata)
	}
	if metadata.VideoCodec == "" || metadata.VideoBitrate == nil || metadata.FPS == nil {
		t.Fatalf("missing real video metadata: %+v", metadata)
	}
}
