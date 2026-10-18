package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/audit"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

type identityKey struct{}

// Handler exposes only policy-filtered operations. It authenticates every
// request; MCP sessions are never used as credentials.
func Handler(r *relay.Relay) http.Handler {
	return HandlerWithAudit(r, nil, false)
}

func HandlerWithAudit(r *relay.Relay, logger *slog.Logger, captureContent bool) http.Handler {
	server := gomcp.NewServer(&gomcp.Implementation{Name: "sireno-relay", Version: "dev"}, nil)
	cursors := newCursorStore()
	gomcp.AddTool(server, &gomcp.Tool{Name: "list_folders", Description: "List folders visible to the authenticated user"}, folders(r))
	gomcp.AddTool(server, &gomcp.Tool{Name: "list_messages", Description: "List a 20-message sanitized page visible in one folder"}, messagesWithCursors(r, cursors))
	gomcp.AddTool(server, &gomcp.Tool{Name: "search_messages", Description: "Search sanitized messages with Himalaya's supported query language"}, searchMessages(r, cursors))
	gomcp.AddTool(server, &gomcp.Tool{Name: "read_message", Description: "Read one sanitized visible message by virtual UID"}, readMessage(r))
	gomcp.AddTool(server, &gomcp.Tool{Name: "read_messages", Description: "Read up to 20 sanitized visible messages by virtual UID"}, readMessages(r))
	gomcp.AddTool(server, &gomcp.Tool{Name: "send_message", Description: "Send a policy-checked plain-text message without attachments"}, sendMessage(r))
	gomcp.AddTool(server, &gomcp.Tool{Name: "update_message_flags", Description: "Add or remove safe flags on one visible writable message"}, updateMessageFlags(r))
	inner := gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server }, &gomcp.StreamableHTTPOptions{JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if origin := req.Header.Get("Origin"); origin != "" && origin != "http://sireno-relay" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		identity, ok := authenticatedIdentity(r, w, req)
		if !ok {
			return
		}
		if req.URL.Path == "/mcp/attachments" {
			if logger != nil {
				logger.Info("MCP attachment download", "event", "mcp.attachment_download", "user", identity.Name, "method", req.Method, "path", req.URL.Path)
			}
			serveAttachment(r, identity, w, req)
			return
		}
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var handler http.Handler = inner
		if logger != nil {
			handler = audit.WrapMCP(handler, logger, captureContent, identity.Name)
		}
		handler.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), identityKey{}, identity)))
	})
}

