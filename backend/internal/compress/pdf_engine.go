package compress

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type PDFMetadata struct {
	SizeBytes        int64    `json:"sizeBytes"`
	PageCount        *int     `json:"pageCount,omitempty"`
	Version          string   `json:"version,omitempty"`
	Encrypted        *bool    `json:"encrypted,omitempty"`
	HasImages        bool     `json:"hasImages"`
	ImageCount       int      `json:"imageCount"`
	TotalImageBytes  *int64   `json:"totalImageBytes,omitempty"`
	ImageFormats     []string `json:"imageFormats,omitempty"`
	ImageResolutions []string `json:"imageResolutions,omitempty"`
	FontCount        *int     `json:"fontCount,omitempty"`
	EmbeddedFiles    *int     `json:"embeddedFileCount,omitempty"`
	HasMetadata      *bool    `json:"hasMetadata,omitempty"`
}

type PDFClassification string

const (
	TextHeavy        PDFClassification = "TEXT_HEAVY"
	ImageHeavy       PDFClassification = "IMAGE_HEAVY"
	ScanDocument     PDFClassification = "SCAN_DOCUMENT"
	Mixed            PDFClassification = "MIXED"
	AlreadyOptimized PDFClassification = "ALREADY_OPTIMIZED"
)

type PDFCompressionStrategy struct {
	Profile              string            `json:"profile"`
	Classification       PDFClassification `json:"classification"`
	Setting              string            `json:"setting"`
	ImageQuality         int               `json:"imageQuality"`
	DownsampleResolution int               `json:"downsampleResolution"`
	ColorConversion      string            `json:"colorConversion"`
	OptimizeStructure    bool              `json:"optimizeStructure"`
	Reason               string            `json:"reason"`
	Candidates           []PDFCandidate    `json:"-"`
}

type PDFCandidate struct {
	Name                 string `json:"name"`
	ImageQuality         int    `json:"imageQuality"`
	ColorPolicy          string `json:"colorPolicy"`
	DownsampleResolution int    `json:"downsampleResolution"`
	DownsampleMethod     string `json:"downsampleMethod"`
	CompressionPreset    string `json:"compressionPreset"`
	EstimatedQuality     string `json:"estimatedQuality"`
	EstimatedCompression string `json:"estimatedCompression"`
	Rationale            string `json:"rationale"`
}

type PDFQualityResult struct {
	Metric      string           `json:"metric"`
	Score       *float64         `json:"score,omitempty"`
	Status      PDFQualityStatus `json:"status"`
	SamplePages []int            `json:"samplePages,omitempty"`
	DPI         int              `json:"dpi,omitempty"`
	Reasons     []string         `json:"reasons,omitempty"`
	Warnings    []string         `json:"warnings,omitempty"`
}

type PDFCandidateResult struct {
	CandidateID          string            `json:"candidateID"`
	Profile              string            `json:"profile"`
	Classification       PDFClassification `json:"classification"`
	SamplePageCount      int               `json:"samplePageCount"`
	SamplePages          []int             `json:"samplePages,omitempty"`
	SampleInputBytes     int64             `json:"sampleInputBytes"`
	SampleOutputBytes    int64             `json:"sampleOutputBytes"`
	EstimatedCompression float64           `json:"estimatedCompressionPercent"`
	Valid                bool              `json:"valid"`
	PageCountPreserved   bool              `json:"pageCountPreserved"`
	Quality              PDFQualityResult  `json:"quality"`
	QualityMetric        string            `json:"qualityMetric,omitempty"`
	QualityScore         *float64          `json:"qualityScore,omitempty"`
	QualityStatus        PDFQualityStatus  `json:"qualityStatus"`
	ProcessingTimeMs     int64             `json:"processingTimeMs,omitempty"`
	StructuralValid      bool              `json:"structuralValid"`
	SizeGatePassed       bool              `json:"sizeGatePassed"`
	Decision             string            `json:"decision"`
	Reason               string            `json:"reason,omitempty"`
}

type PDFQualityStatus string

const (
	QualityUnassessed PDFQualityStatus = "QUALITY_UNASSESSED"
	QualityExcellent  PDFQualityStatus = "EXCELLENT"
	QualityVeryGood   PDFQualityStatus = "VERY_GOOD"
	QualityGood       PDFQualityStatus = "GOOD"
	QualityAcceptable PDFQualityStatus = "ACCEPTABLE"
	QualityPoor       PDFQualityStatus = "POOR"
)

