package processor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type Analysis struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

type Estimate struct {
	DurationSeconds int    `json:"durationSeconds"`
	Resource        string `json:"resource"`
}

type Plan struct {
	Operation string
	Options   map[string]string
}

type Result struct {
	Output string
}

type Contract interface {
	Analyze(context.Context, []string) (Analysis, error)
	CanHandle([]string, string) bool
	Estimate(Analysis, Plan) Estimate
	Process(context.Context, []string, Plan) (Result, error)
	Validate(context.Context, Result) error
	Cancel()
}

type Info struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

type Registry struct {
	mu         sync.RWMutex
	processors map[string]Contract
	metadata   map[string]Info
}

func NewRegistry() *Registry {
	r := &Registry{processors: make(map[string]Contract), metadata: make(map[string]Info)}
	r.Register("PDFProcessor", "PDF", PDFProcessor{})
	r.Register("DocumentProcessor", "DOCUMENT", DocumentProcessor{})
	r.Register("ImageProcessor", "IMAGE", ImageProcessor{})
	r.Register("VideoProcessor", "MEDIA", VideoProcessor{})
	r.Register("AudioProcessor", "MEDIA", AudioProcessor{})
	r.Register("OCRProcessor", "OCR", OCRProcessor{})
	r.Register("ArchiveProcessor", "ARCHIVE", ArchiveProcessor{})
	return r
}

func (r *Registry) Register(name, category string, p Contract) error {
	if strings.TrimSpace(name) == "" || p == nil {
		return fmt.Errorf("processor name and implementation are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.processors[name]; exists {
		return fmt.Errorf("processor already registered: %s", name)
	}
	r.processors[name] = p
	r.metadata[name] = Info{Name: name, Category: category}
	return nil
}

func (r *Registry) Get(name string) (Contract, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.processors[name]
	return p, ok
}

func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Info, 0, len(r.metadata))
	for _, info := range r.metadata {
		result = append(result, info)
	}
	return result
}

type baseProcessor struct{}

func (baseProcessor) Analyze(_ context.Context, inputs []string) (Analysis, error) {
	if len(inputs) == 0 {
		return Analysis{}, fmt.Errorf("processor requires at least one input")
	}
	return Analysis{Files: len(inputs)}, nil
}

func (baseProcessor) Estimate(_ Analysis, plan Plan) Estimate {
	return Estimate{DurationSeconds: 1, Resource: plan.Operation}
}

func (baseProcessor) Process(_ context.Context, _ []string, _ Plan) (Result, error) {
	return Result{}, fmt.Errorf("processor implementation is not connected to an operation")
}

func (baseProcessor) Validate(_ context.Context, result Result) error {
	if strings.TrimSpace(result.Output) == "" {
		return fmt.Errorf("processor produced no output")
	}
	return nil
}

func (baseProcessor) Cancel() {}

type PDFProcessor struct{ baseProcessor }
type DocumentProcessor struct{ baseProcessor }
type ImageProcessor struct{ baseProcessor }
type VideoProcessor struct{ baseProcessor }
type AudioProcessor struct{ baseProcessor }
type OCRProcessor struct{ baseProcessor }
type ArchiveProcessor struct{ baseProcessor }

func (PDFProcessor) CanHandle(inputs []string, operation string) bool {
	return hasExtension(inputs, ".pdf") || strings.Contains(strings.ToLower(operation), "pdf")
}
func (DocumentProcessor) CanHandle(inputs []string, operation string) bool {
	return hasExtension(inputs, ".doc", ".docx", ".txt") || strings.Contains(strings.ToLower(operation), "document")
}
func (ImageProcessor) CanHandle(inputs []string, operation string) bool {
	return hasExtension(inputs, ".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg") || strings.Contains(strings.ToLower(operation), "image")
}
func (VideoProcessor) CanHandle(inputs []string, operation string) bool {
	return hasExtension(inputs, ".mp4", ".mov", ".mkv", ".webm") || strings.Contains(strings.ToLower(operation), "video")
}
func (AudioProcessor) CanHandle(inputs []string, operation string) bool {
	return hasExtension(inputs, ".mp3", ".wav", ".ogg", ".m4a", ".flac") || strings.Contains(strings.ToLower(operation), "audio")
}
func (OCRProcessor) CanHandle(inputs []string, operation string) bool {
	return strings.Contains(strings.ToLower(operation), "ocr")
}
func (ArchiveProcessor) CanHandle(inputs []string, operation string) bool {
	return hasExtension(inputs, ".zip", ".tar", ".gz", ".7z") || strings.Contains(strings.ToLower(operation), "archive")
}

func hasExtension(inputs []string, extensions ...string) bool {
	for _, input := range inputs {
		ext := strings.ToLower(filepath.Ext(input))
		for _, candidate := range extensions {
			if ext == candidate {
				return true
			}
		}
	}
	return false
}
