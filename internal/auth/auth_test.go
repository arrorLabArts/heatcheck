package auth

import (
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("a-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "a-secure-password") {
		t.Fatal("expected password to match")
	}
	if CheckPassword(hash, "not-the-password") {
		t.Fatal("expected incorrect password not to match")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	manager := NewManager("a-secret-that-is-at-least-thirty-two-bytes", time.Minute)
	token, _, err := manager.AccessToken("user-id", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	userID, err := manager.ParseAccessToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if userID != "user-id" {
		t.Fatalf("got user ID %q", userID)
	}
}

func TestRefreshTokenHash(t *testing.T) {
	token, hash, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("expected a token")
	}
	if string(hash) != string(HashRefreshToken(token)) {
		t.Fatal("expected stable hash")
	}
}
