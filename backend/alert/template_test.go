package alert

import (
	"strings"
	"testing"
	"time"
)

func TestSubjectFormat(t *testing.T) {
	base := time.Date(2026, 7, 2, 4, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		n    Notification
		want string
	}{
		{
			name: "down matches PRD F3.2",
			n:    Notification{Kind: KindDown, MonitorName: "nightly-backup", Reason: "missed check-in", At: base},
			want: "[DOWN] nightly-backup — missed check-in",
		},
		{
			name: "up matches PRD F3.2",
			n:    Notification{Kind: KindUp, MonitorName: "nightly-backup", Downtime: 42 * time.Minute, At: base},
			want: "[UP] nightly-backup — recovered after 42m",
		},
		{
			name: "reminder still down",
			n:    Notification{Kind: KindReminder, MonitorName: "nightly-backup", Downtime: 26 * time.Hour, At: base},
			want: "[DOWN] nightly-backup — still down after 1d 2h",
		},
		{
			name: "tls expiry window",
			n:    Notification{Kind: KindTLSExpiry, MonitorName: "api.example.com", TLSExpiresAt: base.Add(41 * 24 * time.Hour), At: base},
			want: "[TLS] api.example.com — certificate expires in 41 days",
		},
		{
			name: "down without reason falls back",
			n:    Notification{Kind: KindDown, MonitorName: "web", At: base},
			want: "[DOWN] web — check failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subject(tt.n); got != tt.want {
				t.Errorf("subject() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{42 * time.Minute, "42m"},
		{90 * time.Minute, "1h 30m"},
		{2 * time.Hour, "2h"},
		{26 * time.Hour, "1d 2h"},
		{48 * time.Hour, "2d"},
		{-5 * time.Minute, "0s"},
	}
	for _, tt := range tests {
		if got := humanDuration(tt.d); got != tt.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestRenderBodiesContainKeyFacts(t *testing.T) {
	n := Notification{
		Kind:         KindDown,
		MonitorName:  "nightly-backup",
		MonitorSlug:  "nightly-backup-a1b2",
		Reason:       "missed check-in",
		At:           time.Date(2026, 7, 2, 4, 30, 0, 0, time.UTC),
		DashboardURL: "https://ping.example.com/m/nightly-backup-a1b2",
	}
	msg := Render(n)

	for _, want := range []string{"nightly-backup", "missed check-in", "nightly-backup-a1b2", n.DashboardURL} {
		if !strings.Contains(msg.TextBody, want) {
			t.Errorf("text body missing %q\n%s", want, msg.TextBody)
		}
		if !strings.Contains(msg.HTMLBody, want) {
			t.Errorf("html body missing %q", want)
		}
	}
	if !strings.Contains(msg.HTMLBody, colDown) {
		t.Errorf("down html should use the --down token %s", colDown)
	}
}

func TestRenderHTMLEscapesInjection(t *testing.T) {
	n := Notification{
		Kind:        KindDown,
		MonitorName: `<script>alert(1)</script>`,
		Reason:      `boom" onmouseover="x`,
		At:          time.Now(),
	}
	msg := Render(n)
	if strings.Contains(msg.HTMLBody, "<script>alert(1)</script>") {
		t.Errorf("html body did not escape monitor name:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "&lt;script&gt;") {
		t.Errorf("expected escaped monitor name in html body")
	}
}

func TestExpiryWindow(t *testing.T) {
	now := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		expires time.Time
		want    string
	}{
		{now.Add(41 * 24 * time.Hour), "in 41 days"},
		{now.Add(6 * time.Hour), "in 6 hours"},
		{now.Add(-time.Hour), "already expired"},
		{time.Time{}, "unknown"},
	}
	for _, tt := range tests {
		if got := expiryWindow(tt.expires, now); got != tt.want {
			t.Errorf("expiryWindow(%v) = %q, want %q", tt.expires, got, tt.want)
		}
	}
}
