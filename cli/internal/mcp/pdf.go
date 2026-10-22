package mcp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

const (
	maxPDFPages      = 8
	maxPDFTextBytes  = 64 << 10
	maxPDFImageBytes = 5 << 20
)

type pdfPage struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// renderPDFPage renders one requested page through pdftoppm when the operator
// has installed it. The fixed argument list, short deadline, isolated 0700
// directory, and output cap keep an untrusted PDF from selecting commands or
// leaving a readable file behind.
func renderPDFPage(ctx context.Context, data []byte, page uint32) ([]byte, error) {
	if page == 0 {
		return nil, errors.New("attachment unavailable")
	}
	pdftoppm, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, errors.New("PDF page rendering is unavailable")
	}
	dir, err := os.MkdirTemp("", "sirenorelay-pdf-")
	if err != nil {
		return nil, errors.New("attachment unavailable")
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "attachment.pdf")
	if err := os.WriteFile(input, data, 0600); err != nil {
		return nil, errors.New("attachment unavailable")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, pdftoppm, "-f", strconv.FormatUint(uint64(page), 10), "-l", strconv.FormatUint(uint64(page), 10), "-png", "-singlefile", "-scale-to", "2048", input, filepath.Join(dir, "page"))
	if _, err := command.CombinedOutput(); err != nil || commandCtx.Err() != nil {
		return nil, errors.New("PDF page rendering is unavailable")
	}
	image, err := os.ReadFile(filepath.Join(dir, "page.png"))
	if err != nil || len(image) == 0 || len(image) > maxPDFImageBytes {
		return nil, errors.New("PDF page rendering is unavailable")
	}
	return image, nil
}

func extractPDFPages(data []byte, requestedPage uint32) ([]pdfPage, bool, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || reader.NumPage() < 1 {
		return nil, false, errors.New("attachment unavailable")
	}
	pages := reader.NumPage()
	if requestedPage > 0 {
		if requestedPage > uint32(pages) {
			return nil, false, errors.New("attachment unavailable")
		}
		pages = int(requestedPage)
	}
	start := 1
	if requestedPage > 0 {
		start = int(requestedPage)
	}
	end := min(pages, start+maxPDFPages-1)
	result := make([]pdfPage, 0, end-start+1)
	used := 0
	for number := start; number <= end; number++ {
		text, err := reader.Page(number).GetPlainText(nil)
		if err != nil {
			return nil, false, errors.New("attachment unavailable")
		}
		text = strings.TrimSpace(text)
		remaining := maxPDFTextBytes - used
		if remaining <= 0 {
			return result, true, nil
		}
		if len(text) > remaining {
			text = text[:remaining]
			result = append(result, pdfPage{Number: number, Text: text})
			return result, true, nil
		}
		used += len(text)
		result = append(result, pdfPage{Number: number, Text: text})
	}
	return result, end < reader.NumPage(), nil
}