func authenticatedIdentity(r *relay.Relay, w http.ResponseWriter, req *http.Request) (*relay.Identity, bool) {
	parts := strings.Fields(req.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	identity, err := r.AuthenticateToken(parts[1])
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	return identity, true
}

func serveAttachment(r *relay.Relay, identity *relay.Identity, w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := req.URL.Query()
	messageUID, messageErr := strconv.ParseUint(query.Get("message_uid"), 10, 32)
	attachmentID, attachmentErr := strconv.ParseUint(query.Get("attachment_id"), 10, 32)
	folder := query.Get("folder")
	if messageErr != nil || attachmentErr != nil || folder == "" {
		http.Error(w, "attachment unavailable", http.StatusNotFound)
		return
	}
	attachment, err := r.Attachment(req.Context(), identity, folder, uint32(messageUID), uint32(attachmentID))
	if err != nil {
		http.Error(w, "attachment unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", attachment.MIMEType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(attachment.Bytes)
}

type listMessagesArgs struct {
	Folder string `json:"folder" jsonschema:"folder to list"`
	Query  string `json:"query,omitempty" jsonschema:"optional Himalaya envelope query"`
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque cursor returned by a previous page"`
}

type cursorStore struct {
	mu      sync.Mutex
	cursors map[string]cursor
}

type cursor struct {
	user, provider, binding, folder, query string
	page, offset                           uint32
	expires                                time.Time
}

func newCursorStore() *cursorStore { return &cursorStore{cursors: make(map[string]cursor)} }

func (s *cursorStore) page(identity *relay.Identity, folder, query, token string) (uint32, uint32, error) {
	if token == "" {
		return 1, 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.cursors[token]
	delete(s.cursors, token)
	if !ok || time.Now().After(value.expires) || value.user != identity.Name || value.provider != identity.AccountName || value.binding != bindingFingerprint(identity) || value.folder != folder || value.query != query || value.page == 0 {
		return 0, 0, errors.New("mailbox unavailable")
	}
	return value.page, value.offset, nil
}

func (s *cursorStore) next(identity *relay.Identity, folder, query string, page, offset uint32) (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors[token] = cursor{user: identity.Name, provider: identity.AccountName, binding: bindingFingerprint(identity), folder: folder, query: query, page: page, offset: offset, expires: time.Now().Add(5 * time.Minute)}
	return token, nil
}

func bindingFingerprint(identity *relay.Identity) string {
	data, _ := json.Marshal(struct {
		Account config.Provider
		Profile config.Profile
		Token   string
	}{identity.Account, identity.Profile, identity.TokenHash})
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest)
}

type envelope struct {
	UID         uint32              `json:"uid"`
	Subject     string              `json:"subject"`
	From        []string            `json:"from,omitempty"`
	To          []string            `json:"to,omitempty"`
	Cc          []string            `json:"cc,omitempty"`
	Date        string              `json:"date,omitempty"`
	Attachments []attachmentSummary `json:"attachments,omitempty"`
}

type attachmentSummary struct {
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int    `json:"size"`
}

type attachmentDescriptor struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type"`
	Size        int    `json:"size"`
	DownloadURL string `json:"download_url"`
}

type readMessageArgs struct {
	Folder string `json:"folder" jsonschema:"folder containing the message"`
	UID    uint32 `json:"uid" jsonschema:"virtual UID returned by list_messages"`
}

type readMessagesArgs struct {
	Folder string   `json:"folder" jsonschema:"folder containing the messages"`
	UIDs   []uint32 `json:"uids" jsonschema:"up to 20 virtual UIDs returned by list_messages"`
}

type messageContent struct {
	UID         uint32                 `json:"uid"`
	Subject     string                 `json:"subject"`
	From        []string               `json:"from,omitempty"`
	To          []string               `json:"to,omitempty"`
	Cc          []string               `json:"cc,omitempty"`
	Body        string                 `json:"body"`
	Attachments []attachmentDescriptor `json:"attachments,omitempty"`
}

type sendMessageArgs struct {
	From    string   `json:"from" jsonschema:"sender address; must be the provider account address"`
	To      []string `json:"to" jsonschema:"one or more recipient addresses"`
	Subject string   `json:"subject" jsonschema:"plain-text subject"`
	Body    string   `json:"body" jsonschema:"plain-text body; attachments are not supported"`
}

type updateMessageFlagsArgs struct {
	Folder string   `json:"folder" jsonschema:"folder containing the message"`
	UID    uint32   `json:"uid" jsonschema:"virtual UID returned by list_messages"`
	Flags  []string `json:"flags" jsonschema:"one or more of seen, flagged, answered"`
	Action string   `json:"action" jsonschema:"add or remove"`
}

func messages(r *relay.Relay) func(context.Context, *gomcp.CallToolRequest, listMessagesArgs) (*gomcp.CallToolResult, any, error) {
	return messagesWithCursors(r, newCursorStore())
}

func messagesWithCursors(r *relay.Relay, cursors *cursorStore) func(context.Context, *gomcp.CallToolRequest, listMessagesArgs) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, args listMessagesArgs) (*gomcp.CallToolResult, any, error) {
		identity, _ := ctx.Value(identityKey{}).(*relay.Identity)
		if identity == nil || args.Folder == "" || len(args.Query) > 512 || strings.ContainsAny(args.Query, "\r\n\x00") {
			return nil, nil, errors.New("mailbox unavailable")
		}
		if err := requireVisibleFolder(ctx, r, identity, args.Folder); err != nil {
			return nil, nil, err
		}
		page, offset, err := cursors.page(identity, args.Folder, args.Query, args.Cursor)
		if err != nil {
			return nil, nil, errors.New("mailbox unavailable")
		}
		items := make([]envelope, 0, 20)
		var nextPage, nextOffset uint32
		for scanned := 0; scanned < 50; scanned++ {
			snapshot, hasMore, err := r.Page(ctx, identity, args.Folder, page, args.Query)
			if err != nil {
				if errors.Is(err, provider.ErrCapacityUnavailable) {
					return nil, nil, provider.ErrCapacityUnavailable
				}
				return nil, nil, errors.New("mailbox unavailable")
			}
			if offset > uint32(len(snapshot.Messages)) {
				return nil, nil, errors.New("mailbox unavailable")
			}
			for i := offset; i < uint32(len(snapshot.Messages)); i++ {
				items = append(items, envelopeFromMessage(snapshot.Messages[i]))
				if len(items) == 20 {
					if i+1 < uint32(len(snapshot.Messages)) {
						nextPage, nextOffset = page, i+1
					} else if hasMore {
						nextPage = page + 1
					}
					break
				}
			}
			if len(items) == 20 || !hasMore {
				break
			}
			page++
			offset = 0
			if scanned == 49 {
				return nil, nil, errors.New("mailbox unavailable")
			}
		}
		result := map[string]any{"messages": items}
		if nextPage != 0 {
			next, err := cursors.next(identity, args.Folder, args.Query, nextPage, nextOffset)
			if err != nil {
				return nil, nil, errors.New("mailbox unavailable")
			}
			result["next_cursor"] = next
		}
		data, _ := json.Marshal(result)
		return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: string(data)}}, StructuredContent: result}, result, nil
	}
}

