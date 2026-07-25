package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

const ProviderRevenueCat = "revenuecat"

type Config struct {
	BaseURL              string
	SecretAPIKey         string
	EntitlementID        string
	AppID                string
	WebhookAuthorization string
	WebhookSigningSecret string
	WebhookTolerance     time.Duration
	AllowSandbox         bool
	Timeout              time.Duration
}

type Client struct {
	baseURL              string
	secretAPIKey         string
	entitlementID        string
	appID                string
	webhookAuthorization string
	webhookSigningSecret string
	webhookTolerance     time.Duration
	allowSandbox         bool
	httpClient           *http.Client
}

type Webhook struct {
	APIVersion string       `json:"api_version"`
	Event      WebhookEvent `json:"event"`
}

type WebhookEvent struct {
	ID                    string   `json:"id"`
	Type                  string   `json:"type"`
	AppID                 string   `json:"app_id"`
	AppUserID             string   `json:"app_user_id"`
	TransferredFrom       []string `json:"transferred_from"`
	TransferredTo         []string `json:"transferred_to"`
	ProductID             string   `json:"product_id"`
	Store                 string   `json:"store"`
	Environment           string   `json:"environment"`
	EntitlementIDs        []string `json:"entitlement_ids"`
	TransactionID         string   `json:"transaction_id"`
	OriginalTransactionID string   `json:"original_transaction_id"`
	Currency              string   `json:"currency"`
	Price                 float64  `json:"price_in_purchased_currency"`
	EventTimestampMS      int64    `json:"event_timestamp_ms"`
	PurchasedAtMS         int64    `json:"purchased_at_ms"`
	ExpirationAtMS        *int64   `json:"expiration_at_ms"`
}

func (e WebhookEvent) AffectedAppUserIDs() []string {
	candidates := []string{e.AppUserID}
	if e.Type == "TRANSFER" {
		candidates = append(candidates, e.TransferredFrom...)
		candidates = append(candidates, e.TransferredTo...)
	}
	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, errors.New("RevenueCat base URL is required")
	}
	if _, err := url.ParseRequestURI(config.BaseURL); err != nil {
		return nil, fmt.Errorf("RevenueCat base URL: %w", err)
	}
	if strings.TrimSpace(config.SecretAPIKey) == "" {
		return nil, errors.New("RevenueCat secret API key is required")
	}
	if strings.TrimSpace(config.EntitlementID) == "" {
		return nil, errors.New("RevenueCat entitlement ID is required")
	}
	if strings.TrimSpace(config.AppID) == "" {
		return nil, errors.New("RevenueCat app ID is required")
	}
	if strings.TrimSpace(config.WebhookAuthorization) == "" {
		return nil, errors.New("RevenueCat webhook authorization is required")
	}
	if strings.TrimSpace(config.WebhookSigningSecret) == "" {
		return nil, errors.New("RevenueCat webhook signing secret is required")
	}
	if config.WebhookTolerance <= 0 {
		config.WebhookTolerance = 5 * time.Minute
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	return &Client{
		baseURL:              strings.TrimRight(config.BaseURL, "/"),
		secretAPIKey:         config.SecretAPIKey,
		entitlementID:        config.EntitlementID,
		appID:                config.AppID,
		webhookAuthorization: config.WebhookAuthorization,
		webhookSigningSecret: config.WebhookSigningSecret,
		webhookTolerance:     config.WebhookTolerance,
		allowSandbox:         config.AllowSandbox,
		httpClient:           &http.Client{Timeout: config.Timeout},
	}, nil
}

func (c *Client) EntitlementID() string {
	return c.entitlementID
}

func (c *Client) AllowSandbox() bool {
	return c.allowSandbox
}

func (c *Client) VerifyWebhook(
	body []byte,
	authorization string,
	signatureHeader string,
	now time.Time,
) error {
	expectedAuthorization := []byte(c.webhookAuthorization)
	actualAuthorization := []byte(authorization)
	if len(expectedAuthorization) != len(actualAuthorization) ||
		subtle.ConstantTimeCompare(expectedAuthorization, actualAuthorization) != 1 {
		return errors.New("invalid webhook authorization")
	}

	parts := make(map[string]string)
	for _, part := range strings.Split(signatureHeader, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			parts[key] = value
		}
	}
	timestamp := parts["t"]
	signature := parts["v1"]
	if timestamp == "" || signature == "" {
		return errors.New("invalid webhook signature header")
	}
	timestampUnix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid webhook timestamp")
	}
	if difference := now.Unix() - timestampUnix; difference < -int64(c.webhookTolerance.Seconds()) ||
		difference > int64(c.webhookTolerance.Seconds()) {
		return errors.New("webhook signature timestamp is outside the allowed tolerance")
	}
	decodedSignature, err := hex.DecodeString(signature)
	if err != nil {
		return errors.New("invalid webhook signature encoding")
	}
	mac := hmac.New(sha256.New, []byte(c.webhookSigningSecret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), decodedSignature) {
		return errors.New("invalid webhook signature")
	}
	return nil
}

func (c *Client) ParseWebhook(body []byte) (Webhook, error) {
	var webhook Webhook
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&webhook); err != nil {
		return Webhook{}, fmt.Errorf("decode RevenueCat webhook: %w", err)
	}
	if webhook.APIVersion != "1.0" {
		return Webhook{}, fmt.Errorf("unsupported RevenueCat webhook API version %q", webhook.APIVersion)
	}
	if webhook.Event.ID == "" || webhook.Event.Type == "" {
		return Webhook{}, errors.New("RevenueCat webhook event ID and type are required")
	}
	if webhook.Event.AppID != c.appID {
		return Webhook{}, errors.New("RevenueCat webhook app ID does not match")
	}
	return webhook, nil
}