type PDFBenchmark struct {
	OriginalBytes     int64                  `json:"originalBytes"`
	OutputBytes       int64                  `json:"outputBytes"`
	SavedBytes        int64                  `json:"savedBytes"`
	SavedPercent      float64                `json:"savedPercent"`
	PageCount         *int                   `json:"pageCount,omitempty"`
	ProcessingTimeMs  int64                  `json:"processingTimeMs"`
	Classification    PDFClassification      `json:"classification"`
	Profile           string                 `json:"profile"`
	Strategy          PDFCompressionStrategy `json:"strategy"`
	Quality           PDFQualityStatus       `json:"quality"`
	Decision          string                 `json:"decision,omitempty"`
	DecisionReason    string                 `json:"decisionReason,omitempty"`
	QualityReason     string                 `json:"qualityReason,omitempty"`
	InputPages        *int                   `json:"inputPages,omitempty"`
	OutputPages       *int                   `json:"outputPages,omitempty"`
	CandidateCount    int                    `json:"candidateCount"`
	SelectedCandidate string                 `json:"selectedCandidate,omitempty"`
	SampleUsed        bool                   `json:"sampleUsed"`
	SamplePages       []int                  `json:"samplePages,omitempty"`
	SampleDPI         int                    `json:"sampleDPI,omitempty"`
	CandidateResults  []PDFCandidateResult   `json:"candidateResults,omitempty"`
	FinalQuality      PDFQualityResult       `json:"finalQuality"`
	SelectionReason   string                 `json:"selectionReason,omitempty"`
	AttemptCount      int                    `json:"attemptCount"`
	PrimaryCandidate  string                 `json:"primaryCandidate,omitempty"`
	FallbackCandidate string                 `json:"fallbackCandidate,omitempty"`
	FallbackUsed      bool                   `json:"fallbackUsed"`
	PrimaryDecision   string                 `json:"primaryDecision,omitempty"`
	PrimaryReason     string                 `json:"primaryDecisionReason,omitempty"`
	FallbackDecision  string                 `json:"fallbackDecision,omitempty"`
	FallbackReason    string                 `json:"fallbackDecisionReason,omitempty"`
}

const (
	AcceptCompressed = "ACCEPT_COMPRESSED"
	RetainOriginal   = "RETAIN_ORIGINAL"
)

func DecidePDFAcceptance(originalBytes, outputBytes int64, inputPages, outputPages *int, structurallyValid bool, quality PDFQualityResult) (string, string) {
	if !structurallyValid {
		return RetainOriginal, "output PDF failed structural validation"
	}
	if inputPages == nil || outputPages == nil || *inputPages != *outputPages {
		return RetainOriginal, "output page count is not preserved"
	}
	if outputBytes <= 0 {
		return RetainOriginal, "output PDF is empty"
	}
	if outputBytes >= originalBytes {
		return RetainOriginal, "output is not smaller than original"
	}
	if quality.Status == QualityPoor {
		return RetainOriginal, "final visual quality is POOR"
	}
	if quality.Status == QualityUnassessed {
		return AcceptCompressed, "visual quality unavailable; accepted by explicit structural and size policy"
	}
	return AcceptCompressed, "output passed structural, page-count, size, and visual quality gates"
}

const (
	SmallPDFThreshold = 250 * 1024
	LargePDFThreshold = 10 * 1024 * 1024
)

func ClassifyPDFQuality(savedPercent float64, strategy PDFCompressionStrategy) PDFQualityStatus {
	if strategy.Classification == ScanDocument && savedPercent > 50 {
		return QualityAcceptable
	}
	switch {
	case savedPercent >= 40:
		return QualityVeryGood
	case savedPercent >= 15:
		return QualityGood
	case savedPercent > 0:
		return QualityAcceptable
	default:
		return QualityUnassessed
	}
}

