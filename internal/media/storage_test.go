package media

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPresignedURLs(t *testing.T) {
	storage, err := New(Config{
		Endpoint:       "minio:9000",
		PublicEndpoint: "localhost:9000",
		AccessKey:      "access-key",
		SecretKey:      "a-local-secret-key",
		Bucket:         "clips",
		Region:         "us-east-1",
		InternalUseSSL: false,
		PublicUseSSL:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	objectKey, err := storage.NewObjectKey(
		"user-id",
		"video/mp4",
		time.Date(2026, time.July, 24, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(objectKey, "originals/user-id/2026/07/") ||
		!strings.HasSuffix(objectKey, ".mp4") {
		t.Fatalf("unexpected object key %q", objectKey)
	}

	uploadURL, headers, err := storage.PresignedUploadURL(
		context.Background(),
		objectKey,
		"video/mp4",
		15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "localhost:9000" {
		t.Fatalf("got public host %q", parsed.Host)
	}
	if parsed.Scheme != "https" {
		t.Fatalf("got public scheme %q", parsed.Scheme)
	}
	if headers.Get("Content-Type") != "video/mp4" {
		t.Fatalf("unexpected signed headers: %#v", headers)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("expected an AWS signature")
	}
}
