package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

const MaxMessage = 16 << 20
const MaxAttachment = 10 << 20
const maxParts = 64

var ErrDenied = errors.New("message denied by content policy")
var link = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"']+`)
var htmlTag = regexp.MustCompile(`(?s)<[^>]*>`)

const removedAttachmentNotice = "[One or more attachments were removed by Sireno security policy.]"

type Attachment struct {
	ID       uint32
	Name     string
	MIMEType string
	Bytes    []byte
}

type Message struct {
	Subject            string
	From               []string
	To                 []string
	Cc                 []string
	Bcc                []string
	Date               *time.Time
	Body               string
	Bytes              []byte
	Attachments        []Attachment
	RemovedAttachments bool
}

func AddSignature(message *Message, signature string) {
	if signature == "" {
		return
	}
	if message.Body != "" {
		message.Body += "\n\n"
	}
	message.Body += signature
	message.Bytes = rebuild(message, message.Body)
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

// Evaluate builds the only message representation exposed or submitted. MIME
// parsing is recursive and bounded; an unreadable part denies the whole mail.
func Evaluate(raw []byte, rule config.ContentRule) (*Message, error) {
	if len(raw) > MaxMessage {
		return nil, ErrDenied
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrDenied
	}
	result := &Message{}
	if result.Subject, err = (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject")); err != nil {
		return nil, ErrDenied
	}
	for _, item := range []struct {
		name string
		dest *[]string
	}{{"From", &result.From}, {"To", &result.To}, {"Cc", &result.Cc}, {"Bcc", &result.Bcc}} {
		if *item.dest, err = addresses(parsed.Header, item.name); err != nil {
			return nil, ErrDenied
		}
	}
	if value := parsed.Header.Get("Date"); value != "" {
		if date, dateErr := mail.ParseDate(value); dateErr == nil {
			date = date.UTC()
			result.Date = &date
		}
	}
	var plain, html string
	parts := 0
	if err := inspect(parsed.Header, parsed.Body, rule, result, &plain, &html, &parts, 0); err != nil {
		return nil, ErrDenied
	}
	body := plain
	if body == "" {
		body = htmlToText(html)
	}
	if result.RemovedAttachments && rule.Attachments.Mode != "silent-remove" {
		if body != "" {
			body += "\n\n"
		}
		body += removedAttachmentNotice
	}
	if body == "" && len(result.Attachments) == 0 {
		return nil, ErrDenied
	}
	for _, pattern := range rule.DeniedSubjects {
		if matched, e := matches(pattern, result.Subject); e != nil || matched {
			return nil, ErrDenied
		}
	}
	for _, pattern := range rule.DeniedBodies {
		if matched, e := matches(pattern, body); e != nil || matched {
			return nil, ErrDenied
		}
	}
	if len(rule.Scanner) > 0 && scan(rule.Scanner, []byte(result.Subject+"\n"+body)) != nil {
		return nil, ErrDenied
	}
	if rule.RemoveLinks {
		result.Subject = link.ReplaceAllString(result.Subject, "[link removed]")
		body = link.ReplaceAllString(body, "[link removed]")
	}
	result.Body = body
	result.Bytes = rebuild(result, body)
	return result, nil
}

func inspect(header mail.Header, source io.Reader, rule config.ContentRule, result *Message, plain, html *string, count *int, depth int) error {
	if depth > 8 {
		return ErrDenied
	}
	*count++
	if *count > maxParts {
		return ErrDenied
	}
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=us-ascii"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ErrDenied
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") {
		if encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))); encoding != "" && encoding != "7bit" && encoding != "8bit" {
			return ErrDenied
		}
		boundary := params["boundary"]
		if boundary == "" {
			return ErrDenied
		}
		reader := multipart.NewReader(source, boundary)
		for {
			part, e := reader.NextPart()
			if errors.Is(e, io.EOF) {
				return nil
			}
			if e != nil {
				return ErrDenied
			}
			if e := inspect(mail.Header(part.Header), part, rule, result, plain, html, count, depth+1); e != nil {
				return e
			}
		}
	}
	data, err := decode(source, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return ErrDenied
	}
	disposition, dispParams := "", map[string]string{}
	if value := header.Get("Content-Disposition"); value != "" {
		var err error
		disposition, dispParams, err = mime.ParseMediaType(value)
		if err != nil {
			return ErrDenied
		}
	}
	isAttachment := strings.EqualFold(disposition, "attachment") || dispParams["filename"] != "" || mediaType == "application/pdf" || strings.HasPrefix(mediaType, "image/") || header.Get("Content-ID") != ""
	if isAttachment {
		if len(data) == 0 || len(data) > MaxAttachment || !attachmentAllowed(mediaType, data, rule.Attachments) {
			if rule.Attachments.Mode == "reject" {
				return ErrDenied
			}
			result.RemovedAttachments = true
			return nil
		}
		name := dispParams["filename"]
		if name == "" {
			name = params["name"]
		}
		result.Attachments = append(result.Attachments, Attachment{ID: uint32(len(result.Attachments) + 1), Name: attachmentName(name), MIMEType: mediaType, Bytes: data})
		return nil
	}
	charset := strings.ToLower(params["charset"])
	if charset != "" && charset != "utf-8" && charset != "us-ascii" {
		return ErrDenied
	}
	switch mediaType {
	case "text/plain":
		if *plain == "" {
			*plain = string(data)
		}
	case "text/html":
		if *html == "" {
			*html = string(data)
		}
	default:
		return ErrDenied
	}
	return nil
}