func AnalyzePDF(ctx context.Context, path string) (PDFMetadata, error) {
	info, err := os.Stat(path)
	if err != nil {
		return PDFMetadata{}, fmt.Errorf("stat PDF: %w", err)
	}
	pdfinfo, err := exec.CommandContext(ctx, "pdfinfo", path).Output()
	if err != nil {
		return PDFMetadata{}, fmt.Errorf("analyze PDF: %w", err)
	}
	result := PDFMetadata{SizeBytes: info.Size()}
	parsePDFInfo(string(pdfinfo), &result)
	if images, err := exec.CommandContext(ctx, "pdfimages", "-list", path).Output(); err == nil {
		parsePDFImages(string(images), &result)
	} else if ctx.Err() != nil {
		return PDFMetadata{}, ctx.Err()
	}
	if fonts, err := exec.CommandContext(ctx, "pdffonts", path).Output(); err == nil {
		count := countTableRows(string(fonts))
		result.FontCount = &count
	}
	if embedded, err := exec.CommandContext(ctx, "pdfdetach", "-list", path).Output(); err == nil {
		count := countEmbeddedFiles(string(embedded))
		result.EmbeddedFiles = &count
	}
	return result, nil
}

func SelectPDFStrategy(metadata PDFMetadata, profile string) (PDFCompressionStrategy, error) {
	profile = strings.ToUpper(strings.TrimSpace(profile))
	if profile != "FAST" && profile != "MEDIUM" && profile != "HIGH" {
		return PDFCompressionStrategy{}, fmt.Errorf("unsupported PDF compression profile: %s", profile)
	}
	classification := classifyPDF(metadata)
	setting := "/ebook"
	imageQuality, resolution := 82, 150
	if profile == "HIGH" {
		imageQuality, resolution = 88, 200
	}
	if classification == ImageHeavy || classification == ScanDocument {
		if profile == "FAST" {
			imageQuality, resolution = 75, 120
		} else if profile == "MEDIUM" {
			imageQuality, resolution = 82, 150
		} else {
			imageQuality, resolution = 88, 200
		}
	} else if classification == TextHeavy {
		imageQuality, resolution = 92, 300
		if profile == "HIGH" {
			setting = "/printer"
		}
	}
	if classification == AlreadyOptimized {
		imageQuality, resolution = 92, 300
	}
	reason := "Mixed content requires balanced structural and image optimization."
	switch classification {
	case TextHeavy:
		reason = "Text-heavy PDF: prioritize readability and structural optimization over aggressive image recompression."
	case ImageHeavy:
		reason = "Image-heavy PDF: focus compression on image streams while preserving page structure."
	case ScanDocument:
		reason = "Scanned document: use conservative image optimization to preserve visual readability."
	case AlreadyOptimized:
		reason = "PDF is already compact: avoid forcing aggressive recompression."
	}
	strategy := PDFCompressionStrategy{
		Profile: profile, Classification: classification, Setting: setting,
		ImageQuality: imageQuality, DownsampleResolution: resolution,
		ColorConversion: "PreserveColor", OptimizeStructure: true, Reason: reason,
	}
	strategy.Candidates = GeneratePDFCandidates(metadata, strategy)
	return strategy, nil
}

func GeneratePDFCandidates(metadata PDFMetadata, strategy PDFCompressionStrategy) []PDFCandidate {
	if strategy.Classification == AlreadyOptimized {
		return nil
	}
	base := PDFCandidate{
		Name: "balanced", ImageQuality: strategy.ImageQuality,
		ColorPolicy: strategy.ColorConversion, DownsampleResolution: strategy.DownsampleResolution,
		DownsampleMethod: "Bicubic", CompressionPreset: strategy.Setting,
		EstimatedQuality: "balanced", EstimatedCompression: "moderate",
		Rationale: strategy.Reason,
	}
	conservative := base
	conservative.Name, conservative.ImageQuality, conservative.DownsampleResolution = "conservative", 92, 300
	conservative.EstimatedQuality, conservative.EstimatedCompression = "high", "low"
	conservative.Rationale = "Preserve readability with conservative image handling."
	aggressive := base
	aggressive.Name, aggressive.ImageQuality, aggressive.DownsampleResolution = "aggressive", 72, 120
	aggressive.EstimatedQuality, aggressive.EstimatedCompression = "acceptable", "high"
	aggressive.Rationale = "Reduce large image contribution while retaining document structure."
	switch strategy.Classification {
	case TextHeavy:
		return []PDFCandidate{conservative, base}
	case ImageHeavy, ScanDocument:
		if metadata.SizeBytes >= LargePDFThreshold || strategy.Profile == "HIGH" {
			return []PDFCandidate{conservative, base, aggressive}
		}
		return []PDFCandidate{base, aggressive}
	default:
		return []PDFCandidate{conservative, base}
	}
}

