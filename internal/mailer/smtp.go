package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

const smtpTimeout = 15 * time.Second

type SMTPPasswordResetSender struct {
	addr      string
	host      string
	username  string
	password  string
	from      *mail.Address
	publicURL *url.URL
}

func NewSMTPPasswordResetSender(addr, username, password, from, publicURL string) (*SMTPPasswordResetSender, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid SMTP address: %w", err)
	}
	fromAddress, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("invalid SMTP sender: %w", err)
	}
	baseURL, err := url.Parse(publicURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("SPOOL_PUBLIC_URL must be an absolute HTTPS URL")
	}
	if (username == "") != (password == "") {
		return nil, fmt.Errorf("SMTP username and password must both be set")
	}
	return &SMTPPasswordResetSender{
		addr: addr, host: host, username: username, password: password,
		from: fromAddress, publicURL: baseURL,
	}, nil
}

func (s *SMTPPasswordResetSender) SendPasswordReset(ctx context.Context, email, token string) error {
	recipient, err := mail.ParseAddress(email)
	if err != nil {
		return fmt.Errorf("invalid reset recipient: %w", err)
	}
	message := s.message(recipient.Address, token)

	dialer := net.Dialer{Timeout: smtpTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP server does not support STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *SMTPPasswordResetSender) message(recipient, token string) []byte {
	resetURL := *s.publicURL
	resetURL.Path = strings.TrimRight(resetURL.Path, "/") + "/reset"
	query := resetURL.Query()
	query.Set("token", token)
	resetURL.RawQuery = query.Encode()

	body := "Use this link to reset your Spool password:\r\n\r\n" + resetURL.String() +
		"\r\n\r\nThis link expires in one hour and can only be used once.\r\n"
	message := "From: " + s.from.String() + "\r\n" +
		"To: " + recipient + "\r\n" +
		"Subject: Reset your Spool password\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + body
	return []byte(message)
}
