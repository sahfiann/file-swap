package merge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/sahfiann/file-swap/internal/convert"
	"github.com/sahfiann/file-swap/internal/files"
)

func Run(inputPaths []string) (string, error) {
	return RunContext(context.Background(), inputPaths)
}

func RunContext(ctx context.Context, inputPaths []string) (string, error) {
	if len(inputPaths) < 2 {
		return "", fmt.Errorf("need at least two files to merge")
	}

	work, err := os.MkdirTemp("", "fileswap-merge-*")
	if err != nil {
		return "", err
	}

	pdfs := make([]string, 0, len(inputPaths))
	for i, in := range inputPaths {
		if files.Ext(in) == ".pdf" {
			pdfs = append(pdfs, in)
			continue
		}
		converted, err := convert.RunContext(ctx, in, "pdf")
		if err != nil {
			os.RemoveAll(work)
			return "", fmt.Errorf("prepare %s: %w", filepath.Base(in), err)
		}
		dest := filepath.Join(work, fmt.Sprintf("%02d-%s.pdf", i+1, files.Stem(in)))
		if err := os.Rename(converted, dest); err != nil {
			data, rerr := os.ReadFile(converted)
			if rerr != nil {
				os.RemoveAll(work)
				return "", err
			}
			if werr := os.WriteFile(dest, data, 0o644); werr != nil {
				os.RemoveAll(work)
				return "", werr
			}
			os.Remove(converted)
			os.Remove(filepath.Dir(converted))
		} else {
			os.Remove(filepath.Dir(converted))
		}
		pdfs = append(pdfs, dest)
	}

	out := filepath.Join(work, "merged.pdf")
	if err := api.MergeCreateFile(pdfs, out, false, nil); err != nil {
		os.RemoveAll(work)
		return "", fmt.Errorf("merge pdf: %w", err)
	}
	return out, nil
}

func Label(paths []string) string {
	if len(paths) == 0 {
		return "merged.pdf"
	}
	base := files.Stem(paths[0])
	if len(base) >= 4 && base[0] >= '0' && base[0] <= '9' && base[1] >= '0' && base[1] <= '9' && base[2] == '-' {
		base = base[3:]
	}
	return strings.TrimSpace(base) + "-merged.pdf"
}
