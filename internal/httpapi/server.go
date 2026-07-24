package httpapi

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/auth"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/media"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Config struct {
	AllowedOrigins  []string
	BootstrapAdmins map[string]struct{}
	MaxUploadBytes  int64
	UploadURLTTL    time.Duration
	RefreshTokenTTL time.Duration
	MinimumAge      int
}

type API struct {
	store           *store.Store
	auth            *auth.Manager
	media           *media.Storage
	logger          *slog.Logger
	allowedOrigins  []string
	bootstrapAdmins map[string]struct{}
	maxUploadBytes  int64
	uploadURLTTL    time.Duration
	refreshTokenTTL time.Duration
	minimumAge      int
	limiter         *fixedWindowLimiter
}

func New(
	store *store.Store,
	authManager *auth.Manager,
	mediaStorage *media.Storage,
	logger *slog.Logger,
	config Config,
) *API {
	return &API{
		store:           store,
		auth:            authManager,
		media:           mediaStorage,
		logger:          logger,
		allowedOrigins:  config.AllowedOrigins,
		bootstrapAdmins: config.BootstrapAdmins,
		maxUploadBytes:  config.MaxUploadBytes,
		uploadURLTTL:    config.UploadURLTTL,
		refreshTokenTTL: config.RefreshTokenTTL,
		minimumAge:      config.MinimumAge,
		limiter:         newFixedWindowLimiter(),
	}
}

func (a *API) Router() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(securityHeaders)
	router.Use(cors(a.allowedOrigins))
	router.Use(recoverer(a.logger))
	router.Use(requestLogger(a.logger))
	router.Use(middleware.Timeout(30 * time.Second))

	router.Get("/healthz", a.health)
	router.Get("/readyz", a.ready)

	router.Route("/v1", func(r chi.Router) {
		r.Get("/policies", a.listPolicies)
		r.Get("/policies/{kind}", a.getPolicy)

		r.Route("/auth", func(r chi.Router) {
			r.With(a.rateLimit("register", 10, time.Hour)).Post("/register", a.register)
			r.With(a.rateLimit("login", 30, 15*time.Minute)).Post("/login", a.login)
			r.With(a.rateLimit("refresh", 60, 15*time.Minute)).Post("/refresh", a.refresh)
			r.Post("/logout", a.logout)
		})

		r.With(a.authenticate(false)).Get("/challenges", a.listChallenges)
		r.Get("/challenges/daily", a.dailyChallenge)
		r.With(a.authenticate(false)).Get("/challenges/{challengeID}", a.getChallenge)
		r.With(a.authenticate(false)).Get("/challenges/{challengeID}/submissions", a.listSubmissions)
		r.With(a.authenticate(false)).Get("/submissions/{submissionID}", a.getSubmission)
		r.Get("/users/{userID}", a.getPublicUser)

		r.With(a.rateLimit("copyright-notice", 5, time.Hour)).Post("/copyright/notices", a.createCopyrightNotice)

		r.Group(func(r chi.Router) {
			r.Use(a.authenticate(true))

			r.Get("/me", a.me)
			r.Post("/me/policy-acceptances", a.acceptPolicies)
			r.Post("/copyright/notices/{noticeID}/counter", a.createCounterNotice)
			r.Post("/moderation/actions/{actionID}/appeals", a.createAppeal)

			r.Group(func(r chi.Router) {
				r.Use(a.requireActive)
				r.Use(a.requirePolicies)

				r.Post("/uploads", a.createUpload)
				r.Post("/uploads/{uploadID}/complete", a.completeUpload)
				r.Post("/challenges/{challengeID}/submissions", a.createSubmission)
				r.Put("/submissions/{submissionID}/vote", a.vote)
				r.Post("/reports", a.createReport)
				r.Put("/users/{userID}/block", a.blockUser)
				r.Delete("/users/{userID}/block", a.unblockUser)
			})

			r.Route("/admin", func(r chi.Router) {
				r.Use(a.requireActive)
				r.Use(requireRoles(domain.RoleModerator, domain.RoleAdmin))

				r.Post("/challenges", a.createChallenge)
				r.Get("/reports", a.listReports)
				r.Post("/reports/{reportID}/dismiss", a.dismissReport)
				r.Post("/moderation/actions", a.createModerationAction)
				r.Get("/submissions", a.listModerationSubmissions)
				r.Put("/submissions/{submissionID}/verification", a.updateVerification)
				r.Get("/appeals", a.listAppeals)
				r.Put("/appeals/{appealID}", a.reviewAppeal)
				r.Get("/copyright/notices", a.listCopyrightNotices)
				r.Put("/copyright/notices/{noticeID}", a.reviewCopyrightNotice)
				r.Get("/audit-events", a.listAuditEvents)

				r.With(requireRoles(domain.RoleAdmin)).Post("/policies", a.publishPolicy)
			})
		})
	})
	return router
}

type fixedWindowEntry struct {
	count   int
	resetAt time.Time
}

type fixedWindowLimiter struct {
	mu      sync.Mutex
	entries map[string]fixedWindowEntry
}

func newFixedWindowLimiter() *fixedWindowLimiter {
	return &fixedWindowLimiter{entries: map[string]fixedWindowEntry{}}
}

func (l *fixedWindowLimiter) allow(key string, maximum int, window time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.resetAt.IsZero() || !now.Before(entry.resetAt) {
		entry = fixedWindowEntry{resetAt: now.Add(window)}
	}
	if entry.count >= maximum {
		l.entries[key] = entry
		return false
	}
	entry.count++
	l.entries[key] = entry

	if len(l.entries) > 10_000 {
		for candidate, value := range l.entries {
			if !now.Before(value.resetAt) {
				delete(l.entries, candidate)
			}
		}
	}
	return true
}
