package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/securedata"
)

type Config struct {
	Environment                    string
	Port                           string
	PublicAppURL                   string
	DatabaseURL                    string
	JWTSecret                      string
	DataEncryptionKey              string
	AccessTokenTTL                 time.Duration
	RefreshTokenTTL                time.Duration
	EmailTokenTTL                  time.Duration
	PasswordResetTTL               time.Duration
	AccountExportTTL               time.Duration
	AccountDeleteWait              time.Duration
	OriginalRetention              time.Duration
	ReservationTTL                 time.Duration
	AllowedOrigins                 []string
	TrustedProxyCIDRs              []string
	BootstrapAdmins                map[string]struct{}
	MaxUploadBytes                 int64
	MinimumAge                     int
	VideoMinDuration               time.Duration
	VideoMaxDuration               time.Duration
	S3Endpoint                     string
	S3PublicEndpoint               string
	S3AccessKey                    string
	S3SecretKey                    string
	S3Bucket                       string
	S3Region                       string
	S3InternalUseSSL               bool
	S3PublicUseSSL                 bool
	UploadURLTTL                   time.Duration
	SMTPHost                       string
	SMTPPort                       int
	SMTPUsername                   string
	SMTPPassword                   string
	SMTPFrom                       string
	SafetyAlertEmail               string
	LegalAlertEmail                string
	SMTPTLSMode                    string
	SMTPTimeout                    time.Duration
	OpenAIAPIKey                   string
	OpenAIBaseURL                  string
	OpenAIModel                    string
	ModerationModel                string
	AIFrameCount                   int
	AIMinConfidence                float64
	OpenAITimeout                  time.Duration
	ClamAVAddress                  string
	WorkerID                       string
	WorkerPoll                     time.Duration
	WorkerJobTimeout               time.Duration
	WorkerStaleAfter               time.Duration
	RequireWorker                  bool
	RevenueCatBaseURL              string
	RevenueCatSecretAPIKey         string
	RevenueCatEntitlementID        string
	RevenueCatAppID                string
	RevenueCatWebhookAuthorization string
	RevenueCatWebhookSigningSecret string
	RevenueCatWebhookTolerance     time.Duration
	RevenueCatAllowSandbox         bool
	RevenueCatTimeout              time.Duration
	ProDailySubmissionLimit        int
	ProMonthlySubmissionLimit      int
	GlobalDailySubmissionLimit     int
	ShutdownTimeout                time.Duration
	ReadHeaderTimeout              time.Duration
}

