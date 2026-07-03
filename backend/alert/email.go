package alert

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// EmailConfig configures the SMTP EmailChannel. Password is never logged.
type EmailConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the envelope sender and From: header (e.g. "ping@example.com").
	From string
}

func (c EmailConfig) configured() bool { return c.Host != "" && c.From != "" }

func (c EmailConfig) addr() string { return net.JoinHostPort(c.Host, itoa(c.Port)) }

// EmailChannel delivers messages over SMTP. It supports implicit TLS (port
// 465, SMTPS) and STARTTLS (port 587 and others); the transport is chosen by
// port. Auth (PLAIN) is attempted only when a username is set, and only over
// an encrypted connection so the password is never sent in the clear.
type EmailChannel struct {
	cfg EmailConfig
	// dialTimeout bounds the initial TCP connect. Overall deadline still comes
	// from the caller's context.
	dialTimeout time.Duration
	// tlsConfig is overridable in tests (to trust a test server's cert). Nil
	// means a default config verifying against cfg.Host.
	tlsConfig *tls.Config
}

// NewEmailChannel builds an EmailChannel from cfg.
func NewEmailChannel(cfg EmailConfig) *EmailChannel {
	return &EmailChannel{cfg: cfg, dialTimeout: 10 * time.Second}
}

// Send delivers msg over SMTP. It returns ErrNotConfigured if no host is set,
// or a *SendError classifying transport failures as retryable or permanent.
func (c *EmailChannel) Send(ctx context.Context, msg Message) error {
	if !c.cfg.configured() {
		return ErrNotConfigured
	}

	// Reject header-injection attempts (CRLF in the envelope/header fields).
	// The recipient is normally a DB-sourced account email, but this is the
	// choke point every caller — including the alerter worker — passes through,
	// so guard it here rather than trusting each call site.
	if hasHeaderInjection(msg.To) || hasHeaderInjection(c.cfg.From) || hasHeaderInjection(msg.Subject) {
		return &SendError{Op: "validate", Err: errors.New("recipient, sender, or subject contains an illegal newline")}
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}

	client, err := c.dial(ctx, deadline)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if err := c.authenticate(client); err != nil {
		return err
	}

	if err := client.Mail(c.cfg.From); err != nil {
		return sendErr("from", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return sendErr("rcpt", err)
	}

	wc, err := client.Data()
	if err != nil {
		return sendErr("data", err)
	}
	if _, err := wc.Write(buildMIME(c.cfg.From, msg)); err != nil {
		return sendErr("write", err)
	}
	if err := wc.Close(); err != nil {
		return sendErr("write", err)
	}

	// The message was accepted at DATA close above; a QUIT hiccup is not a
	// delivery failure. The deferred Close cleans up the connection either way.
	_ = client.Quit()
	return nil
}

// dial establishes an authenticated-capable SMTP client, using implicit TLS on
// port 465 and STARTTLS elsewhere.
func (c *EmailChannel) dial(ctx context.Context, deadline time.Time) (*smtp.Client, error) {
	d := net.Dialer{Timeout: c.dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.cfg.addr())
	if err != nil {
		return nil, sendErr("dial", err)
	}
	_ = conn.SetDeadline(deadline)

	tlsCfg := c.tlsConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: c.cfg.Host, MinVersion: tls.VersionTLS12}
	}

	if c.cfg.Port == 465 {
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, sendErr("tls", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, sendErr("greet", err)
	}

	if c.cfg.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				_ = client.Close()
				return nil, sendErr("starttls", err)
			}
		}
	}

	return client, nil
}

// authenticate performs PLAIN auth when credentials are set. It refuses to
// send the password over an unencrypted link.
func (c *EmailChannel) authenticate(client *smtp.Client) error {
	if c.cfg.Username == "" {
		return nil
	}
	if ok, _ := client.Extension("AUTH"); !ok {
		// Server advertises no AUTH: treat as a permanent config mismatch.
		return &SendError{Op: "auth", Err: errors.New("server does not support AUTH")}
	}
	if _, isTLS := client.TLSConnectionState(); !isTLS {
		return &SendError{Op: "auth", Err: errors.New("refusing to send credentials over an unencrypted connection")}
	}
	auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, c.cfg.Host)
	if err := client.Auth(auth); err != nil {
		return sendErr("auth", err)
	}
	return nil
}

// sendErr classifies an SMTP/network error into a *SendError. SMTP 4xx and
// network/transient failures are retryable; 5xx and auth failures are
// permanent. The error text carries no credentials.
func sendErr(op string, err error) error {
	return &SendError{Op: op, Retryable: isRetryable(op, err), Err: err}
}

func isRetryable(op string, err error) bool {
	// Auth failures never succeed on retry with the same (wrong) credentials.
	if op == "auth" {
		return false
	}

	if proto, ok := errors.AsType[*textproto.Error](err); ok {
		// 4yz = transient negative completion (try again); 5yz = permanent.
		return proto.Code >= 400 && proto.Code < 500
	}

	// Network-level failures (dial timeouts, resets, TLS handshake) are
	// transient — the mail server may come back.
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	// Connection greet/dial stage with a non-textproto error is transport.
	switch op {
	case "dial", "tls", "starttls", "greet":
		return true
	}
	return false
}

// buildMIME renders a multipart/alternative message (text + optional HTML)
// with the standard headers. Subject is RFC 2047 encoded so non-ASCII survives.
func buildMIME(from string, msg Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")

	if msg.HTMLBody == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(normalizeCRLF(msg.TextBody))
		return []byte(b.String())
	}

	boundary := "ping-boundary-" + itoa(int(time.Now().UnixNano()&0x7fffffff))
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(normalizeCRLF(msg.TextBody))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString(normalizeCRLF(msg.HTMLBody))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// hasHeaderInjection reports whether s contains a CR or LF, which would let an
// attacker inject additional headers or a premature body into the message.
func hasHeaderInjection(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

// normalizeCRLF ensures bare LFs become CRLFs, as SMTP requires.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
