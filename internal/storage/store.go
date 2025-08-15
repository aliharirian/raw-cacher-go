package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/yourname/raw-cacher-go/internal/cache"
)

func NewStore(ctx context.Context, endpoint, access, secret, bucket string) (*Store, error) {
	secure := false
	if len(endpoint) >= 8 && endpoint[:8] == "https://" {
		secure = true
		endpoint = endpoint[8:]
	} else if len(endpoint) >= 7 && endpoint[:7] == "http://" {
		endpoint = endpoint[7:]
	}
	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, err
	}
	s := &Store{client: cl, bucket: bucket}
	exists, err := cl.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := cl.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

type Store struct {
	client *minio.Client
	bucket string
}

func (s *Store) HasObject(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" || resp.StatusCode == 404 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, map[string]string, error) {
	st, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, 0, nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, nil, err
	}
	h := map[string]string{
		"ETag":         st.ETag,
		"Content-Type": st.ContentType,
	}
	if !st.LastModified.IsZero() {
		h["Last-Modified"] = st.LastModified.UTC().Format(time.RFC1123)
	}
	return obj, st.Size, h, nil
}

func (s *Store) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	opts := minio.PutObjectOptions{}
	if contentType != "" {
		opts.ContentType = contentType
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), opts)
	return err
}

func (s *Store) ReadMeta(ctx context.Context, key string) (cache.Meta, bool, error) {
	var m cache.Meta
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.StatusCode == 404 {
			return m, false, nil
		}
		return m, false, err
	}
	defer obj.Close()
	b, err := io.ReadAll(obj)
	if err != nil {
		return m, false, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, false, nil
	}
	return m, true, nil
}

func (s *Store) WriteMeta(ctx context.Context, key string, m cache.Meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(b), int64(len(b)), minio.PutObjectOptions{
		ContentType: "application/json",
	})
	return err
}

func (s *Store) Ping(ctx context.Context) error {
	// A simple check: verify bucket exists
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %s not found", s.bucket)
	}
	return nil
}
