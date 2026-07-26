package httpapi

import (
	"errors"
	"html"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/auth"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/mailer"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5"
)

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,24}$`)
var dummyPasswordHash, _ = auth.HashPassword("heatcheck-dummy-password")

type registerRequest struct {
	Email       string                    `json:"email"`
	Password    string                    `json:"password"`
	Handle      string                    `json:"handle"`
	DisplayName string                    `json:"display_name"`
	DateOfBirth string                    `json:"date_of_birth"`
	Acceptances []domain.PolicyAcceptance `json:"policy_acceptances"`
}

type tokenResponse struct {
	AccessToken           string      `json:"access_token"`
	AccessTokenExpiresAt  time.Time   `json:"access_token_expires_at"`
	RefreshToken          string      `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time   `json:"refresh_token_expires_at"`
	User                  domain.User `json:"user"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var request registerRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}

	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.Handle = strings.ToLower(strings.TrimSpace(request.Handle))
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	dateOfBirth, dateOfBirthError := time.Parse("2006-01-02", request.DateOfBirth)
	validation := map[string]string{}
	if !validEmail(request.Email) {
		validation["email"] = "must be a valid email address"
	}
	if !handlePattern.MatchString(request.Handle) {
		validation["handle"] = "must contain 3-24 lowercase letters, numbers, or underscores"
	}
	if len(request.DisplayName) < 1 || len(request.DisplayName) > 60 {
		validation["display_name"] = "must contain 1-60 characters"
	}
	if len(request.Password) < 10 || len(request.Password) > 128 {
		validation["password"] = "must contain 10-128 characters"
	}
	if dateOfBirthError != nil {
		validation["date_of_birth"] = "must use YYYY-MM-DD"
	} else if !isAtLeastAge(dateOfBirth, time.Now().UTC(), a.minimumAge) {
		validation["date_of_birth"] = "does not meet the minimum age requirement"
	}
	if len(request.Acceptances) == 0 {
		validation["policy_acceptances"] = "must include all current required policies"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	if !a.allowRateLimitIdentifier(w, r, "register-email", request.Email, 3, 24*time.Hour) {
		return
	}

	passwordHash, err := auth.HashPassword(request.Password)
	if err != nil {
		validationError(w, map[string]string{"password": err.Error()})
		return
	}
	role := domain.RoleUser
	if _, ok := a.bootstrapAdmins[request.Email]; ok {
		role = domain.RoleAdmin
	}

	verificationToken, verificationHash, err := auth.NewOpaqueToken()
	if err != nil {
		a.logger.Error("generate email verification token", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "The account could not be created.", nil)
		return
	}
	verificationURL := a.authLink("verify-email", verificationToken)
	user, err := a.store.CreateUser(r.Context(), store.CreateUserParams{
		Email:                 request.Email,
		PasswordHash:          passwordHash,
		Handle:                request.Handle,
		DisplayName:           request.DisplayName,
		DateOfBirth:           dateOfBirth,
		Role:                  role,
		Acceptances:           request.Acceptances,
		IPAddress:             a.clientIP(r),
		VerificationTokenHash: verificationHash,
		VerificationExpiresAt: time.Now().UTC().Add(a.emailTokenTTL),
		VerificationEmail:     verificationEmail(request.Email, request.DisplayName, verificationURL),
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}

	response, err := a.issueTokens(r, user)
	if err != nil {
		a.logger.Error("issue tokens after registration", "error", err, "user_id", user.ID)
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": response})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if !a.allowRateLimitIdentifier(w, r, "login-email", request.Email, 30, 15*time.Minute) {
		return
	}
	userWithPassword, err := a.store.GetUserByEmail(r.Context(), request.Email)
	passwordHash := userWithPassword.PasswordHash
	if err != nil {
		passwordHash = dummyPasswordHash
	}
	passwordMatches := auth.CheckPassword(passwordHash, request.Password)
	if err != nil || !passwordMatches {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The email or password is incorrect.", nil)
		return
	}
	if auth.PasswordNeedsRehash(userWithPassword.PasswordHash) {
		replacementHash, hashErr := auth.HashPassword(request.Password)
		if hashErr == nil {
			hashErr = a.store.ReplacePasswordHash(
				r.Context(),
				userWithPassword.ID,
				userWithPassword.PasswordHash,
				replacementHash,
			)
		}
		if hashErr != nil {
			a.logger.Warn("upgrade password hash", "error", hashErr, "user_id", userWithPassword.ID)
		}
	}

	response, err := a.issueTokens(r, userWithPassword.User)
	if err != nil {
		a.logger.Error("issue tokens after login", "error", err, "user_id", userWithPassword.ID)
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": response})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	if request.RefreshToken == "" {
		validationError(w, map[string]string{"refresh_token": "is required"})
		return
	}

	newToken, newHash, err := auth.NewRefreshToken()
	if err != nil {
		a.logger.Error("generate refresh token", "error", err)
		handleStoreError(w, err)
		return
	}
	now := time.Now().UTC()
	refreshExpiresAt := now.Add(a.refreshTokenTTL)
	user, err := a.store.RotateRefreshToken(
		r.Context(),
		auth.HashRefreshToken(request.RefreshToken),
		newHash,
		refreshExpiresAt,
		r.UserAgent(),
		a.clientIP(r),
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	accessToken, accessExpiresAt, err := a.auth.AccessToken(user.ID, now)
	if err != nil {
		a.logger.Error("sign access token", "error", err, "user_id", user.ID)
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": tokenResponse{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExpiresAt,
		RefreshToken:          newToken,
		RefreshTokenExpiresAt: refreshExpiresAt,
		User:                  user,
	}})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	if err := a.store.RevokeRefreshToken(r.Context(), auth.HashRefreshToken(request.RefreshToken)); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	missing, err := a.store.MissingRequiredPolicies(r.Context(), user.ID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user":                       user,
			"missing_policy_acceptances": jsonSlice(missing),
		},
	})
}

type opaqueTokenRequest struct {
	Token string `json:"token"`
}

func (a *API) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var request opaqueTokenRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	if strings.TrimSpace(request.Token) == "" {
		validationError(w, map[string]string{"token": "is required"})
		return
	}
	if err := a.store.VerifyEmail(r.Context(), auth.HashOpaqueToken(request.Token)); err != nil {
		if errors.Is(err, store.ErrToken) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_verification_token", "The verification token is invalid or expired.", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) resendEmailVerification(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if user.EmailVerifiedAt != nil {
		writeJSON(w, http.StatusNoContent, nil)
		return
	}
	token, hash, err := auth.NewOpaqueToken()
	if err != nil {
		a.logger.Error("generate email verification token", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Verification email could not be queued.", nil)
		return
	}
	if err := a.store.StartEmailVerification(
		r.Context(),
		user.ID,
		hash,
		time.Now().UTC().Add(a.emailTokenTTL),
		verificationEmail(user.Email, user.DisplayName, a.authLink("verify-email", token)),
	); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"status": "queued"}})
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

func (a *API) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var request forgotPasswordRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if validEmail(request.Email) &&
		!a.allowRateLimitIdentifier(w, r, "password-reset-email", request.Email, 5, time.Hour) {
		return
	}
	token, hash, err := auth.NewOpaqueToken()
	if err != nil {
		a.logger.Error("generate password reset token", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "The password reset could not be requested.", nil)
		return
	}
	if validEmail(request.Email) {
		if err := a.store.StartPasswordReset(
			r.Context(),
			request.Email,
			hash,
			time.Now().UTC().Add(a.passwordResetTTL),
			passwordResetEmail(request.Email, a.authLink("reset-password", token)),
		); err != nil {
			a.logger.Error("queue password reset", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "The password reset could not be requested.", nil)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"data": map[string]string{
			"status": "accepted",
		},
	})
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	var request resetPasswordRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	validation := map[string]string{}
	if strings.TrimSpace(request.Token) == "" {
		validation["token"] = "is required"
	}
	if len(request.NewPassword) < 10 || len(request.NewPassword) > 128 {
		validation["new_password"] = "must contain 10-128 characters"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	passwordHash, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		validationError(w, map[string]string{"new_password": err.Error()})
		return
	}
	if err := a.store.ResetPassword(
		r.Context(),
		auth.HashOpaqueToken(request.Token),
		passwordHash,
	); err != nil {
		if errors.Is(err, store.ErrToken) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_password_reset_token", "The password reset token is invalid or expired.", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	sessions, err := a.store.ListSessions(r.Context(), user.ID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": jsonSlice(sessions)})
}

func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if err := a.store.RevokeSession(r.Context(), user.ID, chi.URLParam(r, "sessionID")); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if err := a.store.RevokeAllSessions(r.Context(), user.ID); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

type acceptPoliciesRequest struct {
	Acceptances []domain.PolicyAcceptance `json:"policy_acceptances"`
}

func (a *API) acceptPolicies(w http.ResponseWriter, r *http.Request) {
	var request acceptPoliciesRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	if len(request.Acceptances) == 0 {
		validationError(w, map[string]string{"policy_acceptances": "must not be empty"})
		return
	}
	user, _ := userFromContext(r.Context())
	if err := a.store.AcceptPolicies(
		r.Context(),
		user.ID,
		request.Acceptances,
		a.clientIP(r),
	); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) issueTokens(r *http.Request, user domain.User) (tokenResponse, error) {
	now := time.Now().UTC()
	accessToken, accessExpiresAt, err := a.auth.AccessToken(user.ID, now)
	if err != nil {
		return tokenResponse{}, err
	}
	refreshToken, refreshHash, err := auth.NewRefreshToken()
	if err != nil {
		return tokenResponse{}, err
	}
	refreshExpiresAt := now.Add(a.refreshTokenTTL)
	if err := a.store.CreateRefreshToken(
		r.Context(),
		user.ID,
		refreshHash,
		refreshExpiresAt,
		r.UserAgent(),
		a.clientIP(r),
	); err != nil {
		return tokenResponse{}, err
	}
	return tokenResponse{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExpiresAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: refreshExpiresAt,
		User:                  user,
	}, nil
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(address.Address, value) && len(value) <= 254
}

func isAtLeastAge(dateOfBirth, now time.Time, minimumAge int) bool {
	if dateOfBirth.After(now) {
		return false
	}
	cutoff := time.Date(
		now.Year()-minimumAge,
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
	return !dateOfBirth.After(cutoff)
}

func (a *API) authLink(action, token string) string {
	return a.publicAppURL + "/auth/" + action + "?token=" + url.QueryEscape(token)
}

func verificationEmail(address, displayName, link string) mailer.Message {
	safeName := html.EscapeString(displayName)
	safeLink := html.EscapeString(link)
	return mailer.Message{
		To:       address,
		Subject:  "Verify your HeatCheck email",
		TextBody: "Hi " + displayName + ",\n\nVerify your HeatCheck email address:\n" + link + "\n\nIf you did not create this account, ignore this message.",
		HTMLBody: "<p>Hi " + safeName + ",</p><p>Verify your HeatCheck email address:</p><p><a href=\"" + safeLink + "\">Verify email</a></p><p>If you did not create this account, ignore this message.</p>",
	}
}

func passwordResetEmail(address, link string) mailer.Message {
	safeLink := html.EscapeString(link)
	return mailer.Message{
		To:       address,
		Subject:  "Reset your HeatCheck password",
		TextBody: "A password reset was requested for your HeatCheck account.\n\nReset your password:\n" + link + "\n\nIf you did not request this, ignore this message.",
		HTMLBody: "<p>A password reset was requested for your HeatCheck account.</p><p><a href=\"" + safeLink + "\">Reset password</a></p><p>If you did not request this, ignore this message.</p>",
	}
}
