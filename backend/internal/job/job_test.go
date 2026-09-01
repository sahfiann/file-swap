package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sahfiann/file-swap/internal/analyzer"
)

func TestRunCompletesJobLifecycle(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	output := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(input, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(output, []byte("output"), 0600); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine()
	result, measurement, current, err := engine.Run(context.Background(), Spec{
		InputFiles: []string{input}, Operation: "CONVERT", Processor: "test-worker",
	}, func(context.Context) (string, error) {
		return output, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != output || current.Status != Completed || current.Progress != 100 {
		t.Fatalf("unexpected completed job: %#v", current)
	}
	if measurement.InputBytes != 5 || measurement.OutputBytes != 6 {
		t.Fatalf("unexpected measurement: %#v", measurement)
	}
	if current.InputFiles[0] != "input.txt" || current.Output != "output.txt" {
		t.Fatalf("job should expose file names only: %#v", current)
	}
}

func TestRunMarksProcessingFailure(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}

	_, _, current, err := NewEngine().Run(context.Background(), Spec{
		InputFiles: []string{input}, Operation: "CONVERT", Processor: "test-worker",
	}, func(context.Context) (string, error) {
		return "", os.ErrPermission
	})
	if err == nil || current.Status != Failed || current.Error == "" {
		t.Fatalf("expected failed job, got job=%#v err=%v", current, err)
	}
}

func TestWorkerPanicFailsJobAndWorkerContinues(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	output := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(input, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("output"), 0600); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine()
	_, _, current, err := engine.Run(context.Background(), Spec{
		InputFiles: []string{input}, Operation: "CONVERT", Processor: "panic-worker",
	}, func(context.Context) (string, error) {
		panic("simulated worker failure")
	})
	if err == nil || current.Status != Failed || current.Error == "" {
		t.Fatalf("expected failed job after panic, got job=%#v err=%v", current, err)
	}

	_, _, next, err := engine.Run(context.Background(), Spec{
		InputFiles: []string{input}, Operation: "CONVERT", Processor: "follow-up-worker",
	}, func(context.Context) (string, error) {
		return output, nil
	})
	if err != nil || next.Status != Completed {
		t.Fatalf("worker did not continue after panic: job=%#v err=%v", next, err)
	}
}

func TestWorkerManagerHasFixedCategories(t *testing.T) {
	engine := NewEngine()
	expected := map[WorkerCategory]int{WorkerPDF: 2, WorkerDocument: 2, WorkerImage: 4, WorkerMedia: 1, WorkerOCR: 1}
	stats := engine.Workers()
	if len(stats) != len(expected) {
		t.Fatalf("expected %d worker categories, got %d", len(expected), len(stats))
	}

	for _, stat := range stats {
		if stat.Total != expected[stat.Category] || stat.Active != 0 || stat.Available != stat.Total {
			t.Fatalf("unexpected worker stats: %#v", stat)
		}
	}

}

func TestScheduleForAnalyzerResults(t *testing.T) {
	largeVideo := analyzer.Report{Type: "MP4", Size: 2 * 1024 * 1024 * 1024, Complexity: "HIGH"}
	if got := scheduleFor(largeVideo, 1); got != "HIGH RESOURCE -> MEDIA WORKER" {
		t.Fatalf("unexpected video route: %s", got)
	}
	batchImage := analyzer.Report{Type: "JPEG", Size: 1000, Complexity: "LOW"}
	if got := scheduleFor(batchImage, 100); got != "BATCH IMAGE -> IMAGE WORKER" {
		t.Fatalf("unexpected image route: %s", got)
	}
}
