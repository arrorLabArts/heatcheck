package store_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/database"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/store"
)

func TestCoreWorkflow(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, pool, logger); err != nil {
		t.Fatal(err)
	}
	dataStore := store.New(pool)

	policies, err := dataStore.ListCurrentPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("got %d seeded policies, want 2", len(policies))
	}
	acceptance := []domain.PolicyAcceptance{{
		Kind: "community_guidelines", Version: "2026-07-24",
	}}
	dateOfBirth := time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC)
	admin := createTestUser(t, ctx, dataStore, "admin@example.test", "admin", domain.RoleAdmin, dateOfBirth, acceptance)
	player := createTestUser(t, ctx, dataStore, "player@example.test", "player", domain.RoleUser, dateOfBirth, acceptance)
	voter := createTestUser(t, ctx, dataStore, "voter@example.test", "voter", domain.RoleUser, dateOfBirth, acceptance)

	now := time.Now().UTC()
	challenge, err := dataStore.CreateChallenge(ctx, store.CreateChallengeParams{
		Slug:        "integration-challenge",
		Title:       "Integration challenge",
		Description: "A challenge used to verify the backend workflow.",
		Rules:       json.RawMessage(`["Submit one proof clip"]`),
		Status:      "published",
		Visibility:  "public",
		StartsAt:    now.Add(-time.Hour),
		EndsAt:      now.Add(time.Hour),
		CreatedBy:   admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	upload, err := dataStore.CreateMediaUpload(ctx, store.CreateMediaUploadParams{
		UserID:       player.ID,
		ObjectKey:    "integration/test.mp4",
		ContentType:  "video/mp4",
		ExpectedSize: 123,
		ExpiresAt:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CompleteMediaUpload(ctx, upload.ID, player.ID, 123); err != nil {
		t.Fatal(err)
	}
	submission, err := dataStore.CreateSubmission(ctx, store.CreateSubmissionParams{
		ChallengeID:   challenge.ID,
		UserID:        player.ID,
		MediaUploadID: upload.ID,
		Caption:       "proof",
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}

	approved, err := dataStore.CreateModerationAction(ctx, store.CreateModerationActionParams{
		ModeratorID: admin.ID,
		TargetType:  "submission",
		TargetID:    submission.ID,
		Action:      "approve",
		Reason:      "meets_guidelines",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = approved

	voted, err := dataStore.Vote(ctx, submission.ID, voter.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if voted.VoteCount != 1 || voted.StyleScore != 5 {
		t.Fatalf("unexpected vote aggregate: count=%d score=%v", voted.VoteCount, voted.StyleScore)
	}

	report, err := dataStore.CreateReport(ctx, store.CreateReportParams{
		ReporterID: voter.ID,
		TargetType: "submission",
		TargetID:   submission.ID,
		Reason:     "copyright",
		Details:    "Potentially copied footage.",
		Priority:   "normal",
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := dataStore.CreateModerationAction(ctx, store.CreateModerationActionParams{
		ModeratorID: admin.ID,
		TargetType:  "submission",
		TargetID:    submission.ID,
		Action:      "remove",
		Reason:      "copyright_review",
		Notes:       "Removed while the report is reviewed.",
		ReportID:    &report.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	appeal, err := dataStore.CreateAppeal(ctx, player.ID, removed.ID, "This is my own recorded gameplay and should be restored.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ReviewAppeal(ctx, appeal.ID, admin.ID, "reversed", "Ownership evidence was accepted."); err != nil {
		t.Fatal(err)
	}
	restored, err := dataStore.GetSubmission(ctx, submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ModerationStatus != "approved" {
		t.Fatalf("got moderation status %q after appeal reversal", restored.ModerationStatus)
	}

	notice, err := dataStore.CreateCopyrightNotice(ctx, store.CreateCopyrightNoticeParams{
		ClaimantName:    "Rights Holder",
		ClaimantEmail:   "rights@example.test",
		ClaimantAddress: "1 Rights Street",
		Relationship:    "owner",
		CopyrightedWork: "An identified audiovisual work",
		InfringingURL:   "https://heatcheck.example/submissions/" + submission.ID,
		SubmissionID:    &submission.ID,
		GoodFaith:       true,
		Accuracy:        true,
		Signature:       "Rights Holder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ReviewCopyrightNotice(ctx, store.ReviewCopyrightNoticeParams{
		NoticeID:       notice.ID,
		ActorID:        admin.ID,
		Status:         "actioned",
		ResolutionNote: "Restricted pending counter-notice.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateCounterNotice(ctx, store.CreateCounterNoticeParams{
		NoticeID:         notice.ID,
		UserID:           player.ID,
		FullName:         "Test Player",
		Address:          "2 Player Street",
		Phone:            "+1 555 0100",
		Email:            "player@example.test",
		GoodFaith:        true,
		ConsentToProcess: true,
		Signature:        "Test Player",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ReviewCopyrightNotice(ctx, store.ReviewCopyrightNoticeParams{
		NoticeID:       notice.ID,
		ActorID:        admin.ID,
		Status:         "restored",
		ResolutionNote: "Counter-notice accepted and claim closed.",
	}); err != nil {
		t.Fatal(err)
	}

	newPolicy, err := dataStore.PublishPolicy(ctx, store.PublishPolicyParams{
		Kind:               "community_guidelines",
		Version:            "2026-08-01",
		Title:              "Updated Community Guidelines",
		Content:            "Updated integration-test policy content.",
		RequiresAcceptance: true,
		EffectiveAt:        now,
		ActorID:            admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := dataStore.MissingRequiredPolicies(ctx, player.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Version != newPolicy.Version {
		t.Fatalf("unexpected missing policies: %#v", missing)
	}
	if err := dataStore.AcceptPolicies(ctx, player.ID, []domain.PolicyAcceptance{{
		Kind: newPolicy.Kind, Version: newPolicy.Version,
	}}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	missing, err = dataStore.MissingRequiredPolicies(ctx, player.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("expected no missing policies, got %#v", missing)
	}
}

func createTestUser(
	t *testing.T,
	ctx context.Context,
	dataStore *store.Store,
	email string,
	handle string,
	role string,
	dateOfBirth time.Time,
	acceptances []domain.PolicyAcceptance,
) domain.User {
	t.Helper()
	user, err := dataStore.CreateUser(ctx, store.CreateUserParams{
		Email:        email,
		PasswordHash: "test-password-hash",
		Handle:       handle,
		DisplayName:  handle,
		DateOfBirth:  dateOfBirth,
		Role:         role,
		Acceptances:  acceptances,
		IPAddress:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return user
}