func SelectPDFCandidate(strategy PDFCompressionStrategy) (PDFCandidate, bool) {
	if len(strategy.Candidates) == 0 {
		return PDFCandidate{}, false
	}
	switch strategy.Profile {
	case "FAST":
		return strategy.Candidates[0], true
	case "HIGH":
		return strategy.Candidates[len(strategy.Candidates)-1], true
	default:
		return strategy.Candidates[len(strategy.Candidates)/2], true
	}
}

func SamplePDFPages(pageCount *int) []int {
	if pageCount == nil || *pageCount <= 0 {
		return nil
	}
	n := *pageCount
	switch {
	case n <= 5:
		return []int{1}
	case n <= 20:
		return []int{1, n}
	case n <= 100:
		return []int{1, (n + 1) / 2, n}
	default:
		return []int{1, (n + 3) / 4, (n + 1) / 2, (3*n + 1) / 4, n}
	}
}

func ValidatePDFOutput(ctx context.Context, input, output string) (PDFQualityResult, error) {
	inputMeta, err := AnalyzePDF(ctx, input)
	if err != nil {
		return PDFQualityResult{Status: QualityPoor, Reasons: []string{"input PDF could not be analyzed"}}, err
	}
	outputMeta, err := AnalyzePDF(ctx, output)
	if err != nil {
		return PDFQualityResult{Status: QualityPoor, Reasons: []string{"output PDF is not readable"}}, err
	}
	if inputMeta.PageCount == nil || outputMeta.PageCount == nil || *inputMeta.PageCount != *outputMeta.PageCount {
		return PDFQualityResult{Status: QualityPoor, Reasons: []string{"page count changed or is unavailable"}}, fmt.Errorf("PDF page count validation failed")
	}
	result := PDFQualityResult{Metric: "NONE", Status: QualityUnassessed, Reasons: []string{"visual quality measurement is performed separately"}}
	return result, nil
}

const PDFQualityDPI = 150
const PDFQualityMaxDimension = 1600

func MeasurePDFQuality(ctx context.Context, original, compressed string, pages []int) (PDFQualityResult, error) {
	if len(pages) == 0 {
		return PDFQualityResult{Metric: "NONE", Status: QualityUnassessed, Reasons: []string{"no sample pages available"}}, nil
	}
	dir, err := os.MkdirTemp("", "fileswap-pdf-quality-*")
	if err != nil {
		return PDFQualityResult{}, err
	}
	defer os.RemoveAll(dir)
	var total float64
	metric := "SSIM"
	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return PDFQualityResult{}, err
		}
		originalImage := filepath.Join(dir, fmt.Sprintf("original-%03d", page))
		compressedImage := filepath.Join(dir, fmt.Sprintf("compressed-%03d", page))
		if err := renderPDFPage(ctx, original, page, originalImage); err != nil {
			return PDFQualityResult{Metric: "NONE", Status: QualityUnassessed, SamplePages: pages, DPI: PDFQualityDPI, Reasons: []string{"original rendering failed"}}, err
		}
		if err := renderPDFPage(ctx, compressed, page, compressedImage); err != nil {
			return PDFQualityResult{Metric: "NONE", Status: QualityUnassessed, SamplePages: pages, DPI: PDFQualityDPI, Reasons: []string{"compressed rendering failed"}}, err
		}
		output, err := exec.CommandContext(ctx, "compare", "-metric", "SSIM", originalImage+".png", compressedImage+".png", "null:").CombinedOutput()
		if err != nil && ctx.Err() != nil {
			return PDFQualityResult{}, ctx.Err()
		}
		score, parseErr := parseSSIM(string(output))
		if parseErr != nil {
			psnrOutput, psnrErr := exec.CommandContext(ctx, "compare", "-metric", "PSNR", originalImage+".png", compressedImage+".png", "null:").CombinedOutput()
			if psnrErr != nil && ctx.Err() != nil {
				return PDFQualityResult{}, ctx.Err()
			}
			score, parseErr = parsePSNR(string(psnrOutput))
			if parseErr != nil {
				return PDFQualityResult{Metric: "NONE", Status: QualityUnassessed, SamplePages: pages, DPI: PDFQualityDPI, Reasons: []string{"SSIM and PSNR unavailable"}}, nil
			}
			metric = "PSNR"
			total += score
			continue
		}
		total += score
	}
	score := total / float64(len(pages))
	status := classifySSIM(score)
	if metric == "PSNR" {
		status = classifyPSNR(score)
	}
	return PDFQualityResult{Metric: metric, Score: &score, Status: status, SamplePages: pages, DPI: PDFQualityDPI}, nil
}

