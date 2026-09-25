package app

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type generatedScanBody struct {
	remaining, read int64
	maxChunk        int
}

func (r *generatedScanBody) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	clear(p[:n])
	r.remaining -= int64(n)
	r.read += int64(n)
	if n > r.maxChunk {
		r.maxChunk = n
	}
	return n, nil
}

type largeScanStore struct {
	ObjectStore
	body *generatedScanBody
	size int64
}

func (s *largeScanStore) OpenObject(context.Context, string) (ObjectStream, error) {
	return ObjectStream{Body: io.NopCloser(s.body), Size: s.size, ContentType: "application/octet-stream"}, nil
}
func (s *largeScanStore) PutObjectStream(context.Context, string, io.Reader, int64, string) error {
	return errors.New("unexpected upload")
}
func (s *largeScanStore) GetObject(context.Context, string) ([]byte, error) {
	return nil, errors.New("scan attempted to buffer entire object")
}

type streamingTestScanner struct{ size int64 }

func (s *streamingTestScanner) Scan(context.Context, string, string, []byte) (ScanResult, error) {
	return ScanResult{}, errors.New("legacy scan path used")
}
func (s *streamingTestScanner) ScanStream(_ context.Context, _ string, _ string, body io.Reader, size int64) (ScanResult, error) {
	s.size = size
	n, err := io.CopyBuffer(io.Discard, body, make([]byte, 32<<10))
	if n != size {
		return ScanResult{}, errors.New("scan skipped bytes")
	}
	return ScanResult{Status: "clean"}, err
}

func TestTwoGiBAttachmentScanKeepsReadsBounded(t *testing.T) {
	now := time.Now()
	s := newTestService(t, &now)
	owner := registerTestUser(t, s, "stream@example.com")
	paste, err := s.CreatePaste(owner.User.ID, PasteInput{Title: "large scan"})
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(2 << 30)
	body := &generatedScanBody{remaining: size}
	s.objectStore = &largeScanStore{body: body, size: size}
	s.attachmentsByID["large"] = &Attachment{ID: "large", UserID: owner.User.ID, PasteID: paste.ID, ObjectKey: "large", FileName: "large.bin", ContentType: "application/octet-stream", Status: "active", ScanStatus: "pending", Size: size}
	s.pastesByID[paste.ID].AttachmentIDs = []string{"large"}
	scanner := &streamingTestScanner{}
	if err := s.RunAttachmentScanWithContext(context.Background(), scanner, "large"); err != nil {
		t.Fatal(err)
	}
	if scanner.size != size || body.read != size || body.maxChunk > 64<<10 {
		t.Fatalf("scan was not bounded: size=%d read=%d chunk=%d", scanner.size, body.read, body.maxChunk)
	}
	if s.attachmentsByID["large"].ScanStatus != "clean" {
		t.Fatal("scan verdict not applied")
	}
}
