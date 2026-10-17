// Package audit provides owner-controlled operational and MCP transcript logs.
package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

const defaultRetentionDays = 7
const defaultMaxFileBytes = 100 << 20
const maxCapturedContent = 1 << 20

// Open creates a JSON logger. Caller closes the returned closer at shutdown.
func Open(cfg config.Logging) (*slog.Logger, io.Closer, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Target == "" || cfg.Target == "stdout" {
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})), nopCloser{}, nil
	}
	writer, err := newRotatingWriter(cfg.Target, cfg.Rotation)
	if err != nil {
		return nil, nil, err
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})), writer, nil
}

func parseLevel(value string) (slog.Level, error) {
	switch value {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("invalid logging level")
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

type rotatingWriter struct {
	mu        sync.Mutex
	path      string
	retention time.Duration
	max       int64
	day       string
	file      *os.File
	size      int64
}

func newRotatingWriter(path string, cfg config.Rotation) (*rotatingWriter, error) {
	retention := cfg.RetentionDays
	if retention == 0 {
		retention = defaultRetentionDays
	}
	max := cfg.MaxFileSize
	if max == 0 {
		max = defaultMaxFileBytes
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("logging target must be an owner-only regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	w := &rotatingWriter{path: path, retention: time.Duration(retention) * 24 * time.Hour, max: int64(max)}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, w.cleanup()
}

func (w *rotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	w.file, w.size, w.day = file, info.Size(), time.Now().UTC().Format("20060102")
	return nil
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.day != time.Now().UTC().Format("20060102") || w.size+int64(len(data)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	name := w.path + "." + w.day + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.Rename(w.path, name); err != nil {
		return err
	}
	if err := w.open(); err != nil {
		return err
	}
	return w.cleanup()
}

func (w *rotatingWriter) cleanup() error {
	entries, err := os.ReadDir(filepath.Dir(w.path))
	if err != nil {
		return err
	}
	prefix := filepath.Base(w.path) + "."
	deadline := time.Now().Add(-w.retention)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(deadline) {
			if err := os.Remove(filepath.Join(filepath.Dir(w.path), entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Wrap records safe request metadata. Content capture is bounded and only
// intended for an explicit owner-only forensic log target.
func Wrap(next http.Handler, logger *slog.Logger, capture bool) http.Handler {
	return WrapMCP(next, logger, capture, "")
}

// WrapMCP records a request after the relay has authenticated its user.
func WrapMCP(next http.Handler, logger *slog.Logger, capture bool, user string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		var request *captureReadCloser
		if req.URL.Path == "/mcp" {
			request = &captureReadCloser{ReadCloser: req.Body}
			req.Body = request
		}
		recorder := &responseRecorder{ResponseWriter: w, capture: capture && req.URL.Path == "/mcp"}
		next.ServeHTTP(recorder, req)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		var requestBody []byte
		var requestTruncated bool
		if request != nil {
			requestBody, requestTruncated = request.body.Bytes(), request.truncated
		}
		attrs := []slog.Attr{slog.String("event", "mcp.request"), slog.String("method", req.Method), slog.String("path", req.URL.Path), slog.Int("status", recorder.status), slog.Duration("duration", time.Since(start)), slog.String("rpc_method", rpcMethod(requestBody)), slog.String("tool", toolName(requestBody))}
		if user != "" {
			attrs = append(attrs, slog.String("user", user))
		}
		if capture && req.URL.Path == "/mcp" {
			attrs = append(attrs, slog.String("request_content", string(requestBody)), slog.Bool("request_truncated", requestTruncated), slog.String("response_content", recorder.body.String()), slog.Bool("response_truncated", recorder.truncated))
		}
		logger.LogAttrs(req.Context(), slog.LevelInfo, "MCP request", attrs...)
	})
}

func rpcMethod(data []byte) string {
	var value struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(data, &value)
	return value.Method
}

func toolName(data []byte) string {
	var value struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &value) != nil || value.Method != "tools/call" {
		return ""
	}
	return value.Params.Name
}

type captureReadCloser struct {
	io.ReadCloser
	body      bytes.Buffer
	truncated bool
}

func (r *captureReadCloser) Read(data []byte) (int, error) {
	n, err := r.ReadCloser.Read(data)
	if n > 0 && r.body.Len() < maxCapturedContent {
		remaining := maxCapturedContent - r.body.Len()
		if n > remaining {
			_, _ = r.body.Write(data[:remaining])
			r.truncated = true
		} else {
			_, _ = r.body.Write(data[:n])
		}
	}
	return n, err
}

type responseRecorder struct {
	http.ResponseWriter
	status    int
	capture   bool
	body      bytes.Buffer
	truncated bool
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.capture && w.body.Len() < maxCapturedContent {
		remaining := maxCapturedContent - w.body.Len()
		if len(data) > remaining {
			_, _ = w.body.Write(data[:remaining])
			w.truncated = true
		} else {
			_, _ = w.body.Write(data)
		}
	}
	return w.ResponseWriter.Write(data)
}
