package compress

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sahfiann/file-swap/internal/files"
)

type PDFBenchmarkSink struct {
	mu      sync.Mutex
	results []PDFCandidateResult
}

func NewPDFBenchmarkSink() *PDFBenchmarkSink {
	return &PDFBenchmarkSink{}
}

func (s *PDFBenchmarkSink) Add(result PDFCandidateResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, result)
}

func (s *PDFBenchmarkSink) Results() []PDFCandidateResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PDFCandidateResult(nil), s.results...)
}

type pdfBenchmarkSinkKey struct{}
type pdfDecisionSinkKey struct{}

type PDFDecisionMetadata struct {
	AttemptCount                                  int
	PrimaryCandidate, FallbackCandidate           string
	FallbackUsed                                  bool
	PrimaryDecision, PrimaryReason                string
	FallbackDecision, FallbackReason              string
	FinalDecision, FinalReason, SelectedCandidate string
	FinalQuality                                  PDFQualityResult
}

func NewPDFDecisionMetadata() *PDFDecisionMetadata { return &PDFDecisionMetadata{} }
func WithPDFDecisionMetadata(ctx context.Context, metadata *PDFDecisionMetadata) context.Context {
	return context.WithValue(ctx, pdfDecisionSinkKey{}, metadata)
}
func pdfDecisionMetadataFromContext(ctx context.Context) *PDFDecisionMetadata {
	metadata, _ := ctx.Value(pdfDecisionSinkKey{}).(*PDFDecisionMetadata)
	return metadata
}

func WithPDFBenchmarkSink(ctx context.Context, sink *PDFBenchmarkSink) context.Context {
	return context.WithValue(ctx, pdfBenchmarkSinkKey{}, sink)
}

func pdfBenchmarkSinkFromContext(ctx context.Context) *PDFBenchmarkSink {
	sink, _ := ctx.Value(pdfBenchmarkSinkKey{}).(*PDFBenchmarkSink)
	return sink
}

func recordPDFCandidate(ctx context.Context, result PDFCandidateResult) {
	if sink := pdfBenchmarkSinkFromContext(ctx); sink != nil {
		sink.Add(result)
	}
}

func recordPDFDecision(ctx context.Context, metadata PDFDecisionMetadata) {
	if sink := pdfDecisionMetadataFromContext(ctx); sink != nil {
		sink.AttemptCount = metadata.AttemptCount
		sink.PrimaryCandidate, sink.FallbackCandidate = metadata.PrimaryCandidate, metadata.FallbackCandidate
		sink.FallbackUsed = metadata.FallbackUsed
		sink.PrimaryDecision, sink.PrimaryReason = metadata.PrimaryDecision, metadata.PrimaryReason
		sink.FallbackDecision, sink.FallbackReason = metadata.FallbackDecision, metadata.FallbackReason
		sink.FinalDecision, sink.FinalReason, sink.SelectedCandidate = metadata.FinalDecision, metadata.FinalReason, metadata.SelectedCandidate
		sink.FinalQuality = metadata.FinalQuality
	}
}

func (s *PDFDecisionMetadata) Snapshot() PDFDecisionMetadata {
	return PDFDecisionMetadata{
		AttemptCount: s.AttemptCount, PrimaryCandidate: s.PrimaryCandidate,
		FallbackCandidate: s.FallbackCandidate, FallbackUsed: s.FallbackUsed,
		PrimaryDecision: s.PrimaryDecision, PrimaryReason: s.PrimaryReason,
		FallbackDecision: s.FallbackDecision, FallbackReason: s.FallbackReason,
		FinalDecision: s.FinalDecision, FinalReason: s.FinalReason,
		SelectedCandidate: s.SelectedCandidate, FinalQuality: s.FinalQuality,
	}
}

func Run(inputPaths []string, quality string) (outPath string, mime string, err error) {
	return RunContext(context.Background(), inputPaths, quality)
}

