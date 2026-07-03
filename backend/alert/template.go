package alert

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// Kind identifies which notification to render.
type Kind string

const (
	// KindDown fires on any * → down transition (PRD F3.1).
	KindDown Kind = "down"
	// KindUp fires on a down → up recovery, with downtime duration.
	KindUp Kind = "up"
	// KindTLSExpiry warns that an HTTP monitor's TLS certificate is expiring.
	KindTLSExpiry Kind = "tls_expiry"
	// KindReminder is the periodic "still down" reminder (PRD F3.3).
	KindReminder Kind = "reminder"
	// KindTest is the "send test email" verification message.
	KindTest Kind = "test"
)

// Notification is the channel-agnostic data a template renders from. The
// alerter worker (PING-012) builds one per outbox row; the test endpoint
// builds a KindTest one directly.
type Notification struct {
	Kind        Kind
	MonitorName string
	MonitorSlug string
	// Reason is the human-readable cause for a down alert (e.g.
	// "missed check-in", "3 consecutive probe failures: connection refused").
	Reason string
	// Downtime is the resolved outage duration for KindUp / the elapsed
	// duration so far for KindReminder.
	Downtime time.Duration
	// TLSExpiresAt is set for KindTLSExpiry.
	TLSExpiresAt time.Time
	// At is the event timestamp shown in the body.
	At time.Time
	// DashboardURL links straight to the monitor's detail page.
	DashboardURL string
}

// Render produces a ready-to-send Message (subject + text + HTML) for n.
func Render(n Notification) Message {
	return Message{
		Subject:  subject(n),
		TextBody: renderText(n),
		HTMLBody: renderHTML(n),
	}
}

// subject follows PRD F3.2, e.g. "[DOWN] nightly-backup — missed check-in",
// "[UP] nightly-backup — recovered after 42m".
func subject(n Notification) string {
	switch n.Kind {
	case KindDown:
		return fmt.Sprintf("[DOWN] %s — %s", n.MonitorName, reasonOrDefault(n.Reason, "check failed"))
	case KindUp:
		return fmt.Sprintf("[UP] %s — recovered after %s", n.MonitorName, humanDuration(n.Downtime))
	case KindTLSExpiry:
		return fmt.Sprintf("[TLS] %s — certificate expires %s", n.MonitorName, expiryWindow(n.TLSExpiresAt, n.At))
	case KindReminder:
		return fmt.Sprintf("[DOWN] %s — still down after %s", n.MonitorName, humanDuration(n.Downtime))
	case KindTest:
		return "[TEST] ping — SMTP delivery is working"
	default:
		return fmt.Sprintf("[ping] %s", n.MonitorName)
	}
}

func renderText(n Notification) string {
	var b strings.Builder
	switch n.Kind {
	case KindDown:
		fmt.Fprintf(&b, "%s is DOWN.\n\n", n.MonitorName)
		fmt.Fprintf(&b, "Reason: %s\n", reasonOrDefault(n.Reason, "check failed"))
	case KindUp:
		fmt.Fprintf(&b, "%s has RECOVERED.\n\n", n.MonitorName)
		fmt.Fprintf(&b, "Downtime: %s\n", humanDuration(n.Downtime))
	case KindTLSExpiry:
		fmt.Fprintf(&b, "%s: TLS certificate expiring soon.\n\n", n.MonitorName)
		fmt.Fprintf(&b, "Expires: %s (%s)\n", n.TLSExpiresAt.UTC().Format(time.RFC1123), expiryWindow(n.TLSExpiresAt, n.At))
	case KindReminder:
		fmt.Fprintf(&b, "%s is STILL DOWN.\n\n", n.MonitorName)
		fmt.Fprintf(&b, "Down for: %s\n", humanDuration(n.Downtime))
		if n.Reason != "" {
			fmt.Fprintf(&b, "Reason: %s\n", n.Reason)
		}
	case KindTest:
		b.WriteString("This is a test email from ping.\n\n")
		b.WriteString("If you received this, your SMTP settings are working and alerts will be delivered here.\n")
	}

	if n.MonitorSlug != "" {
		fmt.Fprintf(&b, "Monitor: %s\n", n.MonitorSlug)
	}
	if !n.At.IsZero() {
		fmt.Fprintf(&b, "Time: %s\n", n.At.UTC().Format(time.RFC1123))
	}
	if n.DashboardURL != "" {
		fmt.Fprintf(&b, "\nView monitor: %s\n", n.DashboardURL)
	}
	return b.String()
}

