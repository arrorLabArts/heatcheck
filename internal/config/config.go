package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment       string
	Port              string
	DatabaseURL       string
	JWTSecret         string
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration
	AllowedOrigins    []string
	BootstrapAdmins   map[string]struct{}
	MaxUploadBytes    int64
	MinimumAge        int
	S3Endpoint        string
	S3PublicEndpoint  string
	S3AccessKey       string
	S3SecretKey       string
	S3Bucket          string
	S3Region          string
	S3InternalUseSSL  bool
	S3PublicUseSSL    bool
	UploadURLTTL      time.Duration
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
}

func Load() (Config, error) {
	environment := strings.ToLower(get("APP_ENV", "development"))
	allowedOrigins := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if allowedOrigins == "" && environment != "production" {
		allowedOrigins = "http://localhost:3000"
	}
	cfg := Config{
		Environment:       environment,
		Port:              get("PORT", "8080"),
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		AllowedOrigins:    splitCSV(allowedOrigins),
		BootstrapAdmins:   stringSet(splitCSV(os.Getenv("BOOTSTRAP_ADMIN_EMAILS"))),
		S3Endpoint:        get("S3_ENDPOINT", "localhost:9000"),
		S3PublicEndpoint:  get("S3_PUBLIC_ENDPOINT", "localhost:9000"),
		S3AccessKey:       strings.TrimSpace(os.Getenv("S3_ACCESS_KEY")),
		S3SecretKey:       os.Getenv("S3_SECRET_KEY"),
		S3Bucket:          get("S3_BUCKET", "heatcheck-clips"),
		S3Region:          get("S3_REGION", "us-east-1"),
		ShutdownTimeout:   10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var err error
	if cfg.AccessTokenTTL, err = duration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RefreshTokenTTL, err = duration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.UploadURLTTL, err = duration("UPLOAD_URL_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.MaxUploadBytes, err = int64Value("MAX_UPLOAD_BYTES", 100*1024*1024); err != nil {
		return Config{}, err
	}
	minimumAge, err := int64Value("MINIMUM_AGE", 18)
	if err != nil {
		return Config{}, err
	}
	if minimumAge < 13 || minimumAge > 21 {
		return Config{}, errors.New("MINIMUM_AGE must be between 13 and 21")
	}
	cfg.MinimumAge = int(minimumAge)
	if cfg.S3InternalUseSSL, err = boolValue("S3_INTERNAL_USE_SSL", false); err != nil {
		return Config{}, err
	}
	if cfg.S3PublicUseSSL, err = boolValue("S3_PUBLIC_USE_SSL", false); err != nil {
		return Config{}, err
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 characters")
	}
	if cfg.S3Endpoint == "" || cfg.S3PublicEndpoint == "" || cfg.S3Bucket == "" {
		return Config{}, errors.New("S3_ENDPOINT, S3_PUBLIC_ENDPOINT, and S3_BUCKET are required")
	}
	if cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		return Config{}, errors.New("S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	if cfg.Environment == "production" && len(cfg.BootstrapAdmins) > 0 {
		return Config{}, errors.New("BOOTSTRAP_ADMIN_EMAILS must not be used in production")
	}
	return cfg, nil
}

func get(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.ToLower(value)] = struct{}{}
	}
	return result
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := get(key, fallback.String())
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func int64Value(key string, fallback int64) (int64, error) {
	raw := get(key, strconv.FormatInt(fallback, 10))
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func boolValue(key string, fallback bool) (bool, error) {
	raw := get(key, strconv.FormatBool(fallback))
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}
