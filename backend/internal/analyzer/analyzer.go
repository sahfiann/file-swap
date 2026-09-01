package analyzer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Report struct {
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Dimensions string `json:"dimensions,omitempty"`
	Pages      int    `json:"pages,omitempty"`
	Text       string `json:"text,omitempty"`
	Images     int    `json:"images,omitempty"`
	Metadata   string `json:"metadata"`
	Encrypted  string `json:"encrypted,omitempty"`
	Complexity string `json:"complexity"`
}

func Analyze(paths []string) (Report, error) {
	if len(paths) == 0 {
		return Report{}, fmt.Errorf("analyzer requires at least one file")
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		return Report{}, fmt.Errorf("analyze file: %w", err)
	}
	report := Report{Size: info.Size(), Type: fileType(paths[0]), Metadata: "NO", Complexity: "LOW"}
	if report.Type == "PDF" {
		if err := analyzePDF(paths[0], &report); err != nil {
			return Report{}, err
		}
	} else if isImage(paths[0]) {
		if err := analyzeImage(paths[0], &report); err != nil {
			return Report{}, err
		}
	}
	if report.Size > 100*1024*1024 || report.Pages > 100 || report.Images > 100 {
		report.Complexity = "HIGH"
	} else if report.Size > 10*1024*1024 || report.Pages > 20 {
		report.Complexity = "MEDIUM"
	}
	return report, nil
}

func analyzeImage(path string, report *Report) error {
	output, err := exec.Command("identify", "-format", "%wx%h %[EXIF:*]", path).Output()
	if err != nil {
		return fmt.Errorf("analyze image: %w", err)
	}
	parts := strings.Fields(string(output))
	if len(parts) > 0 {
		report.Dimensions = parts[0]
	}
	if len(parts) > 1 {
		report.Metadata = "YES"
	}
	return nil
}

func analyzePDF(path string, report *Report) error {
	output, err := exec.Command("pdfinfo", path).Output()
	if err != nil {
		return fmt.Errorf("analyze PDF: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Pages":
			report.Pages, _ = strconv.Atoi(value)
		case "Encrypted":
			report.Encrypted = strings.ToUpper(value)
		case "Metadata Stream":
			if value == "yes" {
				report.Metadata = "YES"
			}
		}
	}
	text, err := exec.Command("pdftotext", path, "-").Output()
	if err != nil {
		return fmt.Errorf("analyze PDF text: %w", err)
	}
	if strings.TrimSpace(string(text)) != "" {
		report.Text = "YES"
	} else {
		report.Text = "NO"
	}
	images, err := exec.Command("pdfimages", "-list", path).Output()
	if err != nil {
		return fmt.Errorf("analyze PDF images: %w", err)
	}
	for _, line := range strings.Split(string(images), "\n") {
		if strings.TrimSpace(line) != "" && strings.HasPrefix(strings.TrimSpace(line), "page") == false {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if _, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
					report.Images++
				}
			}
		}
	}
	return nil
}

func fileType(path string) string {
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), "."))
	if ext == "JPG" {
		return "JPEG"
	}
	if ext == "" {
		return "UNKNOWN"
	}
	return ext
}

func isImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg":
		return true
	default:
		return false
	}
}
