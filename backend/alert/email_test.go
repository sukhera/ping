package alert

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSendErrClassification(t *testing.T) {
	tests := []struct {
		name          string
		op            string
		err           error
		wantRetryable bool
	}{
		{"smtp 4xx transient", "rcpt", &textproto.Error{Code: 451, Msg: "try again"}, true},
		{"smtp 5xx permanent", "rcpt", &textproto.Error{Code: 550, Msg: "no such user"}, false},
		{"auth failure never retries", "auth", &textproto.Error{Code: 535, Msg: "bad creds"}, false},
		{"network timeout retries", "dial", timeoutErr{}, true},
		{"dial stage generic error retries", "dial", errors.New("connection refused"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sendErr(tt.op, tt.err)
			if got := IsRetryable(err); got != tt.wantRetryable {
				t.Errorf("IsRetryable = %v, want %v", got, tt.wantRetryable)
			}
			var se *SendError
			if !errors.As(err, &se) {
				t.Fatalf("expected *SendError, got %T", err)
			}
			if se.Op != tt.op {
				t.Errorf("Op = %q, want %q", se.Op, tt.op)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestSendUnconfigured(t *testing.T) {
	ch := NewEmailChannel(EmailConfig{})
	err := ch.Send(context.Background(), Message{To: "a@b.com"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	ch := NewEmailChannel(EmailConfig{Host: "localhost", Port: 1025, From: "ping@example.com"})
	err := ch.Send(context.Background(), Message{
		To:      "victim@example.com\r\nBcc: attacker@evil.com",
		Subject: "hi",
	})
	if err == nil {
		t.Fatal("expected header-injection recipient to be rejected")
	}
	var se *SendError
	if !errors.As(err, &se) || se.Op != "validate" {
		t.Fatalf("want validate SendError, got %v", err)
	}
}

// TestSendHappyPath drives Send against a minimal in-process SMTP server that
// speaks just enough of the protocol (no TLS, no AUTH) to accept a message,
// and asserts the delivered payload contains the rendered bodies.
func TestSendHappyPath(t *testing.T) {
	srv := newFakeSMTP(t)
	defer srv.Close()

	host, port := srv.hostPort(t)
	ch := NewEmailChannel(EmailConfig{Host: host, Port: port, From: "ping@example.com"})

	msg := Render(Notification{Kind: KindTest, At: time.Now()})
	msg.To = "user@example.com"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.data()
	for _, want := range []string{
		"From: ping@example.com",
		"To: user@example.com",
		"MIME-Version: 1.0",
		"multipart/alternative",
		"This is a test email from ping.", // text part
		"SMTP test email",                 // html headline
	} {
		if !strings.Contains(got, want) {
			t.Errorf("delivered message missing %q\n---\n%s", want, got)
		}
	}
	// The subject carries a non-ASCII em-dash, so it is RFC 2047 encoded.
	if !strings.Contains(got, "Subject: =?utf-8?") {
		t.Errorf("expected RFC 2047 encoded Subject header\n%s", got)
	}
}

// fakeSMTP is a throwaway SMTP server for tests. It accepts one connection,
// records the DATA payload, and always returns 2xx.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	body string
	done chan struct{}
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTP{ln: ln, done: make(chan struct{})}
	go s.serve()
	return s
}

func (s *fakeSMTP) serve() {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 fake ESMTP")
	r := bufio.NewReader(conn)
	inData := false
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" || line == ".\n" {
				inData = false
				s.mu.Lock()
				s.body = b.String()
				s.mu.Unlock()
				_ = tp.PrintfLine("250 OK queued")
				continue
			}
			b.WriteString(line)
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// Advertise nothing (no STARTTLS/AUTH) — plain path for the test.
			_ = tp.PrintfLine("250 fake")
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
			_ = tp.PrintfLine("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			_ = tp.PrintfLine("354 send data")
			inData = true
		case strings.HasPrefix(cmd, "QUIT"):
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("250 OK")
		}
	}
}

func (s *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	addr := s.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func (s *fakeSMTP) data() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body
}

func (s *fakeSMTP) Close() {
	_ = s.ln.Close()
	select {
	case <-s.done:
	case <-time.After(time.Second):
	}
}
