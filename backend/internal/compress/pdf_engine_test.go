package compress

import (
	"strings"
	"testing"
)

func TestSelectPDFStrategyUsesClassificationAndProfile(t *testing.T) {
	pageCount := 2
	imagePageCount := 10
	largeImages := int64(700000)
	tests := []struct {
		name           string
		metadata       PDFMetadata
		profile        string
		classification PDFClassification
		setting        string
	}{
		{"text fast", PDFMetadata{SizeBytes: 500000, PageCount: &pageCount}, "FAST", TextHeavy, "/ebook"},
		{"images medium", PDFMetadata{SizeBytes: 1000000, PageCount: &imagePageCount, ImageCount: 6, TotalImageBytes: &largeImages}, "MEDIUM", ImageHeavy, "/ebook"},
		{"scan high", PDFMetadata{SizeBytes: 1000000, PageCount: &pageCount, ImageCount: 2}, "HIGH", ScanDocument, "/ebook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy, err := SelectPDFStrategy(tt.metadata, tt.profile)
			if err != nil {
				t.Fatal(err)
			}
			if strategy.Classification != tt.classification || strategy.Setting != tt.setting {
				t.Fatalf("got classification=%s setting=%s", strategy.Classification, strategy.Setting)
			}
		})
	}
}

func TestSelectPDFStrategyRejectsUnknownProfile(t *testing.T) {
	if _, err := SelectPDFStrategy(PDFMetadata{}, "unknown"); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}

func TestParsePDFImages(t *testing.T) {
	value := "page num type width height color comp bpc enc interp object ID x-ppi y-ppi size ratio\n1 0 image 1200 800 rgb jpeg 8 jpeg no 7 0 300 300 120K 10%\n"
	var metadata PDFMetadata
	parsePDFImages(value, &metadata)
	if metadata.ImageCount != 1 || metadata.ImageFormats[0] != "jpeg" {
		t.Fatalf("unexpected image metadata: %+v", metadata)
	}
	if metadata.TotalImageBytes == nil || *metadata.TotalImageBytes != 120*1024 {
		t.Fatalf("unexpected image bytes: %+v", metadata.TotalImageBytes)
	}
}

func TestCandidateGenerationAndSelection(t *testing.T) {
	pages := 20
	meta := PDFMetadata{SizeBytes: LargePDFThreshold, PageCount: &pages, ImageCount: 20}
	for _, profile := range []string{"FAST", "MEDIUM", "HIGH"} {
		strategy, err := SelectPDFStrategy(meta, profile)
		if err != nil {
			t.Fatal(err)
		}
		if len(strategy.Candidates) != 3 {
			t.Fatalf("%s candidates=%d, want 3", profile, len(strategy.Candidates))
		}
		selected, ok := SelectPDFCandidate(strategy)
		if !ok {
			t.Fatal("expected selected candidate")
		}
		if profile == "FAST" && selected.Name != "conservative" {
			t.Fatalf("fast selected %s", selected.Name)
		}
		if profile == "HIGH" && selected.Name != "aggressive" {
			t.Fatalf("high selected %s", selected.Name)
		}
	}
}

func TestAlreadyOptimizedHasNoCandidates(t *testing.T) {
	strategy, err := SelectPDFStrategy(PDFMetadata{SizeBytes: SmallPDFThreshold - 1}, "HIGH")
	if err != nil {
		t.Fatal(err)
	}
	if strategy.Classification != AlreadyOptimized || len(strategy.Candidates) != 0 {
		t.Fatalf("unexpected strategy: %+v", strategy)
	}
}