// requireVisibleFolder lets an agent recover from a stale or guessed folder
// name without revealing folders outside its current policy.
func requireVisibleFolder(ctx context.Context, r *relay.Relay, identity *relay.Identity, requested string) error {
	folders, err := r.Folders(ctx, identity)
	if err != nil {
		return errors.New("mailbox unavailable")
	}
	for _, folder := range folders {
		if folder == requested {
			return nil
		}
	}
	if len(folders) == 0 {
		return errors.New("folder unavailable; no visible folders are available")
	}
	quoted := make([]string, 0, len(folders))
	for _, folder := range folders {
		quoted = append(quoted, strconv.Quote(folder))
	}
	return errors.New("folder unavailable; available folders: " + strings.Join(quoted, ", "))
}

func envelopeFromMessage(message view.Message) envelope {
	item := envelope{UID: message.UID, Subject: message.Mail.Subject, From: message.Mail.From, To: message.Mail.To, Cc: message.Mail.Cc}
	if message.Mail.Date != nil {
		item.Date = message.Mail.Date.Format(time.RFC3339)
	}
	for _, attachment := range message.Mail.Attachments {
		item.Attachments = append(item.Attachments, attachmentSummary{Name: attachment.Name, MIMEType: attachment.MIMEType, Size: len(attachment.Bytes)})
	}
	return item
}

func searchMessages(r *relay.Relay, cursors *cursorStore) func(context.Context, *gomcp.CallToolRequest, listMessagesArgs) (*gomcp.CallToolResult, any, error) {
	list := messagesWithCursors(r, cursors)
	return func(ctx context.Context, request *gomcp.CallToolRequest, args listMessagesArgs) (*gomcp.CallToolResult, any, error) {
		if strings.TrimSpace(args.Query) == "" {
			return nil, nil, errors.New("mailbox unavailable")
		}
		return list(ctx, request, args)
	}
}

func readMessage(r *relay.Relay) func(context.Context, *gomcp.CallToolRequest, readMessageArgs) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, args readMessageArgs) (*gomcp.CallToolResult, any, error) {
		if args.Folder == "" || args.UID == 0 {
			return nil, nil, errors.New("message unavailable")
		}
		identity, _ := ctx.Value(identityKey{}).(*relay.Identity)
		message, err := r.Message(ctx, identity, args.Folder, args.UID)
		if err != nil {
			if errors.Is(err, provider.ErrCapacityUnavailable) {
				return nil, nil, provider.ErrCapacityUnavailable
			}
			return nil, nil, errors.New("message unavailable")
		}
		result := map[string]any{"message": messageResult(args.Folder, *message)}
		return structuredResult(result)
	}
}

func readMessages(r *relay.Relay) func(context.Context, *gomcp.CallToolRequest, readMessagesArgs) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, args readMessagesArgs) (*gomcp.CallToolResult, any, error) {
		if args.Folder == "" || len(args.UIDs) == 0 || len(args.UIDs) > 20 {
			return nil, nil, errors.New("between one and 20 message UIDs are required")
		}
		identity, _ := ctx.Value(identityKey{}).(*relay.Identity)
		seen := make(map[uint32]bool, len(args.UIDs))
		for _, uid := range args.UIDs {
			if uid == 0 || seen[uid] {
				return nil, nil, errors.New("message unavailable")
			}
			seen[uid] = true
		}
		messages, err := r.Messages(ctx, identity, args.Folder, args.UIDs)
		if err != nil {
			if errors.Is(err, provider.ErrCapacityUnavailable) {
				return nil, nil, provider.ErrCapacityUnavailable
			}
			return nil, nil, errors.New("message unavailable")
		}
		items := make([]messageContent, 0, len(messages))
		for _, message := range messages {
			items = append(items, messageResult(args.Folder, message))
		}
		return structuredResult(map[string]any{"messages": items})
	}
}

