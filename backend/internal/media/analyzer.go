package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

type VideoMetadata struct {
	SizeBytes       int64    `json:"sizeBytes"`
	DurationSeconds *float64 `json:"durationSeconds,omitempty"`
	Width           *int     `json:"width,omitempty"`
	Height          *int     `json:"height,omitempty"`
	VideoCodec      string   `json:"videoCodec,omitempty"`
	VideoBitrate    *int64   `json:"videoBitrate,omitempty"`
	FPS             *float64 `json:"fps,omitempty"`
	FrameCount      *int64   `json:"frameCount,omitempty"`
	PixelFormat     string   `json:"pixelFormat,omitempty"`
	AudioCodec      string   `json:"audioCodec,omitempty"`
	AudioBitrate    *int64   `json:"audioBitrate,omitempty"`
	AudioSampleRate *int     `json:"audioSampleRate,omitempty"`
	AudioChannels   *int     `json:"audioChannels,omitempty"`
}

type ffprobeResult struct {
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Bitrate     string `json:"bit_rate"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		FrameRate   string `json:"r_frame_rate"`
		Frames      string `json:"nb_frames"`
		PixelFormat string `json:"pix_fmt"`
		SampleRate  string `json:"sample_rate"`
		Channels    int    `json:"channels"`
		Duration    string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		Bitrate  string `json:"bit_rate"`
	} `json:"format"`
}

func AnalyzeVideo(ctx context.Context, path string) (VideoMetadata, error) {
	info, err := os.Stat(path)
	if err != nil {
		return VideoMetadata{}, fmt.Errorf("stat video: %w", err)
	}
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", path).Output()
	if err != nil {
		return VideoMetadata{}, fmt.Errorf("analyze video with ffprobe: %w", err)
	}
	var probe ffprobeResult
	if err := json.Unmarshal(output, &probe); err != nil {
		return VideoMetadata{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	metadata := VideoMetadata{SizeBytes: info.Size()}
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			metadata.VideoCodec = stream.CodecName
			metadata.PixelFormat = stream.PixelFormat
			metadata.Width = intPtrIfPositive(stream.Width)
			metadata.Height = intPtrIfPositive(stream.Height)
			metadata.VideoBitrate = int64PtrIfPositive(parseInt64(stream.Bitrate))
			metadata.FPS = floatPtrIfPositive(parseRate(stream.FrameRate))
			metadata.FrameCount = int64PtrIfPositive(parseInt64(stream.Frames))
			if metadata.DurationSeconds == nil {
				metadata.DurationSeconds = floatPtrIfPositive(parseFloat(stream.Duration))
			}
		case "audio":
			metadata.AudioCodec = stream.CodecName
			metadata.AudioBitrate = int64PtrIfPositive(parseInt64(stream.Bitrate))
			metadata.AudioSampleRate = intPtrIfPositive(parseInt(stream.SampleRate))
			metadata.AudioChannels = intPtrIfPositive(stream.Channels)
		}
	}
	if metadata.DurationSeconds == nil {
		metadata.DurationSeconds = floatPtrIfPositive(parseFloat(probe.Format.Duration))
	}
	if metadata.VideoBitrate == nil {
		metadata.VideoBitrate = int64PtrIfPositive(parseInt64(probe.Format.Bitrate))
	}
	return metadata, nil
}

func parseInt(value string) int {
	result, _ := strconv.Atoi(value)
	return result
}

func parseInt64(value string) int64 {
	result, _ := strconv.ParseInt(value, 10, 64)
	return result
}

func parseFloat(value string) float64 {
	result, _ := strconv.ParseFloat(value, 64)
	return result
}

func parseRate(value string) float64 {
	var numerator, denominator float64
	if _, err := fmt.Sscanf(value, "%f/%f", &numerator, &denominator); err != nil || denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func intPtrIfPositive(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func int64PtrIfPositive(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func floatPtrIfPositive(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	return &value
}
