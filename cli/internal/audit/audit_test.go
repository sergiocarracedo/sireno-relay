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

func TestWrapMCPRecordsAllowlistedResponseSummaries(t *testing.T) {
	for _, test := range []struct {
		name, tool, response string
		want                 map[string]any
		forbidden            []string
	}{
		{
			name:     "folders",
			tool:     "list_folders",
			response: `{"jsonrpc":"2.0","result":{"structuredContent":{"folders":["INBOX","Receipts"]}}}`,
			want:     map[string]any{"outcome": "success", "folder_count": float64(2), "folder_names": []any{"INBOX", "Receipts"}},
		},
		{
			name:      "messages",
			tool:      "list_messages",
			response:  `{"jsonrpc":"2.0","result":{"structuredContent":{"messages":[{"subject":"private subject","attachment_count":2},{"from":"owner@example.test","attachment_count":1}]}}}`,
			want:      map[string]any{"outcome": "success", "message_count": float64(2), "attachment_count": float64(3)},
			forbidden: []string{"private subject", "owner@example.test"},
		},
		{
			name:     "search",
			tool:     "search_messages",
			response: `{"jsonrpc":"2.0","result":{"structuredContent":{"messages":[]}}}`,
			want:     map[string]any{"outcome": "success", "message_count": float64(0), "attachment_count": float64(0)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := auditEntry(t, test.tool, test.response)
			for key, want := range test.want {
				if got := entry[key]; !equalJSONValue(got, want) {
					t.Fatalf("%s=%#v, want %#v", key, got, want)
				}
			}
			encoded, _ := json.Marshal(entry)
			for _, forbidden := range test.forbidden {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("content leaked: %s", encoded)
				}
			}
		})
	}
}

func TestWrapMCPClassifiesProviderFailuresWithoutLoggingErrorText(t *testing.T) {
	for _, test := range []struct {
		name, failure, operation, reason string
	}{
		{"envelope listing", "mailbox unavailable: himalaya envelope listing failed", "envelope_listing", "envelope_listing_failed"},
		{"message read", "message unavailable: himalaya message read failed", "message_read", "message_read_failed"},
		{"capacity", "capacity unavailable", "capability_check", "capacity_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := auditEntry(t, "search_messages", `{"jsonrpc":"2.0","result":{"isError":true,"content":[{"type":"text","text":"`+test.failure+`"}]}}`)
			if entry["outcome"] != "provider_failed" || entry["provider_operation"] != test.operation || entry["failure_reason"] != test.reason {
				t.Fatalf("entry=%#v", entry)
			}
			encoded, _ := json.Marshal(entry)
			if strings.Contains(string(encoded), "himalaya") || strings.Contains(string(encoded), test.failure) {
				t.Fatalf("provider error leaked: %s", encoded)
			}
		})
	}
}

func auditEntry(t *testing.T, tool, response string) map[string]any {
	t.Helper()
	var logs bytes.Buffer
	h := WrapMCP(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = w.Write([]byte(response))
	}), slog.New(slog.NewJSONHandler(&logs, nil)), false, "hermes-agent")
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"`+tool+`"}}`))
	h.ServeHTTP(httptest.NewRecorder(), request)
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func equalJSONValue(got, want any) bool {
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	return bytes.Equal(gotJSON, wantJSON)
}
