package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/auth"
	"github.com/arrorLabArts/heatcheck/internal/billing"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/media"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Config struct {
	AllowedOrigins    []string
	TrustedProxyCIDRs []string
	BootstrapAdmins   map[string]struct{}
	MaxUploadBytes    int64
	UploadURLTTL      time.Duration
	RefreshTokenTTL   time.Duration
	EmailTokenTTL     time.Duration
	PasswordResetTTL  time.Duration
	AccountDeleteWait time.Duration
	PublicAppURL      string
	SafetyAlertEmail  string
	LegalAlertEmail   string
	MinimumAge        int
	RequireWorker     bool
	WorkerStaleAfter  time.Duration
	ReservationTTL    time.Duration
	ProDailyLimit     int
	ProMonthlyLimit   int
	GlobalDailyLimit  int
}

type API struct {
	store             *store.Store
	auth              *auth.Manager
	media             *media.Storage
	billing           *billing.Client
	logger            *slog.Logger
	allowedOrigins    []string
	bootstrapAdmins   map[string]struct{}
	maxUploadBytes    int64
	uploadURLTTL      time.Duration
	refreshTokenTTL   time.Duration
	emailTokenTTL     time.Duration
	passwordResetTTL  time.Duration
	accountDeleteWait time.Duration
	publicAppURL      string
	safetyAlertEmail  string
	legalAlertEmail   string
	minimumAge        int
	trustedProxies    []*net.IPNet
	requireWorker     bool
	workerStaleAfter  time.Duration
	reservationTTL    time.Duration
	proDailyLimit     int
	proMonthlyLimit   int
	globalDailyLimit  int
}

func New(
	store *store.Store,
	authManager *auth.Manager,
	mediaStorage *media.Storage,
	billingClient *billing.Client,
	logger *slog.Logger,
	config Config,
) *API {
	var trustedProxies []*net.IPNet
	for _, value := range config.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(value)
		if err == nil {
			trustedProxies = append(trustedProxies, network)
		}
	}
	return &API{
		store:             store,
		auth:              authManager,
		media:             mediaStorage,
		billing:           billingClient,
		logger:            logger,
		allowedOrigins:    config.AllowedOrigins,
		bootstrapAdmins:   config.BootstrapAdmins,
		maxUploadBytes:    config.MaxUploadBytes,
		uploadURLTTL:      config.UploadURLTTL,
		refreshTokenTTL:   config.RefreshTokenTTL,
		emailTokenTTL:     config.EmailTokenTTL,
		passwordResetTTL:  config.PasswordResetTTL,
		accountDeleteWait: config.AccountDeleteWait,
		publicAppURL:      config.PublicAppURL,
		safetyAlertEmail:  config.SafetyAlertEmail,
		legalAlertEmail:   config.LegalAlertEmail,
		minimumAge:        config.MinimumAge,
		trustedProxies:    trustedProxies,
		requireWorker:     config.RequireWorker,
		workerStaleAfter:  config.WorkerStaleAfter,
		reservationTTL:    config.ReservationTTL,
		proDailyLimit:     config.ProDailyLimit,
		proMonthlyLimit:   config.ProMonthlyLimit,
		globalDailyLimit:  config.GlobalDailyLimit,
	}
}

