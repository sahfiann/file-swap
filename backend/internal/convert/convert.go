package convert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sahfiann/file-swap/internal/files"
)

func Run(inputPath, target string) (string, error) {
	return RunContext(context.Background(), inputPath, target)
}

func RunContext(ctx context.Context, inputPath, target string) (string, error) {
	target = strings.ToLower(strings.TrimPrefix(target, "."))
	ext := files.Ext(inputPath)

	switch target {
	case "pdf":
		if ext == ".pdf" {
			return "", fmt.Errorf("file is already a PDF")
		}
		if ext != ".doc" && ext != ".docx" && ext != ".txt" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
			return "", fmt.Errorf("cannot convert %s to PDF", ext)
		}
	case "docx":
		if ext == ".docx" {
			return "", fmt.Errorf("file is already a DOCX")
		}
		if ext != ".pdf" && ext != ".doc" && ext != ".txt" {
			return "", fmt.Errorf("cannot convert %s to DOCX", ext)
		}
	default:
		return "", fmt.Errorf("unsupported target: %s", target)
	}

	// LibreOffice imports PDFs as drawing objects, which turns normal text into
	// hundreds of positioned text boxes in the resulting DOCX. Produce a clean,
	// editable Word document from the PDF text layer instead.
	if ext == ".pdf" && target == "docx" {
		return pdfToDOCXContext(ctx, inputPath)
	}

	outDir, err := os.MkdirTemp("", "fileswap-out-*")
	if err != nil {
		return "", err
	}

	profile, err := os.MkdirTemp("", "fileswap-lo-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(profile)

	args := []string{
		"--headless",
		"--norestore",
		"--nolockcheck",
		"-env:UserInstallation=file://" + profile,
	}
	// Use Writer's native PDF export filter for DOC/DOCX input. This keeps the
	// document's original page layout and image data instead of routing it
	// through a lower-quality generic export path.
	convertTo := target
	if target == "pdf" {
		convertTo = "pdf:writer_pdf_Export"
	}
	args = append(args, "--convert-to", convertTo, "--outdir", outDir, inputPath)

	cmd := exec.CommandContext(ctx, "soffice", args...)
	cmd.Env = append(os.Environ(), "HOME="+profile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(outDir)
		return "", fmt.Errorf("libreoffice convert failed: %v\n%s", err, out)
	}

	matches, err := filepath.Glob(filepath.Join(outDir, "*."+target))
	if err != nil || len(matches) == 0 {
		os.RemoveAll(outDir)
		return "", fmt.Errorf("conversion produced no %s file\n%s", target, out)
	}
	return matches[0], nil
}
