package scanner

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"pastebox/internal/config"
)

func TestNewRejectsUnsupportedProvider(t *testing.T) {
	_, err := New(config.ScannerConfig{Provider: "unsupported"})
	if err == nil || !strings.Contains(err.Error(), "unsupported scanner provider") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestNewRequiresClamAVAddress(t *testing.T) {
	_, err := New(config.ScannerConfig{Provider: "clamav"})
	if err == nil || !strings.Contains(err.Error(), "PASTEBOX_CLAMAV_ADDR") {
		t.Fatalf("expected missing ClamAV address error, got %v", err)
	}
}

func TestNewDefaultsClamAVTimeout(t *testing.T) {
	scan, err := New(config.ScannerConfig{
		Provider: "clamav",
		ClamAV:   config.ClamAVConfig{Addr: "clamav:3310"},
	})
	if err != nil {
		t.Fatalf("new scanner: %v", err)
	}
	clam, ok := scan.(ClamAV)
	if !ok {
		t.Fatalf("expected ClamAV scanner, got %T", scan)
	}
	if clam.Timeout != 30*time.Second {
		t.Fatalf("expected default timeout, got %s", clam.Timeout)
	}
}

func TestHeuristicScannerClassifiesExecutableAsMalicious(t *testing.T) {
	result, err := Heuristic{}.Scan(context.Background(), "payload.exe", "application/octet-stream", nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Status != string(VerdictMalicious) || result.Risk != "executable_file" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClamAVScannerStreamsContentAndParsesResponses(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		fileName   string
		content    []byte
		wantStatus Verdict
		wantRisk   string
	}{
		{
			name:       "clean",
			response:   "stream: OK\n",
			fileName:   "safe.txt",
			content:    []byte("plain text payload"),
			wantStatus: VerdictClean,
		},
		{
			name:       "malicious",
			response:   "stream: Eicar-Test-Signature FOUND\n",
			fileName:   "eicar.txt",
			content:    []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR"),
			wantStatus: VerdictMalicious,
			wantRisk:   "eicar_test_signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, captured := startFakeClamAV(t, tt.response)
			result, err := ClamAV{Addr: addr, Timeout: time.Second}.Scan(context.Background(), tt.fileName, "text/plain", tt.content)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if result.Status != string(tt.wantStatus) || result.Risk != tt.wantRisk {
				t.Fatalf("unexpected scan result: %#v", result)
			}
			select {
			case got := <-captured:
				if got.err != nil {
					t.Fatalf("fake clamav read stream: %v", got.err)
				}
				if !bytes.Equal(got.content, tt.content) {
					t.Fatalf("expected streamed content %q, got %q", string(tt.content), string(got.content))
				}
			case <-time.After(time.Second):
				t.Fatal("fake clamav did not receive scanner stream")
			}
		})
	}
}

func TestParseClamAVResponse(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		fileName    string
		contentType string
		wantStatus  Verdict
		wantRisk    string
		wantErr     bool
	}{
		{
			name:       "found",
			response:   "stream: Eicar-Test-Signature FOUND\n",
			wantStatus: VerdictMalicious,
			wantRisk:   "eicar_test_signature",
		},
		{
			name:        "ok with heuristic risk",
			response:    "stream: OK\n",
			fileName:    "doc.svg",
			contentType: "image/svg+xml",
			wantStatus:  VerdictClean,
			wantRisk:    "render_as_download_only",
		},
		{
			name:     "unexpected",
			response: "stream: UNKNOWN\n",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseClamAVResponse(tt.response, tt.fileName, tt.contentType)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result %#v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse response: %v", err)
			}
			if result.Status != string(tt.wantStatus) || result.Risk != tt.wantRisk {
				t.Fatalf("unexpected result: %#v", result)
			}
		})
	}
}

type fakeClamAVCapture struct {
	content []byte
	err     error
}

func startFakeClamAV(t *testing.T, response string) (string, <-chan fakeClamAVCapture) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start fake clamav: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	captured := make(chan fakeClamAVCapture, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			captured <- fakeClamAVCapture{err: err}
			return
		}
		defer conn.Close()

		content, err := readClamAVStream(conn)
		if err != nil {
			captured <- fakeClamAVCapture{err: err}
			return
		}
		if _, err := io.WriteString(conn, response); err != nil {
			captured <- fakeClamAVCapture{content: content, err: err}
			return
		}
		captured <- fakeClamAVCapture{content: content}
	}()
	return listener.Addr().String(), captured
}

func readClamAVStream(reader io.Reader) ([]byte, error) {
	command := make([]byte, len("zINSTREAM\x00"))
	if _, err := io.ReadFull(reader, command); err != nil {
		return nil, fmt.Errorf("read command: %w", err)
	}
	if string(command) != "zINSTREAM\x00" {
		return nil, fmt.Errorf("unexpected command %q", string(command))
	}

	var content bytes.Buffer
	var size [4]byte
	for {
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return nil, fmt.Errorf("read chunk size: %w", err)
		}
		chunkSize := binary.BigEndian.Uint32(size[:])
		if chunkSize == 0 {
			break
		}
		if _, err := io.CopyN(&content, reader, int64(chunkSize)); err != nil {
			return nil, fmt.Errorf("read chunk: %w", err)
		}
	}
	return content.Bytes(), nil
}
