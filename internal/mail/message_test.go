package mail

import (
	"errors"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

const sample = "From: Alice <alice@example.org>\r\nTo: me@example.org\r\nSubject: Invoice\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSee https://example.org/reset?token=abc for info"

func TestEvaluate(t *testing.T) {
	msg, err := Evaluate([]byte(sample), config.ContentRule{RemoveLinks: true})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "Invoice" || len(msg.From) != 1 || msg.From[0] != "alice@example.org" {
		t.Fatalf("unexpected envelope: %+v", msg)
	}
	if strings.Contains(string(msg.Bytes), "token=abc") || !strings.Contains(string(msg.Bytes), "[link removed]") {
		t.Fatal("link was not removed")
	}
	if _, err := Evaluate([]byte(sample), config.ContentRule{DeniedSubjects: []string{"Invo*"}}); !errors.Is(err, ErrDenied) {
		t.Fatal("denied subject was visible")
	}
	if _, err := Evaluate([]byte(sample), config.ContentRule{DeniedBodies: []string{"re:(?i)reset"}}); !errors.Is(err, ErrDenied) {
		t.Fatal("denied body was visible")
	}
}

func TestFailClosed(t *testing.T) {
	for _, raw := range []string{
		"From: a@example.org\r\nContent-Type: text/html\r\n\r\n<a href='https://secret'>x</a>",
		"From: a@example.org\r\nContent-Type: multipart/alternative; boundary=x\r\n\r\n--x--",
		"From: a@example.org\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8=",
	} {
		if _, err := Evaluate([]byte(raw), config.ContentRule{}); !errors.Is(err, ErrDenied) {
			t.Errorf("accepted unsupported mail: %s", raw)
		}
	}
	if _, err := Evaluate([]byte(sample), config.ContentRule{Scanner: []string{"nonexistent-scanner-path"}}); !errors.Is(err, ErrDenied) {
		t.Fatal("scanner failure leaked")
	}
}

func TestRemoveLinksFromDecodedHeaders(t *testing.T) {
	raw := "From: Alice <alice@example.org>\r\nSubject: =?UTF-8?Q?https://secret.example/reset?=\r\n\r\nhello"
	message, err := Evaluate([]byte(raw), config.ContentRule{RemoveLinks: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(message.Bytes), "secret.example") {
		t.Fatalf("encoded link leaked: %q", message.Bytes)
	}
}
