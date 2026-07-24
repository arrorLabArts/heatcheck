package config

import "testing"

func TestLoadProductionWithEmptyCORS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://heatcheck:secret@postgres:5432/heatcheck")
	t.Setenv("JWT_SECRET", "a-secret-that-is-at-least-thirty-two-bytes")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("BOOTSTRAP_ADMIN_EMAILS", "")
	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_PUBLIC_ENDPOINT", "storage.heatcheck.dogi.watch")
	t.Setenv("S3_ACCESS_KEY", "heatcheck-api")
	t.Setenv("S3_SECRET_KEY", "application-storage-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("AllowedOrigins = %v, want no production browser origins", cfg.AllowedOrigins)
	}
}

func TestLoadRequiresStorageCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://heatcheck:secret@postgres:5432/heatcheck")
	t.Setenv("JWT_SECRET", "a-secret-that-is-at-least-thirty-two-bytes")
	t.Setenv("S3_ACCESS_KEY", "")
	t.Setenv("S3_SECRET_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want missing storage credentials error")
	}
}
