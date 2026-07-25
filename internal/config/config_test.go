package config

import "testing"

func TestLoadProductionWithEmptyCORS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://heatcheck:secret@postgres:5432/heatcheck")
	t.Setenv("JWT_SECRET", "a-secret-that-is-at-least-thirty-two-bytes")
	t.Setenv("DATA_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("SAFETY_ALERT_EMAIL", "safety@example.test")
	t.Setenv("LEGAL_ALERT_EMAIL", "legal@example.test")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("BOOTSTRAP_ADMIN_EMAILS", "")
	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_PUBLIC_ENDPOINT", "storage.heatcheck.dogi.watch")
	t.Setenv("S3_ACCESS_KEY", "heatcheck-api")
	t.Setenv("S3_SECRET_KEY", "application-storage-secret")
	setRevenueCatEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("AllowedOrigins = %v, want no production browser origins", cfg.AllowedOrigins)
	}
}

func setRevenueCatEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("REVENUECAT_SECRET_API_KEY", "sk_test_secret")
	t.Setenv("REVENUECAT_APP_ID", "app_test")
	t.Setenv("REVENUECAT_WEBHOOK_AUTHORIZATION", "Bearer a-webhook-authorization-value-that-is-long")
	t.Setenv("REVENUECAT_WEBHOOK_SIGNING_SECRET", "a-signing-secret-that-is-at-least-32-bytes")
	t.Setenv("REVENUECAT_ALLOW_SANDBOX", "false")
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

func TestLoadRejectsInsecureProductionPublicURL(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_APP_URL", "http://heatcheck.example.test")
	t.Setenv("DATABASE_URL", "postgres://heatcheck:secret@postgres:5432/heatcheck")
	t.Setenv("JWT_SECRET", "a-secret-that-is-at-least-thirty-two-bytes")
	t.Setenv("DATA_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("SAFETY_ALERT_EMAIL", "safety@example.test")
	t.Setenv("LEGAL_ALERT_EMAIL", "legal@example.test")
	t.Setenv("S3_ACCESS_KEY", "heatcheck-api")
	t.Setenv("S3_SECRET_KEY", "application-storage-secret")
	setRevenueCatEnvironment(t)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want insecure public URL error")
	}
}
