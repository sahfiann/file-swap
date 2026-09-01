package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeBasicFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Analyze([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Type != "TXT" || report.Size != 5 || report.Complexity != "LOW" {
		t.Fatalf("unexpected report: %#v", report)
	}
}