func Load() (Config, error) {
	environment := strings.ToLower(get("APP_ENV", "development"))
	if environment != "development" && environment != "test" && environment != "production" {
		return Config{}, errors.New("APP_ENV must be development, test, or production")
	}
	allowedOrigins := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if allowedOrigins == "" && environment != "production" {
		allowedOrigins = "http://localhost:3000"
	}
	cfg := Config{
		Environment:       environment,
		Port:              get("PORT", "8080"),
		PublicAppURL:      strings.TrimRight(get("PUBLIC_APP_URL", "https://heatcheck.dogi.watch"), "/"),
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		DataEncryptionKey: strings.TrimSpace(os.Getenv("DATA_ENCRYPTION_KEY")),
		AllowedOrigins:    splitCSV(allowedOrigins),
		TrustedProxyCIDRs: splitCSV(get("TRUSTED_PROXY_CIDRS", "127.0.0.1/32,::1/128")),
		BootstrapAdmins:   stringSet(splitCSV(os.Getenv("BOOTSTRAP_ADMIN_EMAILS"))),
		S3Endpoint:        get("S3_ENDPOINT", "localhost:9000"),
		S3PublicEndpoint:  get("S3_PUBLIC_ENDPOINT", "localhost:9000"),
		S3AccessKey:       strings.TrimSpace(os.Getenv("S3_ACCESS_KEY")),
		S3SecretKey:       os.Getenv("S3_SECRET_KEY"),
		S3Bucket:          get("S3_BUCKET", "heatcheck-clips"),
		S3Region:          get("S3_REGION", "us-east-1"),
		SMTPHost:          strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPUsername:      strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword:      os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:          strings.TrimSpace(os.Getenv("SMTP_FROM")),
		SafetyAlertEmail:  strings.TrimSpace(os.Getenv("SAFETY_ALERT_EMAIL")),
		LegalAlertEmail:   strings.TrimSpace(os.Getenv("LEGAL_ALERT_EMAIL")),
		SMTPTLSMode:       get("SMTP_TLS_MODE", "starttls"),
		OpenAIAPIKey:      strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		OpenAIBaseURL:     strings.TrimRight(get("OPENAI_BASE_URL", "https://api.openai.com"), "/"),
		OpenAIModel:       get("OPENAI_VERIFICATION_MODEL", "gpt-5.6-sol"),
		ModerationModel:   get("OPENAI_MODERATION_MODEL", "omni-moderation-latest"),
		RevenueCatBaseURL: strings.TrimRight(
			get("REVENUECAT_BASE_URL", "https://api.revenuecat.com/v1"),
			"/",
		),
		RevenueCatSecretAPIKey:         strings.TrimSpace(os.Getenv("REVENUECAT_SECRET_API_KEY")),
		RevenueCatEntitlementID:        get("REVENUECAT_ENTITLEMENT_ID", "pro"),
		RevenueCatAppID:                strings.TrimSpace(os.Getenv("REVENUECAT_APP_ID")),
		RevenueCatWebhookAuthorization: strings.TrimSpace(os.Getenv("REVENUECAT_WEBHOOK_AUTHORIZATION")),
		RevenueCatWebhookSigningSecret: strings.TrimSpace(os.Getenv("REVENUECAT_WEBHOOK_SIGNING_SECRET")),
		ClamAVAddress:                  get("CLAMAV_ADDRESS", "clamav:3310"),
		WorkerID:                       get("WORKER_ID", hostname()),
		ShutdownTimeout:                10 * time.Second,
		ReadHeaderTimeout:              5 * time.Second,
	}

	var err error
	if cfg.AccessTokenTTL, err = duration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RefreshTokenTTL, err = duration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.EmailTokenTTL, err = duration("EMAIL_VERIFICATION_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.PasswordResetTTL, err = duration("PASSWORD_RESET_TTL", time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.AccountExportTTL, err = duration("ACCOUNT_EXPORT_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.AccountDeleteWait, err = duration("ACCOUNT_DELETE_GRACE_PERIOD", 7*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.OriginalRetention, err = duration("ORIGINAL_MEDIA_RETENTION", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.UploadURLTTL, err = duration("UPLOAD_URL_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.ReservationTTL, err = duration("SUBMISSION_RESERVATION_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.RevenueCatWebhookTolerance, err = duration("REVENUECAT_WEBHOOK_TOLERANCE", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RevenueCatTimeout, err = duration("REVENUECAT_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.VideoMinDuration, err = duration("VIDEO_MIN_DURATION", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.VideoMaxDuration, err = duration("VIDEO_MAX_DURATION", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.OpenAITimeout, err = duration("OPENAI_TIMEOUT", 90*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.SMTPTimeout, err = duration("SMTP_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WorkerPoll, err = duration("WORKER_POLL_INTERVAL", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WorkerJobTimeout, err = duration("WORKER_JOB_TIMEOUT", 3*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.WorkerStaleAfter, err = duration("WORKER_STALE_AFTER", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.MaxUploadBytes, err = int64Value("MAX_UPLOAD_BYTES", 100*1024*1024); err != nil {
		return Config{}, err
	}
	smtpPort, err := int64Value("SMTP_PORT", 587)
	if err != nil {
		return Config{}, err
	}
	if smtpPort < 1 || smtpPort > 65535 {
		return Config{}, errors.New("SMTP_PORT must be between 1 and 65535")
	}
	cfg.SMTPPort = int(smtpPort)
	frameCount, err := int64Value("AI_FRAME_COUNT", 8)
	if err != nil {
		return Config{}, err
	}
	if frameCount < 3 || frameCount > 16 {
		return Config{}, errors.New("AI_FRAME_COUNT must be between 3 and 16")
	}
	cfg.AIFrameCount = int(frameCount)
	if cfg.AIMinConfidence, err = float64Value("AI_MIN_CONFIDENCE", 0.8); err != nil {
		return Config{}, err
	}
	if cfg.AIMinConfidence < 0.5 || cfg.AIMinConfidence > 1 {
		return Config{}, errors.New("AI_MIN_CONFIDENCE must be between 0.5 and 1")
	}
	minimumAge, err := int64Value("MINIMUM_AGE", 18)
	if err != nil {
		return Config{}, err
	}
	if minimumAge < 13 || minimumAge > 21 {
		return Config{}, errors.New("MINIMUM_AGE must be between 13 and 21")
	}
	cfg.MinimumAge = int(minimumAge)
	proDailyLimit, err := int64Value("PRO_DAILY_SUBMISSION_LIMIT", 1)
	if err != nil {
		return Config{}, err
	}
	proMonthlyLimit, err := int64Value("PRO_MONTHLY_SUBMISSION_LIMIT", 30)
	if err != nil {
		return Config{}, err
	}
	if proDailyLimit < 1 || proDailyLimit > 20 {
		return Config{}, errors.New("PRO_DAILY_SUBMISSION_LIMIT must be between 1 and 20")
	}
	if proMonthlyLimit < proDailyLimit || proMonthlyLimit > 500 {
		return Config{}, errors.New("PRO_MONTHLY_SUBMISSION_LIMIT must be between the daily limit and 500")
	}
	cfg.ProDailySubmissionLimit = int(proDailyLimit)
	cfg.ProMonthlySubmissionLimit = int(proMonthlyLimit)
	globalDailyLimit, err := int64Value("GLOBAL_DAILY_SUBMISSION_LIMIT", 100)
	if err != nil {
		return Config{}, err
	}
	if globalDailyLimit < 1 || globalDailyLimit > 100000 {
		return Config{}, errors.New("GLOBAL_DAILY_SUBMISSION_LIMIT must be between 1 and 100000")
	}
	cfg.GlobalDailySubmissionLimit = int(globalDailyLimit)
	if cfg.S3InternalUseSSL, err = boolValue("S3_INTERNAL_USE_SSL", false); err != nil {
		return Config{}, err
	}
	if cfg.S3PublicUseSSL, err = boolValue("S3_PUBLIC_USE_SSL", false); err != nil {
		return Config{}, err
	}
	if cfg.RequireWorker, err = boolValue("REQUIRE_WORKER", environment == "production"); err != nil {
		return Config{}, err
	}
	if cfg.RevenueCatAllowSandbox, err = boolValue("REVENUECAT_ALLOW_SANDBOX", environment != "production"); err != nil {
		return Config{}, err
	}

	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("PORT must be between 1 and 65535")
	}
	if cfg.AccessTokenTTL <= 0 || cfg.AccessTokenTTL > 24*time.Hour {
		return Config{}, errors.New("ACCESS_TOKEN_TTL must be greater than zero and at most 24h")
	}
	if cfg.RefreshTokenTTL <= cfg.AccessTokenTTL || cfg.RefreshTokenTTL > 365*24*time.Hour {
		return Config{}, errors.New("REFRESH_TOKEN_TTL must be greater than ACCESS_TOKEN_TTL and at most 8760h")
	}
	if cfg.EmailTokenTTL <= 0 || cfg.EmailTokenTTL > 7*24*time.Hour {
		return Config{}, errors.New("EMAIL_VERIFICATION_TTL must be greater than zero and at most 168h")
	}
	if cfg.PasswordResetTTL <= 0 || cfg.PasswordResetTTL > 24*time.Hour {
		return Config{}, errors.New("PASSWORD_RESET_TTL must be greater than zero and at most 24h")
	}
	if cfg.AccountExportTTL <= 0 || cfg.AccountExportTTL > 7*24*time.Hour {
		return Config{}, errors.New("ACCOUNT_EXPORT_TTL must be greater than zero and at most 168h")
	}
	if cfg.AccountDeleteWait <= 0 || cfg.AccountDeleteWait > 30*24*time.Hour {
		return Config{}, errors.New("ACCOUNT_DELETE_GRACE_PERIOD must be greater than zero and at most 720h")
	}
	if cfg.OriginalRetention <= 0 || cfg.OriginalRetention > 365*24*time.Hour {
		return Config{}, errors.New("ORIGINAL_MEDIA_RETENTION must be greater than zero and at most 8760h")
	}
	if cfg.UploadURLTTL <= 0 || cfg.UploadURLTTL > 24*time.Hour {
		return Config{}, errors.New("UPLOAD_URL_TTL must be greater than zero and at most 24h")
	}
	if cfg.ReservationTTL <= cfg.UploadURLTTL || cfg.ReservationTTL > 7*24*time.Hour {
		return Config{}, errors.New("SUBMISSION_RESERVATION_TTL must be greater than UPLOAD_URL_TTL and at most 168h")
	}
	if cfg.RevenueCatWebhookTolerance <= 0 || cfg.RevenueCatWebhookTolerance > 30*time.Minute {
		return Config{}, errors.New("REVENUECAT_WEBHOOK_TOLERANCE must be greater than zero and at most 30m")
	}
	if cfg.RevenueCatTimeout <= 0 || cfg.RevenueCatTimeout > 20*time.Second {
		return Config{}, errors.New("REVENUECAT_TIMEOUT must be greater than zero and at most 20s")
	}
	if cfg.WorkerPoll <= 0 || cfg.WorkerPoll > time.Minute {
		return Config{}, errors.New("WORKER_POLL_INTERVAL must be greater than zero and at most 1m")
	}
	if cfg.WorkerJobTimeout <= 0 || cfg.WorkerJobTimeout > 30*time.Minute {
		return Config{}, errors.New("WORKER_JOB_TIMEOUT must be greater than zero and at most 30m")
	}
	if cfg.WorkerStaleAfter < cfg.WorkerJobTimeout {
		return Config{}, errors.New("WORKER_STALE_AFTER must be at least WORKER_JOB_TIMEOUT")
	}
	if cfg.OpenAITimeout <= 0 || cfg.OpenAITimeout >= cfg.WorkerJobTimeout {
		return Config{}, errors.New("OPENAI_TIMEOUT must be greater than zero and less than WORKER_JOB_TIMEOUT")
	}
	if cfg.SMTPTimeout <= 0 || cfg.SMTPTimeout >= cfg.WorkerJobTimeout {
		return Config{}, errors.New("SMTP_TIMEOUT must be greater than zero and less than WORKER_JOB_TIMEOUT")
	}
	if cfg.MaxUploadBytes < 1024*1024 || cfg.MaxUploadBytes > 500*1024*1024 {
		return Config{}, errors.New("MAX_UPLOAD_BYTES must be between 1 MiB and 500 MiB")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 characters")
	}
	if cfg.Environment == "production" && cfg.DataEncryptionKey == "" {
		return Config{}, errors.New("DATA_ENCRYPTION_KEY is required in production")
	}
	if _, err := securedata.New(cfg.DataEncryptionKey); err != nil {
		return Config{}, err
	}
	if cfg.Environment == "production" &&
		(cfg.SafetyAlertEmail == "" || cfg.LegalAlertEmail == "") {
		return Config{}, errors.New("SAFETY_ALERT_EMAIL and LEGAL_ALERT_EMAIL are required in production")
	}
	if cfg.S3Endpoint == "" || cfg.S3PublicEndpoint == "" || cfg.S3Bucket == "" {
		return Config{}, errors.New("S3_ENDPOINT, S3_PUBLIC_ENDPOINT, and S3_BUCKET are required")
	}
	if cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		return Config{}, errors.New("S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	if cfg.PublicAppURL == "" {
		return Config{}, errors.New("PUBLIC_APP_URL is required")
	}
	for key, value := range map[string]string{
		"REVENUECAT_SECRET_API_KEY":         cfg.RevenueCatSecretAPIKey,
		"REVENUECAT_ENTITLEMENT_ID":         cfg.RevenueCatEntitlementID,
		"REVENUECAT_APP_ID":                 cfg.RevenueCatAppID,
		"REVENUECAT_WEBHOOK_AUTHORIZATION":  cfg.RevenueCatWebhookAuthorization,
		"REVENUECAT_WEBHOOK_SIGNING_SECRET": cfg.RevenueCatWebhookSigningSecret,
	} {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s is required", key)
		}
	}
	if len(cfg.RevenueCatWebhookAuthorization) < 32 {
		return Config{}, errors.New("REVENUECAT_WEBHOOK_AUTHORIZATION must contain at least 32 characters")
	}
	if len(cfg.RevenueCatWebhookSigningSecret) < 32 {
		return Config{}, errors.New("REVENUECAT_WEBHOOK_SIGNING_SECRET must contain at least 32 characters")
	}
	revenueCatURL, err := url.ParseRequestURI(cfg.RevenueCatBaseURL)
	if err != nil || revenueCatURL.Host == "" ||
		(revenueCatURL.Scheme != "http" && revenueCatURL.Scheme != "https") {
		return Config{}, errors.New("REVENUECAT_BASE_URL must be an absolute http or https URL")
	}
	if cfg.Environment == "production" && revenueCatURL.Scheme != "https" {
		return Config{}, errors.New("REVENUECAT_BASE_URL must use https in production")
	}
	if cfg.Environment == "production" && cfg.RevenueCatAllowSandbox {
		return Config{}, errors.New("REVENUECAT_ALLOW_SANDBOX must be false in production")
	}
	publicAppURL, err := url.ParseRequestURI(cfg.PublicAppURL)
	if err != nil || publicAppURL.Host == "" ||
		(publicAppURL.Scheme != "http" && publicAppURL.Scheme != "https") {
		return Config{}, errors.New("PUBLIC_APP_URL must be an absolute http or https URL")
	}
	if cfg.Environment == "production" && publicAppURL.Scheme != "https" {
		return Config{}, errors.New("PUBLIC_APP_URL must use https in production")
	}
	for _, origin := range cfg.AllowedOrigins {
		parsed, err := url.ParseRequestURI(origin)
		if err != nil || parsed.Host == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Path != "" {
			return Config{}, fmt.Errorf("CORS_ALLOWED_ORIGINS contains invalid origin %q", origin)
		}
		if cfg.Environment == "production" && parsed.Scheme != "https" {
			return Config{}, fmt.Errorf("production CORS origin %q must use https", origin)
		}
	}
	for _, cidr := range cfg.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return Config{}, fmt.Errorf("TRUSTED_PROXY_CIDRS contains invalid CIDR %q", cidr)
		}
	}
	if cfg.VideoMinDuration <= 0 || cfg.VideoMaxDuration <= cfg.VideoMinDuration {
		return Config{}, errors.New("VIDEO_MAX_DURATION must be greater than VIDEO_MIN_DURATION")
	}
	if cfg.Environment == "production" && len(cfg.BootstrapAdmins) > 0 {
		return Config{}, errors.New("BOOTSTRAP_ADMIN_EMAILS must not be used in production")
	}
	return cfg, nil
}

func (c Config) ValidateWorker() error {
	var missing []string
	for key, value := range map[string]string{
		"OPENAI_API_KEY":     c.OpenAIAPIKey,
		"SMTP_HOST":          c.SMTPHost,
		"SMTP_FROM":          c.SMTPFrom,
		"SAFETY_ALERT_EMAIL": c.SafetyAlertEmail,
		"LEGAL_ALERT_EMAIL":  c.LegalAlertEmail,
		"CLAMAV_ADDRESS":     c.ClamAVAddress,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("worker configuration missing: %s", strings.Join(missing, ", "))
	}
	if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
		return fmt.Errorf("SMTP_FROM: %w", err)
	}
	for name, address := range map[string]string{
		"SAFETY_ALERT_EMAIL": c.SafetyAlertEmail,
		"LEGAL_ALERT_EMAIL":  c.LegalAlertEmail,
	} {
		parsed, err := mail.ParseAddress(address)
		if err != nil || !strings.EqualFold(parsed.Address, address) {
			return fmt.Errorf("%s must contain one email address", name)
		}
	}
	if c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" {
		return errors.New("SMTP_TLS_MODE must be starttls or tls")
	}
	if (c.SMTPUsername == "") != (c.SMTPPassword == "") {
		return errors.New("SMTP_USERNAME and SMTP_PASSWORD must either both be set or both be empty")
	}
	if strings.TrimSpace(c.OpenAIBaseURL) == "" ||
		strings.TrimSpace(c.OpenAIModel) == "" ||
		strings.TrimSpace(c.ModerationModel) == "" {
		return errors.New("OpenAI base URL and model names are required")
	}
	return nil
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

func float64Value(key string, fallback float64) (float64, error) {
	raw := get(key, strconv.FormatFloat(fallback, 'f', -1, 64))
	value, err := strconv.ParseFloat(raw, 64)
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

func hostname() string {
	value, err := os.Hostname()
	if err != nil || strings.TrimSpace(value) == "" {
		return "heatcheck-worker"
	}
	return value
}
