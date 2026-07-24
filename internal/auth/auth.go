package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type Manager struct {
	secret   []byte
	issuer   string
	accessTT time.Duration
}

type Claims struct {
	jwt.RegisteredClaims
}

func NewManager(secret string, accessTTL time.Duration) *Manager {
	return &Manager{
		secret:   []byte(secret),
		issuer:   "heatcheck-api",
		accessTT: accessTTL,
	}
}

func HashPassword(password string) (string, error) {
	if len(password) < 10 || len(password) > 128 {
		return "", errors.New("password must be between 10 and 128 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (m *Manager) AccessToken(userID string, now time.Time) (string, time.Time, error) {
	expiresAt := now.Add(m.accessTT)
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    m.issuer,
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expiresAt, nil
}

func (m *Manager) ParseAccessToken(raw string) (string, error) {
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return "", errors.New("invalid access token")
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims.Subject == "" {
		return "", errors.New("invalid access token claims")
	}
	return claims.Subject, nil
}

func NewRefreshToken() (plain string, hash []byte, err error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(random)
	sum := sha256.Sum256([]byte(plain))
	return plain, sum[:], nil
}

func HashRefreshToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}
