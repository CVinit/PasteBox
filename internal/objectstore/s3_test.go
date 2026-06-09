package objectstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

func TestNewS3StoreValidatesConfig(t *testing.T) {
	valid := config.S3Config{
		Endpoint:     "https://objects.example.com",
		Bucket:       "pastebox",
		Region:       "us-east-1",
		AccessKey:    "access",
		SecretKey:    "secret",
		UsePathStyle: true,
	}
	for _, tt := range []struct {
		name   string
		mutate func(*config.S3Config)
		want   string
	}{
		{name: "endpoint", mutate: func(cfg *config.S3Config) { cfg.Endpoint = "" }, want: "s3 endpoint is required"},
		{name: "bucket", mutate: func(cfg *config.S3Config) { cfg.Bucket = "" }, want: "s3 bucket is required"},
		{name: "region", mutate: func(cfg *config.S3Config) { cfg.Region = "" }, want: "s3 region is required"},
		{name: "credentials", mutate: func(cfg *config.S3Config) { cfg.AccessKey = "" }, want: "s3 access key and secret key are required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			_, err := NewS3Store(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestS3StoreRoundTripHealthAndNotFoundMapping(t *testing.T) {
	fake := newFakeS3Server(t, "pastebox")
	store, err := NewS3Store(config.S3Config{
		Endpoint:     fake.URL,
		Bucket:       "pastebox",
		Region:       "us-east-1",
		AccessKey:    "access",
		SecretKey:    "secret",
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("new s3 store: %v", err)
	}

	ctx := context.Background()
	if err := store.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if err := store.PutObjectStream(ctx, "objects/paste.txt", strings.NewReader("stored payload"), int64(len("stored payload")), "text/plain"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	if got := fake.contentType("objects/paste.txt"); got != "text/plain" {
		t.Fatalf("expected content type to be forwarded, got %q", got)
	}
	object, err := store.OpenObject(ctx, "objects/paste.txt")
	if err != nil {
		t.Fatalf("open object: %v", err)
	}
	content, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if string(content) != "stored payload" {
		t.Fatalf("expected stored payload, got %q", string(content))
	}
	content, err = store.GetObject(ctx, "objects/paste.txt")
	if err != nil {
		t.Fatalf("legacy get object: %v", err)
	}
	if string(content) != "stored payload" {
		t.Fatalf("expected stored payload from legacy get, got %q", string(content))
	}
	if err := store.DeleteObject(ctx, "objects/paste.txt"); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if _, err := store.GetObject(ctx, "objects/paste.txt"); !errors.Is(err, app.ErrObjectNotFound) {
		t.Fatalf("expected missing object to map to ErrObjectNotFound, got %v", err)
	}
	if err := store.DeleteObject(ctx, "objects/missing.txt"); !errors.Is(err, app.ErrObjectNotFound) {
		t.Fatalf("expected missing delete to map to ErrObjectNotFound, got %v", err)
	}
}

func TestS3StoreExternalEndpoint(t *testing.T) {
	endpoint := os.Getenv("PASTEBOX_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PASTEBOX_TEST_S3_ENDPOINT to run external S3-compatible endpoint test")
	}
	bucket := os.Getenv("PASTEBOX_TEST_S3_BUCKET")
	accessKey := os.Getenv("PASTEBOX_TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("PASTEBOX_TEST_S3_SECRET_KEY")
	if bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("set PASTEBOX_TEST_S3_BUCKET, PASTEBOX_TEST_S3_ACCESS_KEY, and PASTEBOX_TEST_S3_SECRET_KEY")
	}
	region := os.Getenv("PASTEBOX_TEST_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	store, err := NewS3Store(config.S3Config{
		Endpoint:     endpoint,
		Bucket:       bucket,
		Region:       region,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("new s3 store: %v", err)
	}

	ctx := context.Background()
	if err := store.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	key := "pastebox-external-test/" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().UTC().Format("20060102150405.000000000")
	payload := "streamed payload through external s3 endpoint"
	if err := store.PutObjectStream(ctx, key, strings.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("put object stream: %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteObject(context.Background(), key)
	})
	object, err := store.OpenObject(ctx, key)
	if err != nil {
		t.Fatalf("open object: %v", err)
	}
	content, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if string(content) != payload {
		t.Fatalf("expected %q, got %q", payload, string(content))
	}
	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if _, err := store.OpenObject(ctx, key); !errors.Is(err, app.ErrObjectNotFound) {
		t.Fatalf("expected missing object to map to ErrObjectNotFound, got %v", err)
	}
}

type fakeS3Server struct {
	*httptest.Server
	bucket       string
	mu           sync.Mutex
	objects      map[string][]byte
	contentTypes map[string]string
}

func newFakeS3Server(t *testing.T, bucket string) *fakeS3Server {
	t.Helper()
	fake := &fakeS3Server{
		bucket:       bucket,
		objects:      map[string][]byte{},
		contentTypes: map[string]string{},
	}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.Close)
	return fake
}

func (s *fakeS3Server) contentType(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contentTypes[key]
}

func (s *fakeS3Server) handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	if path == s.bucket && r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	prefix := s.bucket + "/"
	if !strings.HasPrefix(path, prefix) {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	key := strings.TrimPrefix(path, prefix)
	switch r.Method {
	case http.MethodPut:
		content, err := io.ReadAll(r.Body)
		if err != nil {
			writeS3Error(w, http.StatusInternalServerError, "InternalError")
			return
		}
		s.mu.Lock()
		s.objects[key] = append([]byte(nil), content...)
		s.contentTypes[key] = r.Header.Get("Content-Type")
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		s.mu.Lock()
		content, ok := s.objects[key]
		contentType := s.contentTypes[key]
		s.mu.Unlock()
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(content)
	case http.MethodDelete:
		s.mu.Lock()
		_, ok := s.objects[key]
		delete(s.objects, key)
		delete(s.contentTypes, key)
		s.mu.Unlock()
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func writeS3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<Error><Code>`+code+`</Code><Message>`+code+`</Message></Error>`)
}
