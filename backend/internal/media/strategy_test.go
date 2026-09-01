package media

import "testing"

func TestSelectStrategyUsesSourceMetadataAndProfile(t *testing.T) {
	low := int64(500_000)
	normal := int64(4_000_000)
	high := int64(12_000_000)
	duration := 10.0
	lowWidth, lowHeight := 854, 480
	highWidth, highHeight := 1920, 1080
	cases := []struct {
		name       string
		metadata   VideoMetadata
		profile    string
		bitrate    BitrateClass
		resolution ResolutionClass
		quality    string
	}{
		{"low bitrate 480p fast", VideoMetadata{VideoBitrate: &low, DurationSeconds: &duration, Width: &lowWidth, Height: &lowHeight}, "FAST", SourceLowBitrate, LowResolution, "speed-safe"},
		{"normal bitrate 1080p medium", VideoMetadata{VideoCodec: "h264", VideoBitrate: &normal, DurationSeconds: &duration, Width: &highWidth, Height: &highHeight}, "MEDIUM", SourceNormalBitrate, MediumResolution, "balanced"},
		{"high bitrate 1080p high", VideoMetadata{VideoBitrate: &high, DurationSeconds: &duration, Width: &highWidth, Height: &highHeight}, "HIGH", SourceHighBitrate, MediumResolution, "efficient-quality"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strategy, err := SelectStrategy(tc.metadata, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			if strategy.BitrateClass != tc.bitrate || strategy.ResolutionClass != tc.resolution || strategy.TargetQuality != tc.quality {
				t.Fatalf("unexpected strategy: %+v", strategy)
			}
			if strategy.TargetWidth == nil || *strategy.TargetWidth != *tc.metadata.Width || strategy.TargetHeight == nil || *strategy.TargetHeight != *tc.metadata.Height {
				t.Fatalf("strategy changed source resolution: %+v", strategy)
			}
		})
	}
}

func TestSelectStrategyPreservesEfficientCodec(t *testing.T) {
	bitrate, duration, width, height := int64(4_000_000), 10.0, 1920, 1080
	strategy, err := SelectStrategy(VideoMetadata{
		VideoCodec: "hevc", VideoBitrate: &bitrate, DurationSeconds: &duration,
		Width: &width, Height: &height,
	}, "HIGH")
	if err != nil {
		t.Fatal(err)
	}
	if strategy.TargetCodec != "hevc" || strategy.TargetQuality != "preserve" {
		t.Fatalf("expected efficient codec preservation: %+v", strategy)
	}
}

func TestSelectStrategyRejectsUnknownProfile(t *testing.T) {
	if _, err := SelectStrategy(VideoMetadata{}, "turbo"); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}
