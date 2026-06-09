package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

type S3Store struct {
	client *s3.Client
	bucket string
}

func NewS3Store(cfg config.S3Config) (*S3Store, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("s3 endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}
	if cfg.Region == "" {
		return nil, errors.New("s3 region is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3 access key and secret key are required")
	}

	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.Endpoint)
		options.UsePathStyle = cfg.UsePathStyle
	})
	return &S3Store{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3Store) PutObject(ctx context.Context, key string, content []byte, contentType string) error {
	return s.PutObjectStream(ctx, key, bytes.NewReader(content), int64(len(content)), contentType)
}

func (s *S3Store) PutObjectStream(ctx context.Context, key string, content io.Reader, size int64, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          content,
		ContentLength: aws.Int64(size),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("s3 put object %q: %w", key, err)
	}
	return nil
}

func (s *S3Store) GetObject(ctx context.Context, key string) ([]byte, error) {
	object, err := s.OpenObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer object.Body.Close()

	content, err := io.ReadAll(object.Body)
	if err != nil {
		return nil, fmt.Errorf("read s3 object %q: %w", key, err)
	}
	return content, nil
}

func (s *S3Store) OpenObject(ctx context.Context, key string) (app.ObjectStream, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return app.ObjectStream{}, app.ErrObjectNotFound
		}
		return app.ObjectStream{}, fmt.Errorf("s3 get object %q: %w", key, err)
	}
	contentType := ""
	if out.ContentType != nil {
		contentType = *out.ContentType
	}
	size := int64(-1)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return app.ObjectStream{Body: out.Body, Size: size, ContentType: contentType}, nil
}

func (s *S3Store) DeleteObject(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		if isS3NotFound(err) {
			return app.ErrObjectNotFound
		}
		return fmt.Errorf("s3 delete object %q: %w", key, err)
	}
	return nil
}

func (s *S3Store) Health(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
		return fmt.Errorf("s3 head bucket %q: %w", s.bucket, err)
	}
	return nil
}

func isS3NotFound(err error) bool {
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound")
}
