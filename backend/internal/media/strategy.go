package media

import (
	"fmt"
	"strings"
)

type CompressionProfile string

const (
	ProfileFast   CompressionProfile = "FAST"
	ProfileMedium CompressionProfile = "MEDIUM"
	ProfileHigh   CompressionProfile = "HIGH"
)

type BitrateClass string
type ResolutionClass string

const (
	SourceLowBitrate    BitrateClass    = "SOURCE_LOW_BITRATE"
	SourceNormalBitrate BitrateClass    = "SOURCE_NORMAL_BITRATE"
	SourceHighBitrate   BitrateClass    = "SOURCE_HIGH_BITRATE"
	LowResolution       ResolutionClass = "LOW_RESOLUTION"
	MediumResolution    ResolutionClass = "MEDIUM_RESOLUTION"
	HighResolution      ResolutionClass = "HIGH_RESOLUTION"
)

type CompressionStrategy struct {
	Profile         CompressionProfile `json:"profile"`
	TargetCodec     string             `json:"targetCodec"`
	TargetQuality   string             `json:"targetQuality"`
	TargetBitrate   *int64             `json:"targetBitrate,omitempty"`
	TargetWidth     *int               `json:"targetWidth,omitempty"`
	TargetHeight    *int               `json:"targetHeight,omitempty"`
	AudioBitrate    *int64             `json:"audioBitrate,omitempty"`
	BitrateClass    BitrateClass       `json:"bitrateClass"`
	ResolutionClass ResolutionClass    `json:"resolutionClass"`
	Reason          string             `json:"reason"`
}

func SelectStrategy(metadata VideoMetadata, profile string) (CompressionStrategy, error) {
	selected := CompressionProfile(strings.ToUpper(strings.TrimSpace(profile)))
	if selected == "MEDIUM" {
	} else if selected == "FAST" {
	} else if selected == "HIGH" {
	} else {
		return CompressionStrategy{}, fmt.Errorf("unsupported compression profile: %s", profile)
	}
	bitrateClass := classifyBitrate(metadata)
	resolutionClass := classifyResolution(metadata)
	targetQuality := "conservative"
	reason := "Source bitrate is already low, using conservative compression."
	targetCodec := "h264"
	if isEfficientCodec(metadata.VideoCodec) {
		targetCodec = strings.ToLower(metadata.VideoCodec)
		targetQuality = "preserve"
		reason = "Source codec is already efficient, preserving the codec and avoiding unnecessary re-encoding."
	}
	switch bitrateClass {
	case SourceNormalBitrate:
		if targetQuality != "preserve" {
			targetQuality = "balanced"
			reason = "Source bitrate is normal, balancing size and visual quality."
		}
	case SourceHighBitrate:
		if targetQuality != "preserve" {
			targetQuality = "efficient"
			reason = "Source bitrate is high, using more efficient compression while preserving resolution."
		}
	}
	if selected == ProfileFast {
		targetQuality = "speed-safe"
		reason += " FAST prioritizes processing speed without reducing resolution."
	} else if selected == ProfileHigh {
		if targetQuality != "preserve" {
			targetQuality += "-quality"
		}
		reason += " HIGH prioritizes compression efficiency and quality."
	}
	return CompressionStrategy{
		Profile: selected, TargetCodec: targetCodec, TargetQuality: targetQuality,
		TargetWidth: metadata.Width, TargetHeight: metadata.Height,
		AudioBitrate: metadata.AudioBitrate, BitrateClass: bitrateClass,
		ResolutionClass: resolutionClass, Reason: reason,
	}, nil
}

func isEfficientCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "hevc", "h265", "av1", "vp9":
		return true
	default:
		return false
	}
}

func classifyBitrate(metadata VideoMetadata) BitrateClass {
	if metadata.VideoBitrate == nil || metadata.DurationSeconds == nil {
		return SourceNormalBitrate
	}
	switch {
	case *metadata.VideoBitrate < 1_000_000:
		return SourceLowBitrate
	case *metadata.VideoBitrate > 8_000_000:
		return SourceHighBitrate
	default:
		return SourceNormalBitrate
	}
}

func classifyResolution(metadata VideoMetadata) ResolutionClass {
	if metadata.Width == nil || metadata.Height == nil {
		return MediumResolution
	}
	pixels := *metadata.Width * *metadata.Height
	switch {
	case pixels <= 854*480:
		return LowResolution
	case pixels <= 1920*1080:
		return MediumResolution
	default:
		return HighResolution
	}
}
