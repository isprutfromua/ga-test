package mailer

import (
	"strings"
	"testing"
	"time"

	"github.com/isprutfromua/ga-test/internal/config"
)

func TestNew(t *testing.T) {
	t.Run("without username creates nil auth", func(t *testing.T) {
		m := New(config.SMTPConfig{Host: "smtp.example.com", Port: "2525", From: "noreply@example.com"})
		smtpM, ok := m.(*smtpMailer)
		if !ok {
			t.Fatalf("type = %T, want *smtpMailer", m)
		}
		if smtpM.addr != "smtp.example.com:2525" {
			t.Fatalf("addr = %q, want smtp.example.com:2525", smtpM.addr)
		}
		if smtpM.from != "noreply@example.com" {
			t.Fatalf("from = %q, want noreply@example.com", smtpM.from)
		}
		if smtpM.auth != nil {
			t.Fatal("auth should be nil when username is empty")
		}
	})

	t.Run("with username creates auth", func(t *testing.T) {
		m := New(config.SMTPConfig{Host: "smtp.example.com", Port: "2525", Username: "user", Password: "pass", From: "noreply@example.com"})
		smtpM, ok := m.(*smtpMailer)
		if !ok {
			t.Fatalf("type = %T, want *smtpMailer", m)
		}
		if smtpM.auth == nil {
			t.Fatal("auth should not be nil when username is set")
		}
	})
}

func TestRender(t *testing.T) {
	html := render("Welcome", "Confirm below", "http://example.com/confirm/token")

	if !strings.Contains(html, "<h1>Welcome</h1>") {
		t.Fatalf("render output missing title: %q", html)
	}
	if !strings.Contains(html, "Confirm below") {
		t.Fatalf("render output missing body: %q", html)
	}
	if !strings.Contains(html, "http://example.com/confirm/token") {
		t.Fatalf("render output missing URL: %q", html)
	}
}

func TestRenderRelease(t *testing.T) {
	release := ReleaseInfo{
		TagName:     "v1.2.3",
		Name:        "R1",
		Body:        "<b>important</b>",
		HTMLURL:     "https://example.com/release",
		PublishedAt: time.Now(),
	}

	html := renderRelease("owner/repo<script>", release, "http://example.com/unsub/token")

	if !strings.Contains(html, "New release for") {
		t.Fatalf("renderRelease output missing heading: %q", html)
	}
	if !strings.Contains(html, "v1.2.3") {
		t.Fatalf("renderRelease output missing tag: %q", html)
	}
	if !strings.Contains(html, "http://example.com/unsub/token") {
		t.Fatalf("renderRelease output missing unsubscribe URL: %q", html)
	}
	if strings.Contains(html, "<script>") {
		t.Fatalf("renderRelease output should escape repo name: %q", html)
	}
}
