package infra

import (
	"backend-core/internal/mail/domain"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type SMTPSender struct {
	timeout time.Duration
}

func NewSMTPSender() *SMTPSender {
	return &SMTPSender{timeout: 10 * time.Second}
}

func (s *SMTPSender) Send(ctx context.Context, settings domain.SMTPSettings, to, subject, body string) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	dialer := &net.Dialer{Timeout: s.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(s.timeout))

	host := settings.Host
	var client *smtp.Client
	if settings.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		client, err = smtp.NewClient(tlsConn, host)
	} else {
		client, err = smtp.NewClient(conn, host)
	}
	if err != nil {
		return err
	}
	defer client.Close()

	if settings.UseStartTLS && !settings.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}

	if settings.Username != "" {
		auth := smtp.PlainAuth("", settings.Username, settings.Password, host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}

	fromEmail := strings.TrimSpace(settings.FromEmail)
	if err := client.Mail(fromEmail); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, buildMessage(settings, to, subject, body)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func buildMessage(settings domain.SMTPSettings, to, subject, body string) string {
	from := mail.Address{Name: settings.FromName, Address: settings.FromEmail}
	toAddr := mail.Address{Address: to}
	headers := map[string]string{
		"From":         from.String(),
		"To":           toAddr.String(),
		"Subject":      mime.QEncoding.Encode("UTF-8", subject),
		"Date":         time.Now().Format(time.RFC1123Z),
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=UTF-8",
	}
	var b strings.Builder
	for _, key := range []string{"From", "To", "Subject", "Date", "MIME-Version", "Content-Type"} {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(headers[key])
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return b.String()
}
