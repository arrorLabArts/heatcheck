package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyWebhook(t *testing.T) {
	client := testClient(t, "https://api.revenuecat.test")
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"api_version":"1.0","event":{"id":"event-1"}}`)
	timestamp := fmt.Sprintf("%d", now.Unix())
	mac := hmac.New(sha256.New, []byte("a-signing-secret-that-is-at-least-32-bytes"))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	signature := "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))

	if err := client.VerifyWebhook(
		body,
		"Bearer a-webhook-authorization-value-that-is-long",
		signature,
		now,
	); err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if err := client.VerifyWebhook(body, "wrong", signature, now); err == nil {
		t.Fatal("VerifyWebhook() accepted an invalid authorization header")
	}
	if err := client.VerifyWebhook(
		body,
		"Bearer a-webhook-authorization-value-that-is-long",
		signature,
		now.Add(10*time.Minute),
	); err == nil {
		t.Fatal("VerifyWebhook() accepted a stale signature")
	}
}

func TestTransferAffectedAppUserIDs(t *testing.T) {
	event := WebhookEvent{
		Type:            "TRANSFER",
		TransferredFrom: []string{"source-user", "$RCAnonymousID:source"},
		TransferredTo:   []string{"destination-user", "destination-user"},
	}
	got := event.AffectedAppUserIDs()
	want := []string{"source-user", "$RCAnonymousID:source", "destination-user"}
	if len(got) != len(want) {
		t.Fatalf("AffectedAppUserIDs() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("AffectedAppUserIDs() = %#v, want %#v", got, want)
		}
	}
}

func TestFetchSubscription(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subscribers/user-1" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_test_secret" {
			t.Fatal("missing RevenueCat API authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"request_date": %q,
			"subscriber": {
				"management_url": "https://example.test/manage",
				"entitlements": {
					"pro": {
						"expires_date": %q,
						"grace_period_expires_date": null,
						"product_identifier": "heatcheck_pro_monthly",
						"purchase_date": %q
					}
				},
				"subscriptions": {
					"heatcheck_pro_monthly": {
						"expires_date": %q,
						"grace_period_expires_date": null,
						"purchase_date": %q,
						"original_purchase_date": %q,
						"unsubscribe_detected_at": null,
						"billing_issues_detected_at": null,
						"store": "play_store",
						"is_sandbox": false
					}
				}
			}
		}`,
			now.Format(time.RFC3339),
			now.Add(30*24*time.Hour).Format(time.RFC3339),
			now.Add(-time.Hour).Format(time.RFC3339),
			now.Add(30*24*time.Hour).Format(time.RFC3339),
			now.Add(-time.Hour).Format(time.RFC3339),
			now.Add(-365*24*time.Hour).Format(time.RFC3339),
		)
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	subscription, err := client.FetchSubscription(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !subscription.Active || subscription.Status != "active" ||
		subscription.Tier != "pro" ||
		subscription.ProductID != "heatcheck_pro_monthly" ||
		subscription.Store != "play_store" {
		t.Fatalf("unexpected subscription: %#v", subscription)
	}
}

func TestDeleteCustomer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/subscribers/user-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_test_secret" {
			t.Fatal("missing RevenueCat API authorization")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	if err := client.DeleteCustomer(context.Background(), "user-1"); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxSubscriptionIsNotGranted(t *testing.T) {
	client := testClient(t, "https://api.revenuecat.test")
	now := time.Now().UTC()
	payload := customerResponse{RequestDate: now}
	payload.Subscriber.Entitlements = map[string]struct {
		ExpiresDate            *time.Time `json:"expires_date"`
		GracePeriodExpiresDate *time.Time `json:"grace_period_expires_date"`
		ProductIdentifier      string     `json:"product_identifier"`
		PurchaseDate           *time.Time `json:"purchase_date"`
	}{
		"pro": {
			ExpiresDate:       timePointer(now.Add(time.Hour)),
			ProductIdentifier: "pro_monthly",
			PurchaseDate:      timePointer(now),
		},
	}
	payload.Subscriber.Subscriptions = map[string]struct {
		ExpiresDate             *time.Time `json:"expires_date"`
		GracePeriodExpiresDate  *time.Time `json:"grace_period_expires_date"`
		PurchaseDate            *time.Time `json:"purchase_date"`
		OriginalPurchaseDate    *time.Time `json:"original_purchase_date"`
		UnsubscribeDetectedAt   *time.Time `json:"unsubscribe_detected_at"`
		BillingIssuesDetectedAt *time.Time `json:"billing_issues_detected_at"`
		Store                   string     `json:"store"`
		IsSandbox               bool       `json:"is_sandbox"`
	}{
		"pro_monthly": {
			ExpiresDate:  timePointer(now.Add(time.Hour)),
			PurchaseDate: timePointer(now),
			Store:        "app_store",
			IsSandbox:    true,
		},
	}
	subscription, err := client.subscriptionFromCustomer("user-1", payload)
	if err != nil {
		t.Fatal(err)
	}
	if subscription.Active || subscription.Status != "inactive" {
		t.Fatalf("sandbox subscription granted access: %#v", subscription)
	}
}

func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL:              baseURL,
		SecretAPIKey:         "sk_test_secret",
		EntitlementID:        "pro",
		AppID:                "app_test",
		WebhookAuthorization: "Bearer a-webhook-authorization-value-that-is-long",
		WebhookSigningSecret: "a-signing-secret-that-is-at-least-32-bytes",
		WebhookTolerance:     5 * time.Minute,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func timePointer(value time.Time) *time.Time {
	return &value
}
