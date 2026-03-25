package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"agent/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinioClient wraps minio client for uploading files.
type MinioClient struct {
	client *minio.Client
	bucket string
}

// NewMinioClient creates a new MinIO client from config.
func NewMinioClient(cfg config.MinioConfig) (*MinioClient, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("minio endpoint not configured")
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	// Ensure bucket exists
	ctx := context.Background()
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket: %w", err)
		}
		slog.Info("minio bucket created", "bucket", cfg.Bucket)
	}

	return &MinioClient{client: client, bucket: cfg.Bucket}, nil
}

// UploadFile uploads a single file to MinIO.
func (m *MinioClient) UploadFile(ctx context.Context, localPath, remotePath string) error {
	_, err := m.client.FPutObject(ctx, m.bucket, remotePath, localPath, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("upload %s: %w", localPath, err)
	}
	slog.Info("uploaded to minio", "local", localPath, "remote", remotePath, "bucket", m.bucket)
	return nil
}

// UploadParquetFiles uploads all parquet files from a directory to MinIO.
// remotePath is the prefix. Files are named as remotePath/filename.
func (m *MinioClient) UploadParquetFiles(ctx context.Context, dir, remotePath string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var uploaded []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Recurse into subdirectories (e.g. realtime/)
			subDir := filepath.Join(dir, entry.Name())
			subRemote := remotePath + "/" + entry.Name()
			subUploaded, err := m.UploadParquetFiles(ctx, subDir, subRemote)
			if err != nil {
				return uploaded, err
			}
			uploaded = append(uploaded, subUploaded...)
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".parquet") {
			continue
		}

		localPath := filepath.Join(dir, entry.Name())
		remote := remotePath + "/" + entry.Name()
		if err := m.UploadFile(ctx, localPath, remote); err != nil {
			return uploaded, err
		}
		uploaded = append(uploaded, remote)
	}
	return uploaded, nil
}

// UploadBenchmarkResult uploads benchmark result as a JSON file to MinIO.
func (m *MinioClient) UploadResultJSON(ctx context.Context, data []byte, remotePath string) error {
	reader := strings.NewReader(string(data))
	_, err := m.client.PutObject(ctx, m.bucket, remotePath, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/json",
	})
	if err != nil {
		return fmt.Errorf("upload result: %w", err)
	}
	slog.Info("uploaded result to minio", "remote", remotePath, "bucket", m.bucket)
	return nil
}
