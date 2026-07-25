package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
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
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		argonIterations,
		argonMemory,
		argonParallelism,
		argonKeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func CheckPassword(hash, password string) bool {
	if strings.HasPrefix(hash, "$argon2id$") {
		memory, iterations, parallelism, salt, expected, ok := parseArgon2Hash(hash)
		if !ok {
			return false
		}
		actual := argon2.IDKey(
			[]byte(password),
			salt,
			iterations,
			memory,
			parallelism,
			uint32(len(expected)),
		)
		return subtle.ConstantTimeCompare(actual, expected) == 1
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func PasswordNeedsRehash(hash string) bool {
	memory, iterations, parallelism, salt, key, ok := parseArgon2Hash(hash)
	return !ok ||
		memory != argonMemory ||
		iterations != argonIterations ||
		parallelism != argonParallelism ||
		len(salt) != argonSaltLength ||
		len(key) != argonKeyLength
}

func parseArgon2Hash(hash string) (
	memory uint32,
	iterations uint32,
	parallelism uint8,
	salt []byte,
	key []byte,
	ok bool,
) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" ||
		parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return 0, 0, 0, nil, nil, false
	}
	var parsedParallelism uint32
	if _, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&memory,
		&iterations,
		&parsedParallelism,
	); err != nil {
		return 0, 0, 0, nil, nil, false
	}
	if memory < 8*1024 || memory > 256*1024 ||
		iterations < 1 || iterations > 10 ||
		parsedParallelism < 1 || parsedParallelism > 8 {
		return 0, 0, 0, nil, nil, false
	}
	parallelism = uint8(parsedParallelism)
	var err error
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return 0, 0, 0, nil, nil, false
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return 0, 0, 0, nil, nil, false
	}
	return memory, iterations, parallelism, salt, key, true
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
	return NewOpaqueToken()
}

func NewOpaqueToken() (plain string, hash []byte, err error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", nil, fmt.Errorf("generate opaque token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(random)
	sum := sha256.Sum256([]byte(plain))
	return plain, sum[:], nil
}

func HashRefreshToken(plain string) []byte {
	return HashOpaqueToken(plain)
}

func HashOpaqueToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}