func attachmentName(value string) string {
	value = filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > 255 {
		value = value[:255]
	}
	if value == "" {
		return "attachment"
	}
	return value
}

func decode(source io.Reader, encoding string) ([]byte, error) {
	var reader io.Reader = source
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit":
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, source)
	case "quoted-printable":
		reader = quotedprintable.NewReader(source)
	default:
		return nil, ErrDenied
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxMessage+1))
	if err != nil || len(data) > MaxMessage {
		return nil, ErrDenied
	}
	return data, nil
}

func attachmentAllowed(declared string, data []byte, rules config.AttachmentRules) bool {
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	if !strings.EqualFold(declared, detected) {
		return false
	}
	for _, pattern := range rules.Deny {
		if mimeMatches(pattern, declared) {
			return false
		}
	}
	for _, pattern := range rules.Allow {
		if mimeMatches(pattern, declared) {
			return true
		}
	}
	return false
}

func mimeMatches(pattern, value string) bool {
	if strings.HasPrefix(pattern, "re:") {
		re, err := regexp.Compile(pattern[3:])
		return err == nil && re.MatchString(value)
	}
	quoted := regexp.QuoteMeta(strings.ToLower(pattern))
	return regexp.MustCompile("^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$").MatchString(strings.ToLower(value))
}

func htmlToText(value string) string {
	value = htmlTag.ReplaceAllString(value, " ")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value))
}

func rebuild(message *Message, body string) []byte {
	var out bytes.Buffer
	for _, header := range []struct {
		name   string
		values []string
	}{{"From", message.From}, {"To", message.To}, {"Cc", message.Cc}} {
		if len(header.values) > 0 {
			fmt.Fprintf(&out, "%s: %s\r\n", header.name, strings.Join(header.values, ", "))
		}
	}
	fmt.Fprintf(&out, "Subject: %s\r\n", message.Subject)
	if len(message.Attachments) > 0 {
		var bodyOut bytes.Buffer
		writer := multipart.NewWriter(&bodyOut)
		fmt.Fprintf(&out, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", writer.Boundary())
		part, _ := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}})
		_, _ = io.WriteString(part, body)
		for _, attachment := range message.Attachments {
			part, _ = writer.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {attachment.MIMEType},
				"Content-Disposition":       {"attachment"},
				"Content-Transfer-Encoding": {"base64"},
			})
			encoder := base64.NewEncoder(base64.StdEncoding, part)
			_, _ = encoder.Write(attachment.Bytes)
			_ = encoder.Close()
		}
		_ = writer.Close()
		out.Write(bodyOut.Bytes())
		return out.Bytes()
	}
	out.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	out.WriteString(body)
	return out.Bytes()
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