// Design tokens (DESIGN.md §4–5) inlined for email clients, which do not
// support external stylesheets or CSS variables.
const (
	colBg      = "#0B0D10"
	colSurface = "#12151A"
	colBorder  = "#232830"
	colText    = "#E6EAF0"
	colTextDim = "#8B94A3"
	colAccent  = "#6E9BF5"
	colUp      = "#2DD4A7"
	colLate    = "#F5B84B"
	colDown    = "#F4564E"
)

// accentFor returns the status color for the notification's headline.
func accentFor(k Kind) string {
	switch k {
	case KindUp:
		return colUp
	case KindTLSExpiry:
		return colLate
	case KindDown, KindReminder:
		return colDown
	default:
		return colAccent
	}
}

func headline(n Notification) string {
	switch n.Kind {
	case KindDown:
		return "Monitor is down"
	case KindUp:
		return "Monitor recovered"
	case KindTLSExpiry:
		return "TLS certificate expiring"
	case KindReminder:
		return "Still down"
	case KindTest:
		return "SMTP test email"
	default:
		return "Notification"
	}
}

// renderHTML produces a minimal, single-column dark email using DESIGN tokens.
// All dynamic values are HTML-escaped.
func renderHTML(n Notification) string {
	accent := accentFor(n.Kind)
	var rows strings.Builder
	row := func(label, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&rows,
			`<tr><td style="padding:4px 0;color:%s;font-size:13px;width:120px;vertical-align:top">%s</td>`+
				`<td style="padding:4px 0;color:%s;font-size:13px">%s</td></tr>`,
			colTextDim, html.EscapeString(label), colText, html.EscapeString(value))
	}

	switch n.Kind {
	case KindDown:
		row("Reason", reasonOrDefault(n.Reason, "check failed"))
	case KindUp:
		row("Downtime", humanDuration(n.Downtime))
	case KindTLSExpiry:
		row("Expires", n.TLSExpiresAt.UTC().Format(time.RFC1123))
		row("Window", expiryWindow(n.TLSExpiresAt, n.At))
	case KindReminder:
		row("Down for", humanDuration(n.Downtime))
		row("Reason", n.Reason)
	}
	row("Monitor", n.MonitorSlug)
	if !n.At.IsZero() {
		row("Time", n.At.UTC().Format(time.RFC1123))
	}

	var button string
	if n.DashboardURL != "" {
		button = fmt.Sprintf(
			`<a href="%s" style="display:inline-block;margin-top:20px;padding:9px 16px;`+
				`background:%s;color:%s;text-decoration:none;border-radius:6px;font-size:13px;font-weight:600">View monitor</a>`,
			html.EscapeString(n.DashboardURL), colAccent, colBg)
	}

	intro := ""
	if n.Kind == KindTest {
		intro = `<p style="margin:0 0 4px;color:` + colTextDim + `;font-size:13px">` +
			`If you received this, your SMTP settings are working and alerts will be delivered here.</p>`
	}

	return fmt.Sprintf(`<!doctype html>
<html><body style="margin:0;padding:0;background:%s">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:%s"><tr><td align="center" style="padding:24px">
<table role="presentation" width="480" cellpadding="0" cellspacing="0" style="max-width:480px;background:%s;border:1px solid %s;border-radius:8px">
<tr><td style="padding:24px">
<div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:%s;font-weight:600">ping</div>
<h1 style="margin:6px 0 16px;color:%s;font-size:18px;font-weight:600">%s: %s</h1>
%s
<table role="presentation" cellpadding="0" cellspacing="0" width="100%%">%s</table>
%s
</td></tr></table>
</td></tr></table>
</body></html>`,
		colBg, colBg, colSurface, colBorder,
		accent, colText, html.EscapeString(headline(n)), html.EscapeString(n.MonitorName),
		intro, rows.String(), button)
}

func reasonOrDefault(reason, def string) string {
	if strings.TrimSpace(reason) == "" {
		return def
	}
	return reason
}

// humanDuration renders a coarse, human-scannable duration like "42m", "3h",
// "2d 4h" — matching the PRD subject examples rather than Go's "42m0s".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	if h == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, h)
}

// expiryWindow renders "in 41 days" / "in 6 hours" / "already expired" relative
// to now.
func expiryWindow(expiresAt, now time.Time) string {
	if expiresAt.IsZero() {
		return "unknown"
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := expiresAt.Sub(now)
	if d <= 0 {
		return "already expired"
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("in %d hours", int(d.Hours()))
	}
	return fmt.Sprintf("in %d days", int(d.Hours())/24)
}
