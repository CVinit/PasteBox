package objectstore

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

var ErrNotConfigured = errors.New("object storage is not configured")

type DynamicS3Store struct {
	mu    sync.RWMutex
	store *S3Store
}

func NewDynamicS3Store() *DynamicS3Store {
	return &DynamicS3Store{}
}

func (s *DynamicS3Store) Update(cfg config.S3Config) error {
	if s3ConfigIncomplete(cfg) {
		s.mu.Lock()
		s.store = nil
		s.mu.Unlock()
		return nil
	}
	next, err := NewS3Store(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.store = next
	s.mu.Unlock()
	return nil
}

func (s *DynamicS3Store) current() (*S3Store, error) {
	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()
	if store == nil {
		return nil, ErrNotConfigured
	}
	return store, nil
}

func (s *DynamicS3Store) PutObject(ctx context.Context, key string, content []byte, contentType string) error {
	store, err := s.current()
	if err != nil {
		return err
	}
	return store.PutObject(ctx, key, content, contentType)
}

func (s *DynamicS3Store) PutObjectStream(ctx context.Context, key string, content io.Reader, size int64, contentType string) error {
	store, err := s.current()
	if err != nil {
		return err
	}
	return store.PutObjectStream(ctx, key, content, size, contentType)
}

func (s *DynamicS3Store) GetObject(ctx context.Context, key string) ([]byte, error) {
	store, err := s.current()
	if err != nil {
		return nil, err
	}
	return store.GetObject(ctx, key)
}

func (s *DynamicS3Store) OpenObject(ctx context.Context, key string) (app.ObjectStream, error) {
	store, err := s.current()
	if err != nil {
		return app.ObjectStream{}, err
	}
	return store.OpenObject(ctx, key)
}

func (s *DynamicS3Store) DeleteObject(ctx context.Context, key string) error {
	store, err := s.current()
	if err != nil {
		return err
	}
	return store.DeleteObject(ctx, key)
}

func (s *DynamicS3Store) Health(ctx context.Context) error {
	store, err := s.current()
	if err != nil {
		return err
	}
	return store.Health(ctx)
}

func s3ConfigIncomplete(cfg config.S3Config) bool {
	values := []string{cfg.Endpoint, cfg.Bucket, cfg.Region, cfg.AccessKey, cfg.SecretKey}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
