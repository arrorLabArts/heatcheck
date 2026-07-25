package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/aiprovider"
	"github.com/arrorLabArts/heatcheck/internal/billing"
	"github.com/arrorLabArts/heatcheck/internal/config"
	"github.com/arrorLabArts/heatcheck/internal/database"
	"github.com/arrorLabArts/heatcheck/internal/mailer"
	"github.com/arrorLabArts/heatcheck/internal/media"
	"github.com/arrorLabArts/heatcheck/internal/mediaprocessor"
	"github.com/arrorLabArts/heatcheck/internal/securedata"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/arrorLabArts/heatcheck/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateWorker(); err != nil {
		return err
	}
	rootContext, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	startupContext, cancelStartup := context.WithTimeout(rootContext, 30*time.Second)
	defer cancelStartup()

	pool, err := database.Open(startupContext, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(startupContext, pool, logger); err != nil {
		return err
	}
	storage, err := media.New(media.Config{
		Endpoint:       cfg.S3Endpoint,
		PublicEndpoint: cfg.S3PublicEndpoint,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		Bucket:         cfg.S3Bucket,
		Region:         cfg.S3Region,
		InternalUseSSL: cfg.S3InternalUseSSL,
		PublicUseSSL:   cfg.S3PublicUseSSL,
	})
	if err != nil {
		return err
	}
	if err := storage.EnsureBucket(startupContext); err != nil {
		return err
	}
	aiClient, err := aiprovider.New(aiprovider.Config{
		APIKey:            cfg.OpenAIAPIKey,
		BaseURL:           cfg.OpenAIBaseURL,
		VerificationModel: cfg.OpenAIModel,
		ModerationModel:   cfg.ModerationModel,
		Timeout:           cfg.OpenAITimeout,
	})
	if err != nil {
		return err
	}
	mailClient, err := mailer.New(mailer.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		TLSMode:  cfg.SMTPTLSMode,
		Timeout:  cfg.SMTPTimeout,
	})
	if err != nil {
		return err
	}
	billingClient, err := billing.New(billing.Config{
		BaseURL:              cfg.RevenueCatBaseURL,
		SecretAPIKey:         cfg.RevenueCatSecretAPIKey,
		EntitlementID:        cfg.RevenueCatEntitlementID,
		AppID:                cfg.RevenueCatAppID,
		WebhookAuthorization: cfg.RevenueCatWebhookAuthorization,
		WebhookSigningSecret: cfg.RevenueCatWebhookSigningSecret,
		WebhookTolerance:     cfg.RevenueCatWebhookTolerance,
		AllowSandbox:         cfg.RevenueCatAllowSandbox,
		Timeout:              cfg.RevenueCatTimeout,
	})
	if err != nil {
		return err
	}
	processor := mediaprocessor.New(mediaprocessor.Config{
		ClamAVAddress: cfg.ClamAVAddress,
		MinDuration:   cfg.VideoMinDuration,
		MaxDuration:   cfg.VideoMaxDuration,
		FrameCount:    cfg.AIFrameCount,
		MaxBytes:      cfg.MaxUploadBytes,
	})
	if err := processor.Ping(startupContext); err != nil {
		return err
	}
	dataCipher, err := securedata.New(cfg.DataEncryptionKey)
	if err != nil {
		return err
	}
	runner := worker.New(
		store.New(pool, store.WithCipher(dataCipher)),
		storage,
		processor,
		aiClient,
		mailClient,
		billingClient,
		logger,
		worker.Config{
			ID:                cfg.WorkerID,
			PollInterval:      cfg.WorkerPoll,
			JobTimeout:        cfg.WorkerJobTimeout,
			MinConfidence:     cfg.AIMinConfidence,
			AccountExportTTL:  cfg.AccountExportTTL,
			SafetyAlertEmail:  cfg.SafetyAlertEmail,
			OriginalRetention: cfg.OriginalRetention,
		},
	)
	return runner.Run(rootContext)
}