func RunContext(ctx context.Context, inputPaths []string, quality string) (outPath string, mime string, err error) {
	if len(inputPaths) == 0 {
		return "", "", fmt.Errorf("no files to compress")
	}

	quality = strings.ToLower(quality)
	if quality == "" {
		quality = "medium"
	}

	allPDF := true
	for _, p := range inputPaths {
		if files.Ext(p) != ".pdf" {
			allPDF = false
			break
		}
	}

	work, err := os.MkdirTemp("", "fileswap-zip-*")
	if err != nil {
		return "", "", err
	}

	if allPDF {
		compressed := make([]string, 0, len(inputPaths))
		for i, p := range inputPaths {
			dest := filepath.Join(work, fmt.Sprintf("%s-compressed.pdf", files.Stem(p)))
			if len(inputPaths) > 1 {
				dest = filepath.Join(work, fmt.Sprintf("%02d-%s.pdf", i+1, files.Stem(p)))
			}
			if err := compressPDFContext(ctx, p, dest, quality); err != nil {
				os.RemoveAll(work)
				return "", "", err
			}
			compressed = append(compressed, dest)
		}
		if len(compressed) == 1 {
			return compressed[0], "application/pdf", nil
		}
		zipPath := filepath.Join(work, "compressed.zip")
		if err := writeZip(compressed, zipPath); err != nil {
			os.RemoveAll(work)
			return "", "", err
		}
		return zipPath, "application/zip", nil
	}

	zipPath := filepath.Join(work, "compressed.zip")
	if err := writeZip(inputPaths, zipPath); err != nil {
		os.RemoveAll(work)
		return "", "", err
	}
	return zipPath, "application/zip", nil
}

func compressPDF(in, out, quality string) error {
	return compressPDFContext(context.Background(), in, out, quality)
}

