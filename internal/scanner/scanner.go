package scanner

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

type Verdict string

const (
	VerdictClean     Verdict = "clean"
	VerdictMalicious Verdict = "malicious"
	VerdictFailed    Verdict = "scan_failed"
)

type Scanner interface {
	Scan(ctx context.Context, fileName string, contentType string, content []byte) (app.ScanResult, error)
}

func New(cfg config.ScannerConfig) (Scanner, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "heuristic":
		return Heuristic{}, nil
	case "clamav":
		timeout := time.Duration(cfg.ClamAV.Timeout) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		addr := strings.TrimSpace(cfg.ClamAV.Addr)
		if addr == "" {
			return nil, errors.New("PASTEBOX_CLAMAV_ADDR is required when PASTEBOX_SCANNER_PROVIDER=clamav")
		}
		return ClamAV{Addr: addr, Timeout: timeout}, nil
	default:
		return nil, fmt.Errorf("unsupported scanner provider %q", cfg.Provider)
	}
}

type Heuristic struct{}

func (Heuristic) Scan(_ context.Context, fileName string, contentType string, _ []byte) (app.ScanResult, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".exe", ".bat", ".cmd", ".scr", ".msi":
		return app.ScanResult{Status: string(VerdictMalicious), Risk: "executable_file"}, nil
	}
	if strings.Contains(strings.ToLower(contentType), "html") || strings.Contains(strings.ToLower(contentType), "svg") {
		return app.ScanResult{Status: string(VerdictClean), Risk: "render_as_download_only"}, nil
	}
	return app.ScanResult{Status: string(VerdictClean)}, nil
}

type ClamAV struct {
	Addr    string
	Timeout time.Duration
}

func (c ClamAV) Scan(ctx context.Context, fileName string, contentType string, content []byte) (app.ScanResult, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return app.ScanResult{}, fmt.Errorf("connect clamav: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	if _, err := io.WriteString(conn, "zINSTREAM\x00"); err != nil {
		return app.ScanResult{}, fmt.Errorf("start clamav stream: %w", err)
	}
	reader := bytes.NewReader(content)
	var size [4]byte
	buf := make([]byte, 128*1024)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(size[:], uint32(n))
			if _, err := conn.Write(size[:]); err != nil {
				return app.ScanResult{}, fmt.Errorf("write clamav chunk size: %w", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return app.ScanResult{}, fmt.Errorf("write clamav chunk: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return app.ScanResult{}, fmt.Errorf("read scan content: %w", readErr)
		}
	}
	binary.BigEndian.PutUint32(size[:], 0)
	if _, err := conn.Write(size[:]); err != nil {
		return app.ScanResult{}, fmt.Errorf("finish clamav stream: %w", err)
	}
	response, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		return app.ScanResult{}, fmt.Errorf("read clamav response: %w", err)
	}
	return parseClamAVResponse(string(response), fileName, contentType)
}

func parseClamAVResponse(response string, fileName string, contentType string) (app.ScanResult, error) {
	trimmed := strings.TrimSpace(response)
	if strings.Contains(trimmed, " FOUND") {
		risk := "malware_detected"
		if parts := strings.Split(trimmed, ":"); len(parts) > 1 {
			detail := strings.TrimSpace(strings.TrimSuffix(parts[len(parts)-1], "FOUND"))
			if detail != "" {
				risk = sanitizeRisk(detail)
			}
		}
		return app.ScanResult{Status: string(VerdictMalicious), Risk: risk}, nil
	}
	if strings.Contains(trimmed, " OK") || strings.HasSuffix(trimmed, "OK") {
		return Heuristic{}.Scan(context.Background(), fileName, contentType, nil)
	}
	return app.ScanResult{}, fmt.Errorf("unexpected clamav response %q", trimmed)
}

func sanitizeRisk(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, value)
	value = strings.Trim(value, "_")
	if value == "" {
		return "malware_detected"
	}
	return value
}
