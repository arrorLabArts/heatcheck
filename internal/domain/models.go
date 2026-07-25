package domain

import (
	"encoding/json"
	"time"
)

const (
	RoleUser      = "user"
	RoleModerator = "moderator"
	RoleAdmin     = "admin"
)

type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email,omitempty"`
	Handle          string     `json:"handle"`
	DisplayName     string     `json:"display_name"`
	DateOfBirth     string     `json:"date_of_birth,omitempty"`
	Role            string     `json:"role,omitempty"`
	Status          string     `json:"status,omitempty"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type UserWithPassword struct {
	User
	PasswordHash string
}

type Policy struct {
	Kind               string    `json:"kind"`
	Version            string    `json:"version"`
	Title              string    `json:"title"`
	Content            string    `json:"content"`
	RequiresAcceptance bool      `json:"requires_acceptance"`
	EffectiveAt        time.Time `json:"effective_at"`
}

type PolicyAcceptance struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

type Challenge struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	Status      string          `json:"status"`
	Visibility  string          `json:"visibility"`
	StartsAt    time.Time       `json:"starts_at"`
	EndsAt      time.Time       `json:"ends_at"`
	CreatedBy   string          `json:"created_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type MediaUpload struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id,omitempty"`
	ObjectKey       string     `json:"object_key,omitempty"`
	ContentType     string     `json:"content_type"`
	ExpectedSize    int64      `json:"expected_size"`
	ActualSize      *int64     `json:"actual_size,omitempty"`
	DurationSeconds *float64   `json:"duration_seconds,omitempty"`
	Width           *int       `json:"width,omitempty"`
	Height          *int       `json:"height,omitempty"`
	VideoCodec      string     `json:"video_codec,omitempty"`
	ScannedAt       *time.Time `json:"scanned_at,omitempty"`
	Status          string     `json:"status"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Subscription struct {
	UserID             string     `json:"-"`
	Tier               string     `json:"tier"`
	Provider           string     `json:"provider,omitempty"`
	EntitlementID      string     `json:"entitlement_id"`
	ProductID          string     `json:"product_id,omitempty"`
	Store              string     `json:"store,omitempty"`
	Environment        string     `json:"environment,omitempty"`
	Status             string     `json:"status"`
	Active             bool       `json:"active"`
	WillRenew          bool       `json:"will_renew"`
	CurrentPeriodStart *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end,omitempty"`
	ManagementURL      string     `json:"management_url,omitempty"`
	SourceUpdatedAt    time.Time  `json:"-"`
	UpdatedAt          time.Time  `json:"updated_at,omitempty"`
}

type SubscriptionUsage struct {
	DailyLimit       int       `json:"daily_limit"`
	DailyUsed        int       `json:"daily_used"`
	DailyRemaining   int       `json:"daily_remaining"`
	DailyResetsAt    time.Time `json:"daily_resets_at"`
	MonthlyLimit     int       `json:"monthly_limit"`
	MonthlyUsed      int       `json:"monthly_used"`
	MonthlyRemaining int       `json:"monthly_remaining"`
	MonthlyResetAt   time.Time `json:"monthly_resets_at"`
}

type SubscriptionOverview struct {
	AppUserID    string            `json:"app_user_id"`
	Subscription Subscription      `json:"subscription"`
	Usage        SubscriptionUsage `json:"usage"`
}

type Submission struct {
	ID                  string          `json:"id"`
	ChallengeID         string          `json:"challenge_id"`
	UserID              string          `json:"user_id"`
	UserHandle          string          `json:"user_handle,omitempty"`
	MediaUploadID       string          `json:"media_upload_id,omitempty"`
	ClipURL             string          `json:"clip_url,omitempty"`
	ThumbnailURL        string          `json:"thumbnail_url,omitempty"`
	Caption             string          `json:"caption"`
	VerificationStatus  string          `json:"verification_status"`
	VerificationDetails json.RawMessage `json:"verification_details,omitempty"`
	ModerationStatus    string          `json:"moderation_status"`
	StyleScore          float64         `json:"style_score"`
	VoteCount           int             `json:"vote_count"`
	PublishedAt         *time.Time      `json:"published_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	MediaObjectKey      string          `json:"-"`
	MediaThumbnailKey   string          `json:"-"`
}

type Report struct {
	ID             string     `json:"id"`
	ReporterID     string     `json:"reporter_id"`
	TargetType     string     `json:"target_type"`
	TargetID       string     `json:"target_id"`
	Reason         string     `json:"reason"`
	Details        string     `json:"details"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	AssignedTo     *string    `json:"assigned_to,omitempty"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

type ModerationAction struct {
	ID          string          `json:"id"`
	ModeratorID string          `json:"moderator_id"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Action      string          `json:"action"`
	Reason      string          `json:"reason"`
	Notes       string          `json:"notes,omitempty"`
	ReportID    *string         `json:"report_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Appeal struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	ActionID       string     `json:"action_id"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	ReviewedBy     *string    `json:"reviewed_by,omitempty"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

type CopyrightNotice struct {
	ID               string     `json:"id"`
	ClaimantName     string     `json:"claimant_name"`
	ClaimantEmail    string     `json:"claimant_email"`
	ClaimantAddress  string     `json:"claimant_address"`
	Relationship     string     `json:"relationship"`
	CopyrightedWork  string     `json:"copyrighted_work"`
	InfringingURL    string     `json:"infringing_url"`
	SubmissionID     *string    `json:"submission_id,omitempty"`
	GoodFaith        bool       `json:"good_faith"`
	Accuracy         bool       `json:"accuracy"`
	Signature        string     `json:"signature"`
	Status           string     `json:"status"`
	ResolutionNote   string     `json:"resolution_note,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ActionedAt       *time.Time `json:"actioned_at,omitempty"`
	CounterNoticeDue *time.Time `json:"counter_notice_due,omitempty"`
}

type CopyrightCounterNotice struct {
	ID               string    `json:"id"`
	NoticeID         string    `json:"notice_id"`
	UserID           string    `json:"user_id"`
	FullName         string    `json:"full_name"`
	Address          string    `json:"address"`
	Phone            string    `json:"phone"`
	Email            string    `json:"email"`
	GoodFaith        bool      `json:"good_faith"`
	ConsentToProcess bool      `json:"consent_to_process"`
	Signature        string    `json:"signature"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
}

type AuditEvent struct {
	ID         int64           `json:"id"`
	ActorID    *string         `json:"actor_id,omitempty"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Job struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	EntityID    *string         `json:"entity_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
}

type Session struct {
	ID         string     `json:"id"`
	UserAgent  string     `json:"user_agent"`
	IPAddress  string     `json:"ip_address,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
}

type AccountExport struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	DownloadURL string     `json:"download_url,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ObjectKey   string     `json:"-"`
}

type PublicUserStats struct {
	SubmissionCount int     `json:"submission_count"`
	ChallengeWins   int     `json:"challenge_wins"`
	CurrentStreak   int     `json:"current_streak"`
	BestStreak      int     `json:"best_streak"`
	AverageScore    float64 `json:"average_score"`
}

type LeaderboardEntry struct {
	Rank       int        `json:"rank"`
	Submission Submission `json:"submission"`
}
