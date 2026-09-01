package convert

import (
	"archive/zip"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// pdfToDOCX renders each PDF page into a high-resolution PNG and places it on
// its own DOCX page. This preserves images, tables, fonts, and exact layout.
func pdfToDOCX(input string) (string, error) {
	return pdfToDOCXContext(context.Background(), input)
}

func pdfToDOCXContext(ctx context.Context, input string) (string, error) {
	work, err := os.MkdirTemp("", "fileswap-pdf-docx-*")
	if err != nil {
		return "", err
	}

	prefix := filepath.Join(work, "page")
	log, err := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150", input, prefix).CombinedOutput()
	if err != nil {
		os.RemoveAll(work)
		return "", fmt.Errorf("could not render PDF pages: %v\n%s", err, log)
	}
	images, err := filepath.Glob(prefix + "-*.png")
	if err != nil || len(images) == 0 {
		os.RemoveAll(work)
		return "", fmt.Errorf("this PDF did not produce any pages")
	}
	sort.Strings(images)

	out := filepath.Join(work, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".docx")
	if err := writeImageDOCX(out, images); err != nil {
		os.RemoveAll(work)
		return "", err
	}
	return out, nil
}

func writeImageDOCX(path string, images []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	parts := map[string]string{
		"[Content_Types].xml":          contentTypesXML(),
		"_rels/.rels":                  relsXML("word/document.xml"),
		"word/document.xml":            documentImagesXML(images),
		"word/_rels/document.xml.rels": documentRelsXML(len(images)),
		"word/styles.xml":              stylesXML(),
	}
	for name, value := range parts {
		entry, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write([]byte(value)); err != nil {
			return err
		}
	}
	for i, imagePath := range images {
		entry, err := zw.Create(fmt.Sprintf("word/media/image%d.png", i+1))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(imagePath)
		if err != nil {
			return err
		}
		if _, err := entry.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func contentTypesXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`
}

func relsXML(target string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="` + target + `"/></Relationships>`
}

func documentRelsXML(count int) string {
	var rels strings.Builder
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= count; i++ {
		rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image%d.png"/>`, i, i))
	}
	rels.WriteString(`</Relationships>`)
	return rels.String()
}

func stylesXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults></w:styles>`
}

func documentImagesXML(images []string) string {
	var body strings.Builder
	for i, path := range images {
		width, height := imageSize(path)
		body.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:jc w:val="center"/><w:spacing w:before="0" w:after="0"/></w:pPr><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="PDF page %d"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="page-%d.png"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="rId%d" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`, width, height, i+1, i+1, i+1, i+1, i+1, width, height))
		if i < len(images)-1 {
			body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		}
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="0" w:right="0" w:bottom="0" w:left="0"/></w:sectPr></w:body></w:document>`
}

func imageSize(path string) (int64, int64) {
	f, err := os.Open(path)
	if err != nil {
		return 6480000, 9360000
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 6480000, 9360000
	}
	const maxWidth int64 = 6480000
	const maxHeight int64 = 9360000
	width := int64(config.Width) * 914400 / 150
	height := int64(config.Height) * 914400 / 150
	scale := 1.0
	if width > maxWidth {
		scale = float64(maxWidth) / float64(width)
	}
	if float64(height)*scale > float64(maxHeight) {
		scale = float64(maxHeight) / float64(height)
	}
	return int64(float64(width) * scale), int64(float64(height) * scale)
}
