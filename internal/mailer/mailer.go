package mailer

import (
	"bytes"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/isprutfromua/ga-test/internal/config"
)

type Mailer interface {
	SendConfirmation(email, repo, confirmURL string) error
	SendReleaseNotificationWithUnsub(email, repo string, release ReleaseInfo, unsubURL string) error
}

type ReleaseInfo struct {
	TagName     string
	Name        string
	Body        string
	HTMLURL     string
	PublishedAt time.Time
}

type smtpMailer struct {
	from string
	addr string
	auth smtp.Auth
}

func New(cfg config.SMTPConfig) Mailer {
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	var auth smtp.Auth
	if cfg.Username != "" { auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host) }
	return &smtpMailer{from: cfg.From, addr: addr, auth: auth}
}

func (m *smtpMailer) SendConfirmation(email, repo, confirmURL string) error {
	body := render(fmt.Sprintf("Confirm your subscription to %s", repo), "Click the link below to confirm.", confirmURL)
	return m.send(email, "Confirm your GitHub release subscription", body)
}

func (m *smtpMailer) SendReleaseNotificationWithUnsub(email, repo string, release ReleaseInfo, unsubURL string) error {
	body := renderRelease(repo, release, unsubURL)
	return m.send(email, fmt.Sprintf("New release for %s", repo), body)
}

func (m *smtpMailer) send(to, subject, htmlBody string) error {
	msg := bytes.NewBuffer(nil)
	fmt.Fprintf(msg, "From: %s\r\n", m.from)
	fmt.Fprintf(msg, "To: %s\r\n", to)
	fmt.Fprintf(msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(msg, "Content-Type: text/html; charset=UTF-8\r\n\r\n")
	msg.WriteString(htmlBody)
	return smtp.SendMail(m.addr, m.auth, m.from, []string{to}, msg.Bytes())
}

func render(title, body, url string) string {
	tpl := template.Must(template.New("mail").Parse(`<html><body><h1>{{.Title}}</h1><p>{{.Body}}</p><p><a href="{{.URL}}">{{.URL}}</a></p></body></html>`))
	var buf bytes.Buffer
	_ = tpl.Execute(&buf, map[string]string{"Title": title, "Body": body, "URL": url})
	return buf.String()
}

func renderRelease(repo string, release ReleaseInfo, unsubURL string) string {
	var b strings.Builder
	b.WriteString("<html><body><h1>New release for ")
	b.WriteString(template.HTMLEscapeString(repo))
	b.WriteString("</h1><p>")
	b.WriteString(template.HTMLEscapeString(release.TagName))
	b.WriteString("</p><p><a href=\"")
	b.WriteString(template.HTMLEscapeString(unsubURL))
	b.WriteString("\">Unsubscribe</a></p></body></html>")
	return b.String()
}
