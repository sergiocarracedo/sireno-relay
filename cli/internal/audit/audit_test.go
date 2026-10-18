package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrapRecordsOnlyMCPMetadataWithoutChangingRequest(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := WrapMCP(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"list_messages"}}` {
			t.Fatalf("request=%q", data)
		}
		_, _ = w.Write([]byte(`{"body":"private message"}`))
	}), logger, true, "hermes-agent")
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"list_messages"}}`))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["tool"] != "list_messages" || entry["user"] != "hermes-agent" || strings.Contains(logs.String(), "request_content") || strings.Contains(logs.String(), "response_content") || strings.Contains(logs.String(), "private message") || strings.Contains(logs.String(), "Authorization") {
		t.Fatalf("log=%s", logs.String())
	}
}

func TestWrapNeverCapturesAttachmentBytes(t *testing.T) {
	var logs bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("binary-pdf")) }), slog.New(slog.NewJSONHandler(&logs, nil)), true)
	request := httptest.NewRequest(http.MethodGet, "/mcp/attachments?attachment_id=1", nil)
	h.ServeHTTP(httptest.NewRecorder(), request)
	if strings.Contains(logs.String(), "binary-pdf") || strings.Contains(logs.String(), "request_content") {
		t.Fatalf("attachment was captured: %s", logs.String())
	}
}