func compressPDFContext(ctx context.Context, in, out, quality string) error {
	metadata, err := AnalyzePDF(ctx, in)
	if err != nil {
		return err
	}
	strategy, err := SelectPDFStrategy(metadata, profileName(quality))
	if err != nil {
		return err
	}
	if strategy.Classification == AlreadyOptimized {
		fmt.Printf("[PDF_GATE] result=REJECT reason=already optimized; retaining original\n")
		return copyFile(in, out)
	}
	candidate, ok := SelectPDFCandidate(strategy)
	if !ok {
		fmt.Printf("[PDF_GATE] result=REJECT reason=no eligible candidate; retaining original\n")
		return copyFile(in, out)
	}
	decisionMetadata := PDFDecisionMetadata{PrimaryCandidate: candidate.Name}
	sampleSink := pdfBenchmarkSinkFromContext(ctx)
	if shouldSamplePDF(metadata, strategy) {
		sample, sampleErr := createPDFSample(ctx, in, metadata.PageCount)
		if sampleErr != nil {
			return sampleErr
		}
		defer os.RemoveAll(filepath.Dir(sample))
		if evaluated, evalErr := evaluatePDFCandidates(ctx, sample, strategy.Candidates, strategy.Profile, strategy.Classification); evalErr != nil {
			return evalErr
		} else if evaluated.Name != candidate.Name {
			candidate = evaluated
		}
		decisionMetadata.PrimaryCandidate = candidate.Name
	}
	work, err := os.MkdirTemp("", "fileswap-pdf-full-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	inInfo, err := os.Stat(in)
	if err != nil {
		return err
	}
	primaryOut := filepath.Join(work, "primary.pdf")
	decisionMetadata.AttemptCount = 1
	primaryDecision, primaryReason, primaryQuality := evaluateFullPDF(ctx, in, primaryOut, candidate, metadata, inInfo.Size())
	decisionMetadata.PrimaryDecision, decisionMetadata.PrimaryReason, decisionMetadata.FinalQuality = primaryDecision, primaryReason, primaryQuality
	if primaryDecision == AcceptCompressed {
		decisionMetadata.FinalDecision, decisionMetadata.FinalReason, decisionMetadata.SelectedCandidate = primaryDecision, primaryReason, candidate.Name
		recordPDFDecision(ctx, decisionMetadata)
		return copyFile(primaryOut, out)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var fallback PDFCandidate
	if sampleSink != nil {
		fallback, _ = selectPDFFallback(strategy, candidate.Name, sampleSink.Results())
	}
	if fallback.Name != "" {
		decisionMetadata.FallbackCandidate, decisionMetadata.FallbackUsed = fallback.Name, true
		fallbackOut := filepath.Join(work, "fallback.pdf")
		decisionMetadata.AttemptCount = 2
		fallbackDecision, fallbackReason, fallbackQuality := evaluateFullPDF(ctx, in, fallbackOut, fallback, metadata, inInfo.Size())
		decisionMetadata.FallbackDecision, decisionMetadata.FallbackReason, decisionMetadata.FinalQuality = fallbackDecision, fallbackReason, fallbackQuality
		if fallbackDecision == AcceptCompressed {
			decisionMetadata.FinalDecision, decisionMetadata.FinalReason, decisionMetadata.SelectedCandidate = fallbackDecision, fallbackReason, fallback.Name
			recordPDFDecision(ctx, decisionMetadata)
			return copyFile(fallbackOut, out)
		}
	}
	decisionMetadata.FinalDecision, decisionMetadata.FinalReason, decisionMetadata.SelectedCandidate = RetainOriginal, "all full-encode candidates failed acceptance", "retain-original"
	recordPDFDecision(ctx, decisionMetadata)
	return copyFile(in, out)
}

func evaluateFullPDF(ctx context.Context, in, out string, candidate PDFCandidate, metadata PDFMetadata, originalBytes int64) (string, string, PDFQualityResult) {
	if err := encodePDF(ctx, in, out, candidate, metadata.HasImages); err != nil {
		return RetainOriginal, "full encode failed", PDFQualityResult{Metric: "NONE", Status: QualityUnassessed}
	}

	info, err := os.Stat(out)
	if err != nil {
		return RetainOriginal, "output is missing", PDFQualityResult{Metric: "NONE", Status: QualityUnassessed}
	}
	structural, err := ValidatePDFOutput(ctx, in, out)
	if err != nil {
		return RetainOriginal, "output PDF failed structural validation", structural
	}
	outputMetadata, outputErr := AnalyzePDF(ctx, out)
	if outputErr != nil {
		return RetainOriginal, "output PDF could not be analyzed", structural
	}
	quality := PDFQualityResult{Metric: "NONE", Status: QualityUnassessed}
	if metadata.PageCount != nil {
		quality, err = MeasurePDFQuality(ctx, in, out, SamplePDFPages(metadata.PageCount))
		if err != nil {
			return RetainOriginal, "final visual quality measurement failed", quality
		}
	}
	decision, reason := DecidePDFAcceptance(originalBytes, info.Size(), metadata.PageCount, outputMetadata.PageCount, true, quality)
	return decision, reason, quality
}

func selectPDFFallback(strategy PDFCompressionStrategy, primary string, results []PDFCandidateResult) (PDFCandidate, bool) {
	byName := make(map[string]PDFCandidateResult, len(results))
	for _, result := range results {
		byName[result.CandidateID] = result
	}
	primarySeen := false
	for _, candidate := range strategy.Candidates {
		if candidate.Name == primary {
			primarySeen = true
			continue
		}
		if !primarySeen {
			continue
		}
		result, ok := byName[candidate.Name]
		if ok && result.StructuralValid && result.PageCountPreserved &&
			result.SizeGatePassed && result.QualityStatus != QualityPoor {
			return candidate, true
		}
	}
	return PDFCandidate{}, false
}

func shouldSamplePDF(metadata PDFMetadata, strategy PDFCompressionStrategy) bool {
	return metadata.SizeBytes >= LargePDFThreshold &&
		(strategy.Classification == ImageHeavy || strategy.Classification == ScanDocument) &&
		len(strategy.Candidates) > 1
}

func createPDFSample(ctx context.Context, input string, pageCount *int) (string, error) {
	pages := SamplePDFPages(pageCount)
	if len(pages) == 0 {
		return "", fmt.Errorf("cannot sample PDF without page count")
	}
	dir, err := os.MkdirTemp("", "fileswap-pdf-sample-*")
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(pages))
	for _, page := range pages {
		part := filepath.Join(dir, fmt.Sprintf("page-%03d.pdf", page))
		cmd := exec.CommandContext(ctx, "pdfseparate", "-f", fmt.Sprintf("%d", page), "-l", fmt.Sprintf("%d", page), input, part)
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("create PDF sample: %v: %s", runErr, output)
		}
		parts = append(parts, part)
	}
	sample := filepath.Join(dir, "sample.pdf")
	cmd := exec.CommandContext(ctx, "pdfunite", append(parts, sample)...)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("combine PDF sample: %v: %s", runErr, output)
	}
	fmt.Printf("[PDF_SAMPLE] pages=%v\n", pages)
	return sample, nil
}