func (a *API) Router() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(requestIDHeader)
	router.Use(securityHeaders)
	router.Use(cors(a.allowedOrigins))
	router.Use(recoverer(a.logger))
	router.Use(requestLogger(a.logger))
	router.Use(middleware.Timeout(30 * time.Second))

	router.Get("/healthz", a.health)
	router.Get("/readyz", a.ready)
	router.Get("/openapi.yaml", a.openAPISpec)

	router.Route("/v1", func(r chi.Router) {
		r.Get("/policies", a.listPolicies)
		r.Get("/policies/{kind}", a.getPolicy)

		r.Route("/auth", func(r chi.Router) {
			r.With(a.rateLimit("register", 10, time.Hour)).Post("/register", a.register)
			r.With(a.rateLimit("login", 30, 15*time.Minute)).Post("/login", a.login)
			r.With(a.rateLimit("refresh", 60, 15*time.Minute)).Post("/refresh", a.refresh)
			r.Post("/logout", a.logout)
			r.With(a.rateLimit("verify-email", 30, time.Hour)).Post("/verify-email", a.verifyEmail)
			r.With(a.rateLimit("forgot-password", 10, time.Hour)).Post("/forgot-password", a.forgotPassword)
			r.With(a.rateLimit("reset-password", 20, time.Hour)).Post("/reset-password", a.resetPassword)
		})

		r.With(a.authenticate(false)).Get("/challenges", a.listChallenges)
		r.Get("/challenges/daily", a.dailyChallenge)
		r.With(a.authenticate(false)).Get("/challenges/{challengeID}", a.getChallenge)
		r.With(a.authenticate(false)).Get("/challenges/{challengeID}/submissions", a.listSubmissions)
		r.With(a.authenticate(false)).Get("/challenges/{challengeID}/leaderboard", a.getLeaderboard)
		r.With(a.authenticate(false)).Get("/submissions/{submissionID}", a.getSubmission)
		r.Get("/submissions/{submissionID}/share-card.png", a.getShareCard)
		r.Get("/users/{userID}", a.getPublicUser)

		r.With(a.rateLimit("copyright-notice", 5, time.Hour)).Post("/copyright/notices", a.createCopyrightNotice)
		r.Post("/billing/revenuecat/webhook", a.revenueCatWebhook)

		r.Group(func(r chi.Router) {
			r.Use(a.authenticate(true))

			r.Get("/me", a.me)
			r.Get("/me/subscription", a.getSubscription)
			r.With(a.rateLimitActor("subscription-sync", 10, time.Hour)).
				Post("/me/subscription/sync", a.syncSubscription)
			r.With(a.rateLimitActor("account-export", 3, 24*time.Hour)).Post("/me/exports", a.createAccountExport)
			r.Get("/me/exports/{exportID}", a.getAccountExport)
			r.Get("/me/deletion", a.getAccountDeletion)
			r.Delete("/me", a.requestAccountDeletion)
			r.Delete("/me/deletion", a.cancelAccountDeletion)
			r.Post("/me/policy-acceptances", a.acceptPolicies)
			r.With(a.rateLimitActor("resend-verification", 5, time.Hour)).Post("/me/email-verification", a.resendEmailVerification)
			r.Get("/me/sessions", a.listSessions)
			r.Delete("/me/sessions", a.revokeAllSessions)
			r.Delete("/me/sessions/{sessionID}", a.revokeSession)
			r.With(a.rateLimitActor("copyright-counter", 5, 24*time.Hour)).Post("/copyright/notices/{noticeID}/counter", a.createCounterNotice)
			r.With(a.rateLimitActor("moderation-appeal", 10, 24*time.Hour)).Post("/moderation/actions/{actionID}/appeals", a.createAppeal)

			r.Group(func(r chi.Router) {
				r.Use(a.requireActive)
				r.Use(a.requirePolicies)
				r.Use(a.requireEmailVerified)

				r.With(a.rateLimitActor("media-upload", 30, time.Hour)).Post("/uploads", a.createUpload)
				r.With(a.rateLimitActor("media-complete", 30, time.Hour)).Post("/uploads/{uploadID}/complete", a.completeUpload)
				r.With(a.rateLimitActor("submission-create", 20, 24*time.Hour)).Post("/challenges/{challengeID}/submissions", a.createSubmission)
				r.With(a.rateLimitActor("vote", 300, time.Hour)).Put("/submissions/{submissionID}/vote", a.vote)
				r.With(a.rateLimitActor("report", 20, time.Hour)).Post("/reports", a.createReport)
				r.With(a.rateLimitActor("block", 60, time.Hour)).Put("/users/{userID}/block", a.blockUser)
				r.With(a.rateLimitActor("block", 60, time.Hour)).Delete("/users/{userID}/block", a.unblockUser)
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
