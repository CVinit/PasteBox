package mailer

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"pastebox/internal/config"
)

func TestDynamicSMTPDeliveryAndInvalidReload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	delivered := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		peer := textproto.NewConn(conn)
		if err := peer.PrintfLine("220 fixture ESMTP"); err != nil {
			done <- err
			return
		}
		for {
			line, err := peer.ReadLine()
			if err != nil {
				done <- err
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				err = peer.PrintfLine("250 OK")
			case line == "DATA":
				if err = peer.PrintfLine("354 send message"); err == nil {
					var body []byte
					body, err = io.ReadAll(peer.DotReader())
					if err == nil {
						delivered <- string(body)
						err = peer.PrintfLine("250 accepted")
					}
				}
			case line == "QUIT":
				done <- peer.PrintfLine("221 bye")
				return
			default:
				err = peer.PrintfLine("500 unsupported")
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	portNum, _ := strconv.Atoi(port)
	dynamic := NewDynamicSender(slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := config.Config{MailerProvider: "smtp", SMTP: config.SMTPConfig{Host: host, Port: portNum, TLSMode: "none", FromEmail: "sender@example.test"}}
	if err := dynamic.Update(cfg); err != nil {
		t.Fatal(err)
	}
	if err := dynamic.Update(config.Config{MailerProvider: "invalid"}); err == nil {
		t.Fatal("invalid update succeeded")
	}
	if err := dynamic.Send(context.Background(), "receiver@example.test", "Local delivery", "test message"); err != nil {
		t.Fatal(err)
	}
	if body := <-delivered; !strings.Contains(body, "test message") || !strings.Contains(body, "receiver@example.test") {
		t.Fatalf("message was not delivered: %q", body)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := dynamic.Update(config.Config{MailerProvider: "log"}); err != nil {
		t.Fatal(err)
	}
	if err := dynamic.Send(context.Background(), "receiver@example.test", "local", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestSMTPCancelInterruptsServerGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	portNum, _ := strconv.Atoi(port)
	sender, err := NewSMTPSender(config.SMTPConfig{Host: host, Port: portNum, TLSMode: "none", FromEmail: "sender@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sender.Send(ctx, "receiver@example.test", "cancel", "message") }()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("smtp not connected")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled send succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop SMTP")
	}
	<-serverDone
}
