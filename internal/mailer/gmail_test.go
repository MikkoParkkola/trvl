package mailer

import (
	"errors"
	"net/smtp"
	"strings"
	"testing"
)

func TestNewFromEnv_DryRunNeedsNoCredentials(t *testing.T) {
	t.Setenv("TRVL_GMAIL_USER", "")
	t.Setenv("TRVL_GMAIL_APP_PASSWORD", "")
	t.Setenv("TRVL_MAILER_DRYRUN", "1")
	m, err := NewFromEnv()
	if err != nil {
		t.Fatalf("dry-run NewFromEnv should not require creds: %v", err)
	}
	if !m.DryRun {
		t.Fatal("expected DryRun true")
	}
}

func TestNewFromEnv_RequiresCredentialsWhenSending(t *testing.T) {
	t.Setenv("TRVL_GMAIL_USER", "")
	t.Setenv("TRVL_GMAIL_APP_PASSWORD", "")
	t.Setenv("TRVL_MAILER_DRYRUN", "0")
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("expected error when credentials missing and not dry-run")
	}
}

func TestSendDigest_DryRunComposesWithoutSocket(t *testing.T) {
	m := &Mailer{User: "me@example.com", DryRun: true}
	msg, err := m.SendDigest("subj", "hello body")
	if err != nil {
		t.Fatalf("dry-run send: %v", err)
	}
	raw := string(msg.Bytes())
	for _, want := range []string{"From: me@example.com", "To: me@example.com", "Subject: subj", "hello body"} {
		if !strings.Contains(raw, want) {
			t.Errorf("composed message missing %q:\n%s", want, raw)
		}
	}
}

func TestSendDigest_UsesInjectedTransport(t *testing.T) {
	var gotAddr, gotFrom string
	var gotTo []string
	var gotMsg []byte
	m := &Mailer{
		User:     "me@example.com",
		Password: "app-pass",
		Send: func(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
			gotAddr, gotFrom, gotTo, gotMsg = addr, from, to, msg
			return nil
		},
	}
	if _, err := m.SendDigest("subj", "body"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotAddr != gmailSMTPAddr {
		t.Errorf("addr = %q, want %q", gotAddr, gmailSMTPAddr)
	}
	if gotFrom != "me@example.com" || len(gotTo) != 1 || gotTo[0] != "me@example.com" {
		t.Errorf("from/to wrong: from=%q to=%v", gotFrom, gotTo)
	}
	if !strings.Contains(string(gotMsg), "Subject: subj") {
		t.Errorf("message missing subject: %s", gotMsg)
	}
}

func TestSendDigest_PropagatesTransportError(t *testing.T) {
	m := &Mailer{
		User:     "me@example.com",
		Password: "app-pass",
		Send: func(string, smtp.Auth, string, []string, []byte) error {
			return errors.New("boom")
		},
	}
	if _, err := m.SendDigest("s", "b"); err == nil {
		t.Fatal("expected transport error to propagate")
	}
}
