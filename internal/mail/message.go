package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

const MaxMessage = 4 << 20

var ErrDenied = errors.New("message denied by content policy")
var link = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"']+`)

type Message struct {
	Subject string
	From    []string
	To      []string
	Cc      []string
	Bcc     []string
	Bytes   []byte
}

func addresses(h mail.Header, name string) ([]string, error) {
	if h.Get(name) == "" {
		return nil, nil
	}
	list, err := h.AddressList(name)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(list))
	for _, addr := range list {
		result = append(result, addr.Address)
	}
	return result, nil
}

// Evaluate constructs the only bytes that may be exposed. Unsupported MIME is
// denied rather than accidentally forwarding an uninspected alternative part.
func Evaluate(raw []byte, rule config.ContentRule) (*Message, error) {
	if len(raw) > MaxMessage {
		return nil, ErrDenied
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrDenied
	}
	var result Message
	if result.Subject, err = (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject")); err != nil {
		return nil, ErrDenied
	}
	for _, item := range []struct {
		name string
		dest *[]string
	}{{"From", &result.From}, {"To", &result.To}, {"Cc", &result.Cc}, {"Bcc", &result.Bcc}} {
		*item.dest, err = addresses(parsed.Header, item.name)
		if err != nil {
			return nil, ErrDenied
		}
	}
	contentType := parsed.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "text/plain") || (params["charset"] != "" && !strings.EqualFold(params["charset"], "us-ascii") && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil, ErrDenied
	}
	encoding := strings.ToLower(parsed.Header.Get("Content-Transfer-Encoding"))
	if encoding != "" && encoding != "7bit" && encoding != "8bit" {
		return nil, ErrDenied
	}
	if parsed.Header.Get("Content-Disposition") != "" || parsed.Header.Get("Content-Location") != "" || parsed.Header.Get("Content-ID") != "" {
		return nil, ErrDenied
	}
	if rule.DenyAttachments || len(rule.DeniedExtensions) > 0 || rule.MaxAttachmentSize > 0 {
		// All attachments are denied by the supported plain-text MIME subset.
	}
	body, err := io.ReadAll(io.LimitReader(parsed.Body, MaxMessage+1))
	if err != nil || len(body) > MaxMessage {
		return nil, ErrDenied
	}
	for _, pattern := range rule.DeniedSubjects {
		matched, err := matches(pattern, result.Subject)
		if err != nil || matched {
			return nil, ErrDenied
		}
	}
	for _, pattern := range rule.DeniedBodies {
		matched, err := matches(pattern, string(body))
		if err != nil || matched {
			return nil, ErrDenied
		}
	}
	if len(rule.Scanner) > 0 {
		if err := scan(rule.Scanner, append([]byte(result.Subject+"\n"), body...)); err != nil {
			return nil, ErrDenied
		}
	}
	if rule.RemoveLinks {
		result.Subject = link.ReplaceAllString(result.Subject, "[link removed]")
		body = []byte(link.ReplaceAllString(string(body), "[link removed]"))
	}
	// Rebuild with an allowlist: unchecked Received, List-Unsubscribe, and
	// Content-* headers may contain links or secrets.
	var out bytes.Buffer
	for _, header := range []struct {
		name   string
		values []string
	}{{"From", result.From}, {"To", result.To}, {"Cc", result.Cc}} {
		if len(header.values) > 0 {
			fmt.Fprintf(&out, "%s: %s\r\n", header.name, strings.Join(header.values, ", "))
		}
	}
	fmt.Fprintf(&out, "Subject: %s\r\n", result.Subject)
	out.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	out.Write(body)
	result.Bytes = out.Bytes()
	return &result, nil
}

func matches(pattern, value string) (bool, error) {
	if strings.HasPrefix(pattern, "re:") {
		re, err := regexp.Compile(pattern[3:])
		if err != nil {
			return false, err
		}
		return re.MatchString(value), nil
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return strings.Contains(strings.ToLower(value), strings.ToLower(pattern)), nil
	}
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(part))
	}
	re := regexp.MustCompile("(?is)" + b.String())
	return re.MatchString(value), nil
}

// A scanner reports JSON {"status":"clean"} or {"status":"finding"} and
// must exit successfully. Anything else is an inspection failure.
func scan(command []string, input []byte) error {
	if len(command) == 0 || command[0] == "" {
		return ErrDenied
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = &limitedWriter{out: &output, max: 4096}
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return ErrDenied
	}
	var verdict struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output.Bytes(), &verdict); err != nil || verdict.Status != "clean" {
		return ErrDenied
	}
	return nil
}

type limitedWriter struct {
	out *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.max-w.out.Len() {
		return 0, ErrDenied
	}
	return w.out.Write(p)
}