func renderPDFPage(ctx context.Context, input string, page int, outputBase string) error {
	return exec.CommandContext(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-r", strconv.Itoa(PDFQualityDPI), "-scale-to", strconv.Itoa(PDFQualityMaxDimension), "-png", "-singlefile", input, outputBase).Run()
}

var ssimPattern = regexp.MustCompile(`\(([0-9]+(?:\.\d+)?)\)`)

func parseSSIM(value string) (float64, error) {
	match := ssimPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0, fmt.Errorf("invalid SSIM output")
	}
	distortion, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, err
	}
	return 1 - distortion, nil
}

func parsePSNR(value string) (float64, error) {
	fields := strings.Fields(value)
	for _, field := range fields {
		score, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err == nil && score > 0 {
			return score, nil
		}
	}
	return 0, fmt.Errorf("invalid PSNR output")
}

func classifySSIM(score float64) PDFQualityStatus {
	switch {
	case score >= 0.99:
		return QualityExcellent
	case score >= 0.97:
		return QualityVeryGood
	case score >= 0.94:
		return QualityGood
	case score >= 0.90:
		return QualityAcceptable
	default:
		return QualityPoor
	}
}

func classifyPSNR(score float64) PDFQualityStatus {
	switch {
	case score >= 45:
		return QualityExcellent
	case score >= 40:
		return QualityVeryGood
	case score >= 35:
		return QualityGood
	case score >= 30:
		return QualityAcceptable
	default:
		return QualityPoor
	}
}

func classifyPDF(metadata PDFMetadata) PDFClassification {
	if metadata.SizeBytes > 0 && metadata.SizeBytes < SmallPDFThreshold {
		return AlreadyOptimized
	}
	if metadata.ImageCount == 0 {
		return TextHeavy
	}
	if metadata.PageCount != nil && metadata.ImageCount >= *metadata.PageCount && metadata.ImageCount > 0 {
		return ScanDocument
	}
	if metadata.ImageCount >= 5 || (metadata.TotalImageBytes != nil && *metadata.TotalImageBytes > metadata.SizeBytes/2) {
		return ImageHeavy
	}
	return Mixed
}

func parsePDFInfo(value string, result *PDFMetadata) {
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		key, raw, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		raw = strings.TrimSpace(raw)
		switch strings.TrimSpace(key) {
		case "Pages":
			if n, err := strconv.Atoi(raw); err == nil {
				result.PageCount = &n
			}
		case "PDF version":
			result.Version = raw
		case "Encrypted":
			encrypted := strings.EqualFold(raw, "yes")
			result.Encrypted = &encrypted
		case "Metadata Stream":
			has := strings.EqualFold(raw, "yes")
			result.HasMetadata = &has
		}
	}
}

func parsePDFImages(value string, result *PDFMetadata) {
	formats := make(map[string]struct{})
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[0] == "page" || fields[0] == "-" {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		result.ImageCount++
		result.HasImages = true
		formats[fields[8]] = struct{}{}
		result.ImageResolutions = append(result.ImageResolutions, fields[3]+"x"+fields[4])
		if len(fields) > 14 {
			if bytes, err := parseImageSize(fields[14]); err == nil && bytes > 0 {
				total := bytes
				if result.TotalImageBytes != nil {
					total += *result.TotalImageBytes
				}
				result.TotalImageBytes = &total
			}
		}
	}

	for format := range formats {
		result.ImageFormats = append(result.ImageFormats, format)
	}
}

func parseImageSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(value, "K"):
		multiplier = 1024
		value = strings.TrimSuffix(value, "K")
	case strings.HasSuffix(value, "M"):
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "M")
	case strings.HasSuffix(value, "B"):
		value = strings.TrimSuffix(value, "B")
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(multiplier)), nil
}

func countTableRows(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && !strings.HasPrefix(fields[0], "-") && fields[0] != "name" {
			count++
		}
	}
	if count > 2 {
		return count - 2
	}
	return 0
}

func countEmbeddedFiles(value string) int {
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				return n
			}
		}
	}
	return 0
}
