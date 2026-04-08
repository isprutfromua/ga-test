package mailer

import (
	"bytes"
	"crypto/tls"
	"errors"
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
	from   string
	addr   string
	host   string
	auth   smtp.Auth
	useTLS bool
}

func New(cfg config.SMTPConfig) Mailer {
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	var auth smtp.Auth
	if cfg.Username != "" { auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host) }
	return &smtpMailer{from: cfg.From, addr: addr, host: cfg.Host, auth: auth, useTLS: cfg.UseTLS}
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

	conn, err := net.Dial("tcp", m.addr)
	if err != nil {
		return err
	}

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if m.useTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("smtp server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.host}); err != nil {
			return err
		}
	}

	if m.auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("smtp server does not support AUTH")
		}
		if err := client.Auth(m.auth); err != nil {
			return err
		}
	}

	if err := client.Mail(m.from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg.Bytes()); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	if err := client.Quit(); err != nil {
		return err
	}
	return nil
}

func render(title, body, url string) string {
	tpl := template.Must(template.New("mail").Parse(`<html><body><h1>{{.Title}}</h1><p>{{.Body}}</p><p><a href="{{.URL}}" style="display:inline-block;padding:10px 16px;background:#0b5ed7;color:#ffffff;text-decoration:none;border-radius:6px;">Confirm subscription</a></p></body></html>`))
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
