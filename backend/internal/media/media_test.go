package media

import (
	"strings"
	"testing"
)

func TestParseProgressUsesDurationFallbackAndEnd(t *testing.T) {
	var updates []VideoProgress
	input := "frame=10\nfps=25.0\nout_time_us=5000000\nspeed=1.0x\nprogress=continue\n\nframe=20\nfps=25.0\nout_time_us=10000000\nspeed=1.0x\nprogress=end\n\n"
	parseProgress(strings.NewReader(input), 0, 10, func(progress VideoProgress) {
		updates = append(updates, progress)
	})
	if len(updates) != 2 {
		t.Fatalf("expected 2 progress updates, got %d", len(updates))
	}
	if updates[0].Percent != 50 || updates[0].Elapsed != "00:05" {
		t.Fatalf("unexpected duration fallback: %+v", updates[0])
	}
	if updates[1].Percent != 100 || updates[1].Frame != 20 {
		t.Fatalf("unexpected final progress: %+v", updates[1])
	}
}

func TestParseProgressInputFormatsAndClamps(t *testing.T) {
	var updates []VideoProgress
	input := "progress=begin\n\nframe=4\nfps=24.5\nout_time_ms=2500000\nspeed=0.8x\nprogress=continue\n\nframe=8\nfps=25\nout_time=00:00:20.000000\nspeed= 1.2x\nprogress=continue\n\nframe=9\nfps=25\nout_time_us=30000000\nspeed=1.2x\nprogress=end\n\n"
	parseProgress(strings.NewReader(input), 0, 10, func(progress VideoProgress) {
		updates = append(updates, progress)
	})
	if len(updates) != 4 {
		t.Fatalf("expected begin and three progress updates, got %d", len(updates))
	}
	if updates[0].Percent != 0 || updates[0].Frame != 0 {
		t.Fatalf("unexpected begin progress: %+v", updates[0])
	}
	if updates[1].Percent != 25 || updates[1].FPS != 24.5 || updates[1].Speed != 0.8 {
		t.Fatalf("unexpected out_time_ms progress: %+v", updates[1])
	}
	if updates[2].Percent != 99 || updates[2].Speed != 1.2 || updates[3].Percent != 100 {
		t.Fatalf("expected progress values to clamp at 99/100: %+v", updates)
	}
}

func TestParseTimestampMicros(t *testing.T) {
	if got := parseTimestampMicros("01:02:03.500000"); got != 3_723_500_000 {
		t.Fatalf("unexpected timestamp: %d", got)
	}
}