func evaluatePDFCandidates(ctx context.Context, sample string, candidates []PDFCandidate, profile string, classification PDFClassification) (PDFCandidate, error) {
	dir, err := os.MkdirTemp("", "fileswap-pdf-candidates-*")
	if err != nil {
		return PDFCandidate{}, err
	}
	defer os.RemoveAll(dir)
	var best PDFCandidate
	bestScore := -1.0
	sampleInfo, err := os.Stat(sample)
	if err != nil {
		return PDFCandidate{}, err
	}
	sampleMeta, err := AnalyzePDF(ctx, sample)
	if err != nil {
		return PDFCandidate{}, fmt.Errorf("analyze candidate sample: %w", err)
	}
	if sampleMeta.PageCount == nil {
		return PDFCandidate{}, fmt.Errorf("analyze candidate sample: page count unavailable")
	}
	samplePages := SamplePDFPages(sampleMeta.PageCount)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return PDFCandidate{}, err
		}
		output := filepath.Join(dir, candidate.Name+".pdf")
		started := time.Now()
		result := PDFCandidateResult{
			CandidateID: candidate.Name, Profile: profile, Classification: classification,
			SamplePageCount: len(samplePages), SamplePages: append([]int(nil), samplePages...),
			QualityStatus: QualityUnassessed, Decision: "REJECT",
		}
		if err := encodePDF(ctx, sample, output, candidate, true); err != nil {
			result.Reason = "sample encode failed"
			result.ProcessingTimeMs = time.Since(started).Milliseconds()
			recordPDFCandidate(ctx, result)
			fmt.Printf("[PDF_CANDIDATE] candidate=%s action=sample status=REJECT\n", candidate.Name)
			continue
		}
		if _, err := ValidatePDFOutput(ctx, sample, output); err != nil {
			result.Reason = "structural validation failed"
			result.ProcessingTimeMs = time.Since(started).Milliseconds()
			recordPDFCandidate(ctx, result)
			fmt.Printf("[PDF_CANDIDATE] candidate=%s action=sample status=REJECT\n", candidate.Name)
			continue
		}
		result.Valid, result.StructuralValid, result.PageCountPreserved = true, true, true
		info, err := os.Stat(output)
		if err != nil {
			return PDFCandidate{}, err
		}
		result.SampleInputBytes, result.SampleOutputBytes = sampleInfo.Size(), info.Size()
		quality, qualityErr := MeasurePDFQuality(ctx, sample, output, samplePages)
		result.Quality = quality
		result.QualityMetric, result.QualityScore, result.QualityStatus = quality.Metric, quality.Score, quality.Status
		if qualityErr != nil && ctx.Err() != nil {
			return PDFCandidate{}, ctx.Err()
		}
		if quality.Status == QualityPoor {
			result.Reason = "visual quality is POOR"
			result.ProcessingTimeMs = time.Since(started).Milliseconds()
			recordPDFCandidate(ctx, result)
			fmt.Printf("[PDF_CANDIDATE] candidate=%s action=sample status=REJECT quality=%s\n", candidate.Name, quality.Status)
			continue
		}
		if info.Size() >= sampleInfo.Size() {
			result.Reason = "output is not smaller"
			result.ProcessingTimeMs = time.Since(started).Milliseconds()
			recordPDFCandidate(ctx, result)
			fmt.Printf("[PDF_CANDIDATE] candidate=%s action=sample status=REJECT reason=output is not smaller\n", candidate.Name)
			continue
		}
		fmt.Printf("[PDF_CANDIDATE] candidate=%s action=sample status=ACCEPT output=%d\n", candidate.Name, info.Size())
		savedPercent := float64(sampleInfo.Size()-info.Size()) * 100 / float64(sampleInfo.Size())
		result.EstimatedCompression = savedPercent
		result.SizeGatePassed, result.Decision, result.Reason = true, "ACCEPT", "structural, quality, and size gates passed"
		result.ProcessingTimeMs = time.Since(started).Milliseconds()
		recordPDFCandidate(ctx, result)
		score := candidateScore(quality, savedPercent, profile)
		if best.Name == "" || score > bestScore || (score == bestScore && preferredCandidate(candidate, best, profile)) {
			best, bestScore = candidate, score
		}

	}
	if best.Name == "" {
		fmt.Printf("[PDF_CANDIDATE] action=sample status=NONE reason=all candidates failed; retaining conservative fallback\n")
		return candidates[0], nil
	}

	return best, nil
}

