package scanner

import (
	"context"
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
