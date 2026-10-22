package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestExtractPDFPages(t *testing.T) {
	pages, truncated, err := extractPDFPages(testPDF("Hello from SirenoRELAY"), 0)
	if err != nil || truncated || len(pages) != 1 || !strings.Contains(pages[0].Text, "Hello from SirenoRELAY") {
		t.Fatalf("pages=%#v truncated=%v err=%v", pages, truncated, err)
	}
	if _, _, err := extractPDFPages([]byte("not a PDF"), 0); err == nil {
		t.Fatal("invalid PDF was accepted")
	}
}

func TestRenderPDFPage(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm is not installed")
	}
	image, err := renderPDFPage(context.Background(), testPDF("Rendered page"), 1)
	if err != nil || !strings.HasPrefix(string(image), "\x89PNG\r\n\x1a\n") {
		t.Fatalf("image bytes=%d err=%v", len(image), err)
	}
}

func testPDF(text string) []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\nBT /F1 12 Tf 72 712 Td (%s) Tj ET\nendstream", len("BT /F1 12 Tf 72 712 Td ("+text+") Tj ET\n"), text),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var document strings.Builder
	document.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for number, object := range objects {
		offsets[number+1] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", number+1, object)
	}
	xref := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&document, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(document.String())
}
