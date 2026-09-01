package files

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

const MaxUploadBytes = 500 << 20 // 500 MB per file

var allowedExt = map[string]struct{}{
	".pdf":  {},
	".doc":  {},
	".docx": {},
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".webp": {},
	".gif":  {},
	".mp4":  {},
	".mov":  {},
	".mkv":  {},
	".webm": {},
	".txt":  {},
	".zip":  {},
}

func Ext(name string) string {
	return strings.ToLower(filepath.Ext(name))
}

func IsAllowed(name string) bool {
	_, ok := allowedExt[Ext(name)]
	return ok
}

func SaveUpload(fh *multipart.FileHeader, dir string) (string, error) {
	if fh.Size > MaxUploadBytes {
		return "", fmt.Errorf("file too large: %s", fh.Filename)
	}
	if !IsAllowed(fh.Filename) {
		return "", fmt.Errorf("unsupported file type: %s", fh.Filename)
	}

	src, err := fh.Open()
	if err != nil {
		return nilErr("open upload", err)
	}
	defer src.Close()

	safe := filepath.Base(fh.Filename)
	dstPath := filepath.Join(dir, safe)
	dst, err := os.Create(dstPath)
	if err != nil {
		return nilErr("create dest", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return nilErr("copy upload", err)
	}
	return dstPath, nil
}

func SaveAll(uploads []*multipart.FileHeader, dir string) ([]string, error) {
	out := make([]string, 0, len(uploads))
	for i, fh := range uploads {
		p, err := SaveUpload(fh, dir)
		if err != nil {
			return nil, err
		}
		unique := filepath.Join(dir, fmt.Sprintf("%02d-%s", i+1, filepath.Base(p)))
		if unique != p {
			if err := os.Rename(p, unique); err != nil {
				return nil, err
			}
			p = unique
		}
		out = append(out, p)
	}
	return out, nil
}

func nilErr(op string, err error) (string, error) {
	return "", fmt.Errorf("%s: %w", op, err)
}

func Stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
