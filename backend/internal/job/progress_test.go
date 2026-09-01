package job

import "testing"

func TestUpdateProgressAcceptsZero(t *testing.T) {
	engine := NewEngine()
	created, err := engine.Create(Spec{InputFiles: []string{"input.mp4"}, Operation: "MEDIA", Processor: "media-medium"})
	if err != nil {
		t.Fatal(err)
	}
	engine.update(created.ID, Processing, 25)
	engine.UpdateProgress(created.ID, Progress{Percent: 0})
	current, ok := engine.Get(created.ID)
	if !ok {
		t.Fatal("job not found")
	}
	if current.Progress != 0 {
		t.Fatalf("expected progress 0, got %d", current.Progress)
	}
}
