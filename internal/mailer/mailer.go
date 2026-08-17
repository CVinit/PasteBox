package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"pastebox/internal/config"
)

type Sender interface {
	Send(ctx context.Context, to string, subject string, body string) error
}

type DynamicSender struct {
	mu     sync.RWMutex
	sender Sender
	logger *slog.Logger
}

func NewDynamicSender(logger *slog.Logger) *DynamicSender {
	return &DynamicSender{sender: LogSender{Logger: logger}, logger: logger}
}

func (s *DynamicSender) Update(cfg config.Config) error {
	next, err := NewSender(cfg, s.logger)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sender = next
	s.mu.Unlock()
	return nil
}

func (s *DynamicSender) Send(ctx context.Context, to string, subject string, body string) error {
	s.mu.RLock()
	sender := s.sender
	s.mu.RUnlock()
	if sender == nil {
		return fmt.Errorf("mailer is not configured")
	}
	return sender.Send(ctx, to, subject, body)
}

type LogSender struct {
	Logger *slog.Logger
}

func NewSender(cfg config.Config, logger *slog.Logger) (Sender, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.MailerProvider)) {
	case "", "log":
		return LogSender{Logger: logger}, nil
	case "smtp":
		return NewSMTPSender(cfg.SMTP)
	default:
		return nil, fmt.Errorf("unsupported mailer provider %q", cfg.MailerProvider)
	}
}

func (s LogSender) Send(ctx context.Context, _ string, subject string, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("mail delivery logged", "subject", subject)
	return nil
}

type SMTPSender struct {
	cfg config.SMTPConfig
}

func NewSMTPSender(cfg config.SMTPConfig) (*SMTPSender, error) {
	cfg.TLSMode = strings.ToLower(strings.TrimSpace(cfg.TLSMode))
	if cfg.TLSMode == "" {
		cfg.TLSMode = "starttls"
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("smtp host is required")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp port is invalid")
	}
	if _, err := mail.ParseAddress(cfg.FromEmail); err != nil {
		return nil, fmt.Errorf("smtp from email is invalid: %w", err)
	}
	switch cfg.TLSMode {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("smtp tls mode must be starttls, tls, or none")
	}
	return &SMTPSender{cfg: cfg}, nil
}

func (s *SMTPSender) Send(ctx context.Context, to string, subject string, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message, from, recipient, err := buildMessage(s.cfg, to, subject, body)
	if err != nil {
		return err
	}
	client, err := s.smtpClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if strings.TrimSpace(s.cfg.Username) != "" || strings.TrimSpace(s.cfg.Password) != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func (s *SMTPSender) smtpClient(ctx context.Context) (*smtp.Client, error) {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	var conn net.Conn
	var err error
	switch s.cfg.TLSMode {
	case "tls":
		conn, err = (&tls.Dialer{NetDialer: &net.Dialer{}, Config: tlsConfig(s.cfg.Host)}).DialContext(ctx, "tcp", address)
	default:
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("connect smtp: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create smtp client: %w", err)
	}
	if s.cfg.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			return nil, fmt.Errorf("smtp server does not advertise STARTTLS")
		}
		if err := client.StartTLS(tlsConfig(s.cfg.Host)); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("smtp starttls: %w", err)
		}
	}
	return client, nil
}

func tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func buildMessage(cfg config.SMTPConfig, to string, subject string, body string) ([]byte, mail.Address, mail.Address, error) {
	if hasHeaderBreak(subject) {
		return nil, mail.Address{}, mail.Address{}, fmt.Errorf("smtp subject must not contain line breaks")
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return nil, mail.Address{}, mail.Address{}, fmt.Errorf("smtp recipient is invalid: %w", err)
	}
	from, err := mail.ParseAddress(cfg.FromEmail)
	if err != nil {
		return nil, mail.Address{}, mail.Address{}, fmt.Errorf("smtp from email is invalid: %w", err)
	}
	if strings.TrimSpace(cfg.FromName) != "" {
		from.Name = strings.TrimSpace(cfg.FromName)
	}
	var builder strings.Builder
	writeHeader(&builder, "From", from.String())
	writeHeader(&builder, "To", recipient.String())
	writeHeader(&builder, "Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader(&builder, "MIME-Version", "1.0")
	writeHeader(&builder, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&builder, "Content-Transfer-Encoding", "8bit")
	builder.WriteString("\r\n")
	builder.WriteString(crlfBody(body))
	if !strings.HasSuffix(builder.String(), "\r\n") {
		builder.WriteString("\r\n")
	}
	return []byte(builder.String()), *from, *recipient, nil
}

func writeHeader(w io.StringWriter, key string, value string) {
	_, _ = w.WriteString(key)
	_, _ = w.WriteString(": ")
	_, _ = w.WriteString(value)
	_, _ = w.WriteString("\r\n")
}

func hasHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func crlfBody(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.ReplaceAll(normalized, "\n", "\r\n")
}
