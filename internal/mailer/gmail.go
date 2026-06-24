// Package mailer sends trvl digests over Gmail's SMTP submission endpoint
// using an app password. It exposes a dry-run mode so callers (and tests) can
// compose and inspect the outgoing message without opening a socket.
package mailer

import (
	"errors"
	"fmt"
	"net/smtp"
	"os"
	"strings"
)

// Gmail's SMTP submission endpoint (STARTTLS on 587 is handled by
// smtp.SendMail, which negotiates TLS when the server advertises it).
const gmailSMTPAddr = "smtp.gmail.com:587"

// Message is a composed plain-text email ready to send.
type Message struct {
	From    string
	To      string
	Subject string
	Body    string
}

// Bytes renders the RFC-5322 message. Deterministic: no Date header so the
// composed bytes are stable for tests (the SMTP server stamps its own
// Received/Date on delivery).
func (m Message) Bytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", m.From)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", m.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(m.Body)
	return []byte(b.String())
}

// SendFunc is the transport seam. The default sends over Gmail SMTP; tests
// inject a no-socket implementation.
type SendFunc func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error

// Mailer composes and dispatches digests. Construct with NewFromEnv for the
// production path or set Send directly for tests.
type Mailer struct {
	User     string
	Password string
	DryRun   bool
	// Send is the transport. When nil, smtp.SendMail is used.
	Send SendFunc
}

// NewFromEnv reads Gmail credentials from the environment:
//
//	TRVL_GMAIL_USER          — the sending Gmail address (also the From/To).
//	TRVL_GMAIL_APP_PASSWORD  — a Gmail app password (not the account password).
//	TRVL_MAILER_DRYRUN=1     — compose but do not send.
//
// In dry-run mode credentials are optional, so the digest can be previewed
// without secrets configured.
func NewFromEnv() (*Mailer, error) {
	m := &Mailer{
		User:     os.Getenv("TRVL_GMAIL_USER"),
		Password: os.Getenv("TRVL_GMAIL_APP_PASSWORD"),
		DryRun:   os.Getenv("TRVL_MAILER_DRYRUN") == "1",
	}
	if !m.DryRun {
		if m.User == "" || m.Password == "" {
			return nil, errors.New("mailer: TRVL_GMAIL_USER and TRVL_GMAIL_APP_PASSWORD are required (or set TRVL_MAILER_DRYRUN=1)")
		}
	}
	return m, nil
}

// Compose builds the outgoing message addressed from and to the configured
// Gmail user. The deal-radar is a self-digest, so sender and recipient match.
func (m *Mailer) Compose(subject, body string) Message {
	return Message{From: m.User, To: m.User, Subject: subject, Body: body}
}

// Send composes and dispatches the digest. In dry-run mode it returns the
// composed message without touching the network. The returned Message is
// always the exact bytes that would be (or were) sent.
func (m *Mailer) SendDigest(subject, body string) (Message, error) {
	msg := m.Compose(subject, body)
	if m.DryRun {
		return msg, nil
	}
	if m.User == "" || m.Password == "" {
		return msg, errors.New("mailer: missing Gmail credentials")
	}
	send := m.Send
	if send == nil {
		send = smtp.SendMail
	}
	auth := smtp.PlainAuth("", m.User, m.Password, "smtp.gmail.com")
	if err := send(gmailSMTPAddr, auth, m.User, []string{m.To(msg)}, msg.Bytes()); err != nil {
		return msg, fmt.Errorf("mailer: send failed: %w", err)
	}
	return msg, nil
}

// To returns the recipient for a composed message (kept as a method so future
// multi-recipient support has a single seam).
func (m *Mailer) To(msg Message) string { return msg.To }
