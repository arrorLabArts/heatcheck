package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5/middleware"
)

type userContextKey struct{}

func userFromContext(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(userContextKey{}).(domain.User)
	return user, ok
}

func (a *API) authenticate(required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := strings.TrimSpace(r.Header.Get("Authorization"))
			if header == "" && !required {
				next.ServeHTTP(w, r)
				return
			}
			scheme, token, ok := strings.Cut(header, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				writeError(w, http.StatusUnauthorized, "authentication_required", "A valid bearer token is required.", nil)
				return
			}
			userID, err := a.auth.ParseAccessToken(token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid_access_token", "The access token is invalid or expired.", nil)
				return
			}
			user, err := a.store.GetUserByID(r.Context(), userID)
			if err != nil || user.Status == "deleted" {
				writeError(w, http.StatusUnauthorized, "invalid_access_token", "The account is not available.", nil)
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey{}, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (a *API) requireActive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
			return
		}
		if user.Status != "active" {
			writeError(w, http.StatusForbidden, "account_restricted", "The account is currently restricted.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requirePolicies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		missing, err := a.store.MissingRequiredPolicies(r.Context(), user.ID)
		if err != nil {
			a.logger.Error("check policy acceptance", "error", err, "user_id", user.ID)
			handleStoreError(w, err)
			return
		}
		if len(missing) > 0 {
			writeError(
				w,
				http.StatusPreconditionRequired,
				"policy_acceptance_required",
				"Current required policies must be accepted before continuing.",
				map[string]any{"missing": missing},
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) requireEmailVerified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
			return
		}
		if user.EmailVerifiedAt == nil {
			writeError(
				w,
				http.StatusForbidden,
				"email_verification_required",
				"Verify the account email address before continuing.",
				nil,
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireRoles(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
				return
			}
			if !slices.Contains(roles, user.Role) {
				writeError(w, http.StatusForbidden, "insufficient_role", "This operation requires an elevated role.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func requestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", middleware.GetReqID(r.Context()))
		next.ServeHTTP(w, r)
	})
}

func cors(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error(
						"request panic",
						"panic", recovered,
						"stack", string(debug.Stack()),
						"request_id", middleware.GetReqID(r.Context()),
					)
					writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(recorder, r)
			logger.Info(
				"http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.Status(),
				"bytes", recorder.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

func (a *API) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote := net.ParseIP(host)
	if remote == nil || !a.isTrustedProxy(remote) {
		return host
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for index := len(chain) - 1; index >= 0; index-- {
		candidate := net.ParseIP(strings.TrimSpace(chain[index]))
		if candidate == nil {
			continue
		}
		if !a.isTrustedProxy(candidate) {
			return candidate.String()
		}
	}
	return remote.String()
}

func (a *API) isTrustedProxy(ip net.IP) bool {
	for _, network := range a.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *API) rateLimit(
	name string,
	maximum int,
	window time.Duration,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := time.Now().UTC()
			key := name + ":" + a.clientIP(r)
			resetAt, err := a.store.AllowRateLimit(r.Context(), key, maximum, window, now)
			if errors.Is(err, store.ErrRateLimit) {
				retryAfter := max(1, int(time.Until(resetAt).Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil)
				return
			}
			if err != nil {
				a.logger.Error("apply rate limit", "error", err, "limit", name)
				writeError(w, http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *API) rateLimitActor(
	name string,
	maximum int,
	window time.Duration,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
				return
			}
			now := time.Now().UTC()
			key := name + ":user:" + user.ID
			resetAt, err := a.store.AllowRateLimit(r.Context(), key, maximum, window, now)
			if errors.Is(err, store.ErrRateLimit) {
				retryAfter := max(1, int(time.Until(resetAt).Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil)
				return
			}
			if err != nil {
				a.logger.Error("apply rate limit", "error", err, "limit", name, "user_id", user.ID)
				writeError(w, http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *API) allowRateLimitIdentifier(
	w http.ResponseWriter,
	r *http.Request,
	name string,
	identifier string,
	maximum int,
	window time.Duration,
) bool {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(identifier))))
	key := name + ":identifier:" + hex.EncodeToString(sum[:])
	resetAt, err := a.store.AllowRateLimit(r.Context(), key, maximum, window, time.Now().UTC())
	if errors.Is(err, store.ErrRateLimit) {
		retryAfter := max(1, int(time.Until(resetAt).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil)
		return false
	}
	if err != nil {
		a.logger.Error("apply rate limit", "error", err, "limit", name)
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.", nil)
		return false
	}
	return true
}