func candidateScore(quality PDFQualityResult, savedPercent float64, profile string) float64 {
	if quality.Status == QualityPoor {
		return -1
	}
	qualityScore := 0.0
	if quality.Score != nil {
		if quality.Metric == "SSIM" {
			qualityScore = *quality.Score
		} else if quality.Metric == "PSNR" {
			qualityScore = *quality.Score / 50
		}
	}
	compressionScore := savedPercent / 100
	switch profile {
	case "HIGH":
		return qualityScore*0.6 + compressionScore*0.4
	case "FAST":
		return qualityScore*0.3 + compressionScore*0.7
	default:
		return qualityScore*0.5 + compressionScore*0.5
	}
}

func preferredCandidate(candidate, current PDFCandidate, profile string) bool {
	order := map[string]int{"conservative": 0, "balanced": 1, "aggressive": 2}
	if profile == "HIGH" {
		return order[candidate.Name] > order[current.Name]
	}
	if profile == "FAST" {
		return order[candidate.Name] < order[current.Name]
	}
	return absInt(order[candidate.Name]-1) < absInt(order[current.Name]-1)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func encodePDF(ctx context.Context, input, output string, candidate PDFCandidate, hasImages bool) error {
	args := []string{"-sDEVICE=pdfwrite", "-dCompatibilityLevel=1.4",
		"-dPDFSETTINGS=" + candidate.CompressionPreset, "-dNOPAUSE", "-dQUIET", "-dBATCH",
		"-sOutputFile=" + output}
	if hasImages {
		args = append(args, "-dDownsampleColorImages=true",
			"-dColorImageResolution="+fmt.Sprintf("%d", candidate.DownsampleResolution),
			"-dGrayImageResolution="+fmt.Sprintf("%d", candidate.DownsampleResolution),
			"-dMonoImageResolution="+fmt.Sprintf("%d", candidate.DownsampleResolution*2),
			"-dJPEGQ="+fmt.Sprintf("%d", candidate.ImageQuality))
	}
	args = append(args, input)
	return exec.CommandContext(ctx, "gs", args...).Run()
}

func copyFile(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o644)
}

func profileName(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "low", "fast":
		return "FAST"
	case "high":
		return "HIGH"
	default:
		return "MEDIUM"
	}
}

func writeZip(paths []string, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	used := map[string]int{}
	for _, p := range paths {
		name := filepath.Base(p)
		if n := used[name]; n > 0 {
			ext := filepath.Ext(name)
			stem := strings.TrimSuffix(name, ext)
			name = fmt.Sprintf("%s-%d%s", stem, n+1, ext)
		}
		used[filepath.Base(p)]++

		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, src)
		src.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
