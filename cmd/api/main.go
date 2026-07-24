package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/auth"
	"github.com/arrorLabArts/heatcheck/internal/config"
	"github.com/arrorLabArts/heatcheck/internal/database"
	"github.com/arrorLabArts/heatcheck/internal/httpapi"
	"github.com/arrorLabArts/heatcheck/internal/media"
	"github.com/arrorLabArts/heatcheck/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
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

	mediaStorage, err := media.New(media.Config{
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
	if err := mediaStorage.EnsureBucket(startupContext); err != nil {
		return err
	}

	dataStore := store.New(pool)
	authManager := auth.NewManager(cfg.JWTSecret, cfg.AccessTokenTTL)
	api := httpapi.New(dataStore, authManager, mediaStorage, logger, httpapi.Config{
		AllowedOrigins:  cfg.AllowedOrigins,
		BootstrapAdmins: cfg.BootstrapAdmins,
		MaxUploadBytes:  cfg.MaxUploadBytes,
		UploadURLTTL:    cfg.UploadURLTTL,
		RefreshTokenTTL: cfg.RefreshTokenTTL,
		MinimumAge:      cfg.MinimumAge,
	})
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.Router(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       60 * time.Second,
		WriteTimeout:      35 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server started", "address", server.Addr, "environment", cfg.Environment)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-rootContext.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		return err
	}
	logger.Info("http server stopped")
	return nil
}