func TestSamplePDFPagesDeterministic(t *testing.T) {
	pages := 100
	got := SamplePDFPages(&pages)
	want := []int{1, 50, 100}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPreferredCandidateRanking(t *testing.T) {
	conservative := PDFCandidate{Name: "conservative"}
	balanced := PDFCandidate{Name: "balanced"}
	aggressive := PDFCandidate{Name: "aggressive"}
	if !preferredCandidate(aggressive, conservative, "HIGH") {
		t.Fatal("high should prefer aggressive on a size tie")
	}
	if !preferredCandidate(conservative, aggressive, "FAST") {
		t.Fatal("fast should prefer conservative on a size tie")
	}
	if !preferredCandidate(balanced, conservative, "MEDIUM") {
		t.Fatal("medium should prefer balanced on a size tie")
	}
}

func TestSSIMQualityPolicy(t *testing.T) {
	tests := []struct {
		score  float64
		status PDFQualityStatus
	}{
		{0.995, QualityExcellent}, {0.98, QualityVeryGood}, {0.95, QualityGood},
		{0.91, QualityAcceptable}, {0.82, QualityPoor},
	}
	for _, test := range tests {
		if got := classifySSIM(test.score); got != test.status {
			t.Fatalf("score %.3f => %s, want %s", test.score, got, test.status)
		}
	}
}

func TestParseSSIM(t *testing.T) {
	score, err := parseSSIM("13643.2 (0.0179)")
	if err != nil || score != 0.9821 {
		t.Fatalf("got score=%v err=%v", score, err)
	}
	if _, err := parseSSIM("not-a-metric"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestPSNRFallbackPolicy(t *testing.T) {
	score, err := parsePSNR("42.5")
	if err != nil || score != 42.5 {
		t.Fatalf("got score=%v err=%v", score, err)
	}
	if classifyPSNR(score) != QualityVeryGood {
		t.Fatalf("unexpected PSNR status: %s", classifyPSNR(score))
	}
}

func TestCandidateScoreRejectsPoorQualityPreference(t *testing.T) {
	good := 0.95
	poor := 0.82
	if candidateScore(PDFQualityResult{Metric: "SSIM", Score: &good}, 20, "HIGH") <=
		candidateScore(PDFQualityResult{Metric: "SSIM", Score: &poor, Status: QualityPoor}, 90, "HIGH") {
		t.Fatal("quality-aware score allowed extreme compression to dominate")
	}
}

func TestPDFBenchmarkSinkPreservesEvaluationOrder(t *testing.T) {
	sink := NewPDFBenchmarkSink()
	sink.Add(PDFCandidateResult{
		CandidateID: "conservative", Profile: "MEDIUM", Classification: ImageHeavy,
		SamplePageCount: 2, SamplePages: []int{1, 10}, QualityStatus: QualityUnassessed,
		Decision: "REJECT", Reason: "visual quality unavailable",
	})
	sink.Add(PDFCandidateResult{
		CandidateID: "balanced", Profile: "MEDIUM", Classification: ImageHeavy,
		SamplePageCount: 2, SamplePages: []int{1, 10}, QualityMetric: "SSIM",
		QualityStatus: QualityGood, StructuralValid: true, SizeGatePassed: true,
		Decision: "ACCEPT",
	})
	results := sink.Results()
	if len(results) != 2 || results[0].CandidateID != "conservative" || results[1].CandidateID != "balanced" {
		t.Fatalf("unexpected benchmark order: %+v", results)
	}
	if results[0].QualityMetric != "" || results[0].QualityStatus != QualityUnassessed {
		t.Fatalf("unavailable quality was not represented consistently: %+v", results[0])
	}
	if !results[1].StructuralValid || !results[1].SizeGatePassed || results[1].Decision != "ACCEPT" {
		t.Fatalf("accepted candidate benchmark incomplete: %+v", results[1])
	}
}

func TestDecidePDFAcceptance(t *testing.T) {
	pages := 3
	score := 0.96
	quality := PDFQualityResult{Metric: "SSIM", Score: &score, Status: QualityGood}
	tests := []struct {
		name, decision, reason string
		output, original       int64
		outPages               *int
		valid                  bool
		quality                PDFQualityResult
	}{
		{"acceptable", AcceptCompressed, "output passed", 80, 100, &pages, true, quality},
		{"same size", RetainOriginal, "not smaller", 100, 100, &pages, true, quality},
		{"larger", RetainOriginal, "not smaller", 120, 100, &pages, true, quality},
		{"invalid", RetainOriginal, "structural", 80, 100, &pages, false, quality},
		{"poor", RetainOriginal, "POOR", 80, 100, &pages, true, PDFQualityResult{Status: QualityPoor}},
		{"unassessed policy", AcceptCompressed, "explicit", 80, 100, &pages, true, PDFQualityResult{Status: QualityUnassessed}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, reason := DecidePDFAcceptance(test.original, test.output, &pages, test.outPages, test.valid, test.quality)
			if decision != test.decision || !strings.Contains(reason, test.reason) {
				t.Fatalf("got decision=%s reason=%q", decision, reason)
			}
		})
	}
	changed := pages + 1
	if decision, _ := DecidePDFAcceptance(100, 80, &pages, &changed, true, quality); decision != RetainOriginal {
		t.Fatal("page count change should retain original")
	}
}

func TestSelectPDFFallbackEligibility(t *testing.T) {
	strategy := PDFCompressionStrategy{Candidates: []PDFCandidate{
		{Name: "conservative"}, {Name: "balanced"}, {Name: "aggressive"},
	}}
	results := []PDFCandidateResult{
		{CandidateID: "conservative", StructuralValid: true, PageCountPreserved: true, SizeGatePassed: true, QualityStatus: QualityGood},
		{CandidateID: "balanced", StructuralValid: false, PageCountPreserved: true, SizeGatePassed: true, QualityStatus: QualityGood},
		{CandidateID: "aggressive", StructuralValid: true, PageCountPreserved: true, SizeGatePassed: true, QualityStatus: QualityGood},
	}
	fallback, ok := selectPDFFallback(strategy, "conservative", results)
	if !ok || fallback.Name != "aggressive" {
		t.Fatalf("expected invalid balanced candidate to be skipped, got %+v, ok=%v", fallback, ok)
	}
	results[1].StructuralValid = true
	results[1].QualityStatus = QualityPoor
	fallback, ok = selectPDFFallback(strategy, "conservative", results)
	if !ok || fallback.Name != "aggressive" {
		t.Fatalf("expected aggressive fallback, got %+v, ok=%v", fallback, ok)
	}
	results[2].SizeGatePassed = false
	if _, ok := selectPDFFallback(strategy, "conservative", results); ok {
		t.Fatal("size-gate-failed candidates should be ineligible")
	}
}