func (c *Client) FetchSubscription(
	ctx context.Context,
	userID string,
) (domain.Subscription, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/subscribers/"+url.PathEscape(userID),
		nil,
	)
	if err != nil {
		return domain.Subscription{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.secretAPIKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("fetch RevenueCat subscriber: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		return domain.Subscription{}, fmt.Errorf(
			"fetch RevenueCat subscriber: status %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(message)),
		)
	}

	var payload customerResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload); err != nil {
		return domain.Subscription{}, fmt.Errorf("decode RevenueCat subscriber: %w", err)
	}
	return c.subscriptionFromCustomer(userID, payload)
}

func (c *Client) DeleteCustomer(ctx context.Context, userID string) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		c.baseURL+"/subscribers/"+url.PathEscape(userID),
		nil,
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.secretAPIKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("delete RevenueCat customer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK ||
		response.StatusCode == http.StatusNoContent ||
		response.StatusCode == http.StatusNotFound {
		return nil
	}
	message, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	return fmt.Errorf(
		"delete RevenueCat customer: status %d: %s",
		response.StatusCode,
		strings.TrimSpace(string(message)),
	)
}

type customerResponse struct {
	RequestDate time.Time `json:"request_date"`
	Subscriber  struct {
		Entitlements map[string]struct {
			ExpiresDate            *time.Time `json:"expires_date"`
			GracePeriodExpiresDate *time.Time `json:"grace_period_expires_date"`
			ProductIdentifier      string     `json:"product_identifier"`
			PurchaseDate           *time.Time `json:"purchase_date"`
		} `json:"entitlements"`
		Subscriptions map[string]struct {
			ExpiresDate             *time.Time `json:"expires_date"`
			GracePeriodExpiresDate  *time.Time `json:"grace_period_expires_date"`
			PurchaseDate            *time.Time `json:"purchase_date"`
			OriginalPurchaseDate    *time.Time `json:"original_purchase_date"`
			UnsubscribeDetectedAt   *time.Time `json:"unsubscribe_detected_at"`
			BillingIssuesDetectedAt *time.Time `json:"billing_issues_detected_at"`
			Store                   string     `json:"store"`
			IsSandbox               bool       `json:"is_sandbox"`
		} `json:"subscriptions"`
		ManagementURL string `json:"management_url"`
	} `json:"subscriber"`
}

func (c *Client) subscriptionFromCustomer(
	userID string,
	payload customerResponse,
) (domain.Subscription, error) {
	sourceUpdatedAt := payload.RequestDate.UTC()
	if sourceUpdatedAt.IsZero() {
		sourceUpdatedAt = time.Now().UTC()
	}
	result := domain.Subscription{
		UserID:          userID,
		Tier:            "none",
		Provider:        ProviderRevenueCat,
		EntitlementID:   c.entitlementID,
		Environment:     "unknown",
		Status:          "inactive",
		ManagementURL:   payload.Subscriber.ManagementURL,
		SourceUpdatedAt: sourceUpdatedAt,
	}
	entitlement, ok := payload.Subscriber.Entitlements[c.entitlementID]
	if !ok {
		return result, nil
	}
	result.Tier = "pro"
	result.ProductID = entitlement.ProductIdentifier
	result.CurrentPeriodStart = entitlement.PurchaseDate
	result.CurrentPeriodEnd = laterTime(
		entitlement.ExpiresDate,
		entitlement.GracePeriodExpiresDate,
	)
	result.Environment = "production"

	if subscription, exists := payload.Subscriber.Subscriptions[result.ProductID]; exists {
		result.Store = subscription.Store
		result.CurrentPeriodStart = firstTime(
			subscription.PurchaseDate,
			entitlement.PurchaseDate,
			subscription.OriginalPurchaseDate,
		)
		result.CurrentPeriodEnd = laterTime(
			subscription.ExpiresDate,
			subscription.GracePeriodExpiresDate,
			entitlement.ExpiresDate,
			entitlement.GracePeriodExpiresDate,
		)
		if subscription.IsSandbox {
			result.Environment = "sandbox"
		}
		result.Active = result.CurrentPeriodEnd == nil ||
			result.CurrentPeriodEnd.After(sourceUpdatedAt)
		result.WillRenew = result.Active && subscription.UnsubscribeDetectedAt == nil
		switch {
		case !result.Active:
			result.Status = "expired"
		case result.Environment == "sandbox" && !c.allowSandbox:
			result.Active = false
			result.WillRenew = false
			result.Status = "inactive"
		case subscription.BillingIssuesDetectedAt != nil:
			result.Status = "billing_issue"
		case subscription.UnsubscribeDetectedAt != nil:
			result.Status = "canceled"
		default:
			result.Status = "active"
		}
		return result, nil
	}

	result.Active = result.CurrentPeriodEnd == nil ||
		result.CurrentPeriodEnd.After(sourceUpdatedAt)
	if result.Active {
		result.Status = "active"
	}
	return result, nil
}

func firstTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			copy := value.UTC()
			return &copy
		}
	}
	return nil
}

func laterTime(values ...*time.Time) *time.Time {
	var result *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		if result == nil || value.After(*result) {
			copy := value.UTC()
			result = &copy
		}
	}
	return result
}

func WebhookEnvironment(value string) string {
	switch strings.ToUpper(value) {
	case "PRODUCTION":
		return "production"
	case "SANDBOX":
		return "sandbox"
	default:
		return "unknown"
	}
}

func WebhookTimestamp(milliseconds int64, fallback time.Time) time.Time {
	if milliseconds <= 0 {
		return fallback.UTC()
	}
	return time.UnixMilli(milliseconds).UTC()
}

func PayloadHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
