package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Storage struct {
	internalClient *minio.Client
	publicClient   *minio.Client
	bucket         string
	region         string
}

type Config struct {
	Endpoint       string
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	Bucket         string
	Region         string
	InternalUseSSL bool
	PublicUseSSL   bool
}

func New(config Config) (*Storage, error) {
	internalOptions := &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.InternalUseSSL,
		Region: config.Region,
	}
	internalClient, err := minio.New(config.Endpoint, internalOptions)
	if err != nil {
		return nil, fmt.Errorf("create internal object storage client: %w", err)
	}
	publicOptions := &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.PublicUseSSL,
		Region: config.Region,
	}
	publicClient, err := minio.New(config.PublicEndpoint, publicOptions)
	if err != nil {
		return nil, fmt.Errorf("create public object storage client: %w", err)
	}
	return &Storage{
		internalClient: internalClient,
		publicClient:   publicClient,
		bucket:         config.Bucket,
		region:         config.Region,
	}, nil
}

func (s *Storage) EnsureBucket(ctx context.Context) error {
	exists, err := s.internalClient.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check media bucket: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.internalClient.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{
		Region: s.region,
	}); err != nil {
		return fmt.Errorf("create media bucket: %w", err)
	}
	return nil
}

func (s *Storage) NewObjectKey(userID, contentType string, now time.Time) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate media key: %w", err)
	}
	extension := ".bin"
	switch contentType {
	case "video/mp4":
		extension = ".mp4"
	case "video/quicktime":
		extension = ".mov"
	case "video/webm":
		extension = ".webm"
	}
	return path.Join(
		"originals",
		userID,
		now.UTC().Format("2006/01"),
		hex.EncodeToString(random)+extension,
	), nil
}

func (s *Storage) PresignedUploadURL(
	ctx context.Context,
	objectKey string,
	contentType string,
	expires time.Duration,
) (string, http.Header, error) {
	headers := http.Header{}
	headers.Set("Content-Type", contentType)
	signed, err := s.publicClient.PresignHeader(
		ctx,
		http.MethodPut,
		s.bucket,
		objectKey,
		expires,
		url.Values{},
		headers,
	)
	if err != nil {
		return "", nil, fmt.Errorf("presign media upload: %w", err)
	}
	return signed.String(), headers, nil
}

func (s *Storage) Stat(ctx context.Context, objectKey string) (minio.ObjectInfo, error) {
	info, err := s.internalClient.StatObject(
		ctx,
		s.bucket,
		objectKey,
		minio.StatObjectOptions{},
	)
	if err != nil {
		return minio.ObjectInfo{}, fmt.Errorf("stat media object: %w", err)
	}
	return info, nil
}

func (s *Storage) PresignedDownloadURL(
	ctx context.Context,
	objectKey string,
	expires time.Duration,
) (string, error) {
	signed, err := s.publicClient.PresignedGetObject(
		ctx,
		s.bucket,
		objectKey,
		expires,
		url.Values{},
	)
	if err != nil {
		return "", fmt.Errorf("presign media download: %w", err)
	}
	return signed.String(), nil
}