func sendMessage(r *relay.Relay) func(context.Context, *gomcp.CallToolRequest, sendMessageArgs) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, args sendMessageArgs) (*gomcp.CallToolResult, any, error) {
		identity, _ := ctx.Value(identityKey{}).(*relay.Identity)
		if identity == nil || !safeHeader(args.From) || !safeHeader(args.Subject) || len(args.To) == 0 || len(args.To) > 100 || len(args.Body) > 1<<20 {
			return nil, nil, errors.New("message unavailable")
		}
		for _, recipient := range args.To {
			if !safeHeader(recipient) {
				return nil, nil, errors.New("message unavailable")
			}
		}
		raw := []byte("From: " + args.From + "\r\nTo: " + strings.Join(args.To, ", ") + "\r\nSubject: " + args.Subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + args.Body)
		if err := r.Send(ctx, identity, args.From, args.To, raw); err != nil {
			if errors.Is(err, provider.ErrCapacityUnavailable) {
				return nil, nil, provider.ErrCapacityUnavailable
			}
			return nil, nil, errors.New("message unavailable")
		}
		return structuredResult(map[string]any{"sent": true})
	}
}

func safeHeader(value string) bool {
	return value != "" && len(value) <= 998 && !strings.ContainsAny(value, "\r\n\x00")
}

func updateMessageFlags(r *relay.Relay) func(context.Context, *gomcp.CallToolRequest, updateMessageFlagsArgs) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, args updateMessageFlagsArgs) (*gomcp.CallToolResult, any, error) {
		if args.Folder == "" || args.UID == 0 {
			return nil, nil, errors.New("message unavailable")
		}
		op, ok := flagOperation(args.Action)
		if !ok {
			return nil, nil, errors.New("message unavailable")
		}
		flags, ok := messageFlags(args.Flags)
		if !ok {
			return nil, nil, errors.New("message unavailable")
		}
		identity, _ := ctx.Value(identityKey{}).(*relay.Identity)
		if err := r.UpdateFlags(ctx, identity, args.Folder, args.UID, flags, op); err != nil {
			if errors.Is(err, provider.ErrCapacityUnavailable) {
				return nil, nil, provider.ErrCapacityUnavailable
			}
			return nil, nil, errors.New("message update unavailable")
		}
		return structuredResult(map[string]any{"updated": true})
	}
}

func flagOperation(action string) (provider.FlagAction, bool) {
	switch action {
	case "add":
		return provider.FlagAdd, true
	case "remove":
		return provider.FlagRemove, true
	default:
		return "", false
	}
}

func messageFlags(names []string) ([]provider.Flag, bool) {
	if len(names) == 0 || len(names) > 3 {
		return nil, false
	}
	known := map[string]provider.Flag{"seen": provider.FlagSeen, "flagged": provider.FlagFlagged, "answered": provider.FlagAnswered}
	flags := make([]provider.Flag, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		flag, ok := known[name]
		if !ok || seen[name] {
			return nil, false
		}
		seen[name] = true
		flags = append(flags, flag)
	}
	return flags, true
}

func messageResult(folder string, message view.Message) messageContent {
	result := messageContent{UID: message.UID, Subject: message.Mail.Subject, From: message.Mail.From, To: message.Mail.To, Cc: message.Mail.Cc, Body: message.Mail.Body}
	for _, attachment := range message.Mail.Attachments {
		result.Attachments = append(result.Attachments, attachmentDescriptor{ID: attachment.ID, Name: attachment.Name, MIMEType: attachment.MIMEType, Size: len(attachment.Bytes), DownloadURL: attachmentURL(folder, message.UID, attachment.ID)})
	}
	return result
}

func attachmentURL(folder string, messageUID, attachmentID uint32) string {
	return "/mcp/attachments?folder=" + url.QueryEscape(folder) + "&message_uid=" + strconv.FormatUint(uint64(messageUID), 10) + "&attachment_id=" + strconv.FormatUint(uint64(attachmentID), 10)
}

func structuredResult(result map[string]any) (*gomcp.CallToolResult, any, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, nil, errors.New("message unavailable")
	}
	return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: string(data)}}, StructuredContent: result}, result, nil
}

func folders(r *relay.Relay) func(context.Context, *gomcp.CallToolRequest, struct{}) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, _ struct{}) (*gomcp.CallToolResult, any, error) {
		identity, _ := ctx.Value(identityKey{}).(*relay.Identity)
		items, err := r.Folders(ctx, identity)
		if err != nil {
			if errors.Is(err, provider.ErrCapacityUnavailable) {
				return nil, nil, provider.ErrCapacityUnavailable
			}
			return nil, nil, err
		}
		data, _ := json.Marshal(items)
		result := map[string]any{"folders": items}
		return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: string(data)}}, StructuredContent: result}, result, nil
	}
}
