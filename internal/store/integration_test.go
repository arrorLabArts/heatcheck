package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/auth"
	"github.com/arrorLabArts/heatcheck/internal/database"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/mailer"
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
	if len(policies) != 3 {
		t.Fatalf("got %d seeded policies, want 3", len(policies))
	}
	acceptance := []domain.PolicyAcceptance{{
		Kind: "community_guidelines", Version: "2026-07-24",
	}}
	dateOfBirth := time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC)
	admin := createTestUser(t, ctx, dataStore, "admin@example.test", "admin", domain.RoleAdmin, dateOfBirth, acceptance)
	player := createTestUser(t, ctx, dataStore, "player@example.test", "player", domain.RoleUser, dateOfBirth, acceptance)
	voter := createTestUser(t, ctx, dataStore, "voter@example.test", "voter", domain.RoleUser, dateOfBirth, acceptance)

	now := time.Now().UTC()
	if err := dataStore.SyncSubscription(ctx, domain.Subscription{
		UserID:             player.ID,
		Tier:               "pro",
		Provider:           "revenuecat",
		EntitlementID:      "pro",
		ProductID:          "heatcheck_pro_monthly",
		Store:              "play_store",
		Environment:        "production",
		Status:             "active",
		Active:             true,
		WillRenew:          true,
		CurrentPeriodStart: timePointer(now.Add(-time.Hour)),
		CurrentPeriodEnd:   timePointer(now.Add(30 * 24 * time.Hour)),
		SourceUpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	overview, err := dataStore.GetSubscriptionOverview(
		ctx,
		player.ID,
		"pro",
		1,
		30,
		now,
	)
	if err != nil || !overview.Subscription.Active ||
		overview.Usage.DailyRemaining != 1 {
		t.Fatalf("subscription overview=%#v err=%v", overview, err)
	}
	if err := dataStore.ApplyRevenueCatEvent(
		ctx,
		store.BillingEventParams{
			ExternalEventID: "integration-billing-event",
			EventType:       "EXPIRATION",
			UserID:          player.ID,
			Environment:     "production",
			EventTimestamp:  now,
			PayloadSHA256:   "integration-hash",
			Metadata:        map[string]any{"product_id": "heatcheck_pro_monthly"},
		},
		domain.Subscription{
			UserID:          player.ID,
			Tier:            "pro",
			Provider:        "revenuecat",
			EntitlementID:   "pro",
			ProductID:       "heatcheck_pro_monthly",
			Store:           "play_store",
			Environment:     "production",
			Status:          "expired",
			Active:          false,
			SourceUpdatedAt: now.Add(-time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	overview, err = dataStore.GetSubscriptionOverview(ctx, player.ID, "pro", 1, 30, now)
	if err != nil || !overview.Subscription.Active {
		t.Fatalf("older billing snapshot regressed access: overview=%#v err=%v", overview, err)
	}
	if err := dataStore.SyncSubscription(ctx, domain.Subscription{
		UserID:          voter.ID,
		Tier:            "none",
		Provider:        "revenuecat",
		EntitlementID:   "pro",
		Environment:     "production",
		Status:          "inactive",
		SourceUpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	unpaidOverview, err := dataStore.GetSubscriptionOverview(
		ctx,
		voter.ID,
		"pro",
		1,
		30,
		now,
	)
	if err != nil || unpaidOverview.Subscription.Tier != "none" {
		t.Fatalf("unpaid subscription overview=%#v err=%v", unpaidOverview, err)
	}
	if _, err := dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       voter.ID,
			ObjectKey:    "integration/unpaid.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    now.Add(time.Hour),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 100,
		Now:              now,
	}); err != store.ErrSubscriptionRequired {
		t.Fatalf("unpaid upload error = %v, want ErrSubscriptionRequired", err)
	}
	concurrentUser := createTestUser(
		t,
		ctx,
		dataStore,
		"concurrent@example.test",
		"concurrent",
		domain.RoleUser,
		dateOfBirth,
		acceptance,
	)
	if err := dataStore.SyncSubscription(ctx, domain.Subscription{
		UserID:             concurrentUser.ID,
		Tier:               "pro",
		Provider:           "revenuecat",
		EntitlementID:      "pro",
		ProductID:          "heatcheck_pro_monthly",
		Store:              "app_store",
		Environment:        "production",
		Status:             "active",
		Active:             true,
		WillRenew:          true,
		CurrentPeriodStart: timePointer(now.Add(-time.Hour)),
		CurrentPeriodEnd:   timePointer(now.Add(30 * 24 * time.Hour)),
		SourceUpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	type uploadResult struct {
		upload domain.MediaUpload
		err    error
	}
	results := make(chan uploadResult, 2)
	for _, objectKey := range []string{
		"integration/concurrent-1.mp4",
		"integration/concurrent-2.mp4",
	} {
		objectKey := objectKey
		go func() {
			upload, err := dataStore.CreatePaidMediaUpload(
				ctx,
				store.CreatePaidMediaUploadParams{
					CreateMediaUploadParams: store.CreateMediaUploadParams{
						UserID:       concurrentUser.ID,
						ObjectKey:    objectKey,
						ContentType:  "video/mp4",
						ExpectedSize: 123,
						ExpiresAt:    now.Add(time.Hour),
					},
					EntitlementID:    "pro",
					DailyLimit:       1,
					MonthlyLimit:     30,
					GlobalDailyLimit: 100,
					Now:              now,
				},
			)
			results <- uploadResult{upload: upload, err: err}
		}()
	}
	var reservedUpload domain.MediaUpload
	var successful, limited int
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successful++
			reservedUpload = result.upload
		case errors.Is(result.err, store.ErrUsageLimit):
			limited++
		default:
			t.Fatalf("concurrent paid upload error = %v", result.err)
		}
	}
	if successful != 1 || limited != 1 {
		t.Fatalf("concurrent uploads succeeded=%d limited=%d", successful, limited)
	}
	if err := dataStore.ReleaseUploadReservation(
		ctx,
		reservedUpload.ID,
		concurrentUser.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       concurrentUser.ID,
			ObjectKey:    "integration/concurrent-retry.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    now.Add(time.Hour),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 100,
		Now:              now,
	}); err != nil {
		t.Fatalf("released reservation did not restore allowance: %v", err)
	}
	boundaryUser := createTestUser(
		t,
		ctx,
		dataStore,
		"boundary@example.test",
		"boundary",
		domain.RoleUser,
		dateOfBirth,
		acceptance,
	)
	if err := dataStore.SyncSubscription(ctx, domain.Subscription{
		UserID:             boundaryUser.ID,
		Tier:               "pro",
		Provider:           "revenuecat",
		EntitlementID:      "pro",
		ProductID:          "heatcheck_pro_monthly",
		Store:              "app_store",
		Environment:        "production",
		Status:             "active",
		Active:             true,
		WillRenew:          true,
		CurrentPeriodStart: timePointer(now.Add(-time.Hour)),
		CurrentPeriodEnd:   timePointer(now.Add(30 * 24 * time.Hour)),
		SourceUpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	beforeMidnight := time.Date(
		now.Year(), now.Month(), now.Day(), 23, 50, 0, 0, time.UTC,
	)
	boundaryUpload, err := dataStore.CreatePaidMediaUpload(
		ctx,
		store.CreatePaidMediaUploadParams{
			CreateMediaUploadParams: store.CreateMediaUploadParams{
				UserID:       boundaryUser.ID,
				ObjectKey:    "integration/boundary-1.mp4",
				ContentType:  "video/mp4",
				ExpectedSize: 123,
				ExpiresAt:    beforeMidnight.Add(time.Hour),
			},
			EntitlementID:    "pro",
			DailyLimit:       1,
			MonthlyLimit:     30,
			GlobalDailyLimit: 100,
			Now:              beforeMidnight,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       boundaryUser.ID,
			ObjectKey:    "integration/boundary-2.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    beforeMidnight.Add(80 * time.Minute),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 100,
		Now:              beforeMidnight.Add(20 * time.Minute),
	})
	if !errors.Is(err, store.ErrUsageLimit) {
		t.Fatalf("cross-midnight reservation error = %v, want ErrUsageLimit", err)
	}
	if err := dataStore.ReleaseUploadReservation(
		ctx,
		boundaryUpload.ID,
		boundaryUser.ID,
	); err != nil {
		t.Fatal(err)
	}
	globalNow := now.AddDate(0, 0, 1)
	var globalUsers []domain.User
	for _, account := range []struct {
		email  string
		handle string
	}{
		{email: "global1@example.test", handle: "global_one"},
		{email: "global2@example.test", handle: "global_two"},
	} {
		user := createTestUser(
			t,
			ctx,
			dataStore,
			account.email,
			account.handle,
			domain.RoleUser,
			dateOfBirth,
			acceptance,
		)
		if err := dataStore.SyncSubscription(ctx, domain.Subscription{
			UserID:             user.ID,
			Tier:               "pro",
			Provider:           "revenuecat",
			EntitlementID:      "pro",
			ProductID:          "heatcheck_pro_monthly",
			Store:              "play_store",
			Environment:        "production",
			Status:             "active",
			Active:             true,
			WillRenew:          true,
			CurrentPeriodStart: timePointer(now),
			CurrentPeriodEnd:   timePointer(now.Add(30 * 24 * time.Hour)),
			SourceUpdatedAt:    now,
		}); err != nil {
			t.Fatal(err)
		}
		globalUsers = append(globalUsers, user)
	}
	if _, err := dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       globalUsers[0].ID,
			ObjectKey:    "integration/global-1.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    globalNow.Add(time.Hour),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 1,
		Now:              globalNow,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       globalUsers[1].ID,
			ObjectKey:    "integration/global-2.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    globalNow.Add(time.Hour),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 1,
		Now:              globalNow,
	})
	var globalLimit *store.UsageLimitError
	if !errors.As(err, &globalLimit) || globalLimit.Period != "global_daily" {
		t.Fatalf("global capacity error = %v, want global_daily UsageLimitError", err)
	}
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

	upload, err := dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       player.ID,
			ObjectKey:    "integration/test.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    now.Add(time.Hour),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 100,
		Now:              now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CompletePaidMediaUpload(
		ctx,
		upload.ID,
		player.ID,
		123,
		now.Add(24*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SyncSubscription(ctx, domain.Subscription{
		UserID:             player.ID,
		Tier:               "pro",
		Provider:           "revenuecat",
		EntitlementID:      "pro",
		ProductID:          "heatcheck_pro_monthly",
		Store:              "play_store",
		Environment:        "production",
		Status:             "expired",
		Active:             false,
		CurrentPeriodStart: timePointer(now.Add(-time.Hour)),
		CurrentPeriodEnd:   timePointer(now),
		SourceUpdatedAt:    now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateSubmission(ctx, store.CreateSubmissionParams{
		ChallengeID:   challenge.ID,
		UserID:        player.ID,
		MediaUploadID: upload.ID,
		Caption:       "proof",
		Now:           now,
	}); err != store.ErrSubscriptionRequired {
		t.Fatalf("submission after entitlement revocation error = %v, want ErrSubscriptionRequired", err)
	}
	if err := dataStore.SyncSubscription(ctx, domain.Subscription{
		UserID:             player.ID,
		Tier:               "pro",
		Provider:           "revenuecat",
		EntitlementID:      "pro",
		ProductID:          "heatcheck_pro_monthly",
		Store:              "play_store",
		Environment:        "production",
		Status:             "active",
		Active:             true,
		WillRenew:          true,
		CurrentPeriodStart: timePointer(now),
		CurrentPeriodEnd:   timePointer(now.Add(30 * 24 * time.Hour)),
		SourceUpdatedAt:    now.Add(2 * time.Minute),
	}); err != nil {
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
	if _, err := dataStore.CreatePaidMediaUpload(ctx, store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       player.ID,
			ObjectKey:    "integration/over-limit.mp4",
			ContentType:  "video/mp4",
			ExpectedSize: 123,
			ExpiresAt:    now.Add(time.Hour),
		},
		EntitlementID:    "pro",
		DailyLimit:       1,
		MonthlyLimit:     30,
		GlobalDailyLimit: 100,
		Now:              now,
	}); !errors.Is(err, store.ErrUsageLimit) {
		t.Fatalf("over-limit upload error = %v, want ErrUsageLimit", err)
	}
	if _, err := dataStore.Vote(ctx, submission.ID, voter.ID, 5); err != store.ErrForbidden {
		t.Fatalf("vote on unverified submission error = %v, want ErrForbidden", err)
	}
	analysisJob, err := dataStore.ClaimJob(ctx, "integration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if analysisJob.Kind != "submission.analyze" ||
		analysisJob.EntityID == nil ||
		*analysisJob.EntityID != submission.ID {
		t.Fatalf("unexpected analysis job: %#v", analysisJob)
	}
	if err := dataStore.CompleteJob(ctx, analysisJob.ID, "integration-worker", map[string]string{"status": "tested"}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CompleteAutomatedAnalysis(
		ctx,
		submission.ID,
		"passed",
		"approved",
		map[string]string{"source": "integration_test"},
	); err != nil {
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
	stats, err := dataStore.GetPublicUserStats(ctx, player.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SubmissionCount != 1 || stats.ChallengeWins != 1 ||
		stats.CurrentStreak != 1 || stats.BestStreak != 1 ||
		stats.AverageScore != 5 {
		t.Fatalf("unexpected public user stats: %#v", stats)
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
	recipients, err := dataStore.GetCopyrightRecipients(ctx, notice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recipients.ClaimantEmail != "rights@example.test" ||
		recipients.UploaderEmail != player.Email {
		t.Fatalf("unexpected copyright recipients: %#v", recipients)
	}
	if _, err := dataStore.ReviewCopyrightNotice(ctx, store.ReviewCopyrightNoticeParams{
		NoticeID:       notice.ID,
		ActorID:        admin.ID,
		Status:         "actioned",
		ResolutionNote: "Restricted pending counter-notice.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ReviewCopyrightNotice(ctx, store.ReviewCopyrightNoticeParams{
		NoticeID:       notice.ID,
		ActorID:        admin.ID,
		Status:         "reviewing",
		ResolutionNote: "An actioned notice cannot return to review.",
	}); err != store.ErrInvalid {
		t.Fatalf("invalid copyright transition error = %v, want ErrInvalid", err)
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

	verificationToken, verificationHash, err := auth.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	message := mailer.Message{
		To:       player.Email,
		Subject:  "Verify",
		TextBody: verificationToken,
		HTMLBody: verificationToken,
	}
	if err := dataStore.StartEmailVerification(
		ctx,
		player.ID,
		verificationHash,
		now.Add(time.Hour),
		message,
	); err != nil {
		t.Fatal(err)
	}
	emailJob, err := dataStore.ClaimJob(ctx, "integration-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var decodedMessage mailer.Message
	if err := dataStore.DecodeEmailPayload(emailJob.Payload, &decodedMessage); err != nil {
		t.Fatal(err)
	}
	if decodedMessage.To != player.Email {
		t.Fatalf("decoded email recipient = %q", decodedMessage.To)
	}
	if err := dataStore.CompleteJob(ctx, emailJob.ID, "integration-worker", map[string]string{"status": "sent"}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.VerifyEmail(ctx, auth.HashOpaqueToken(verificationToken)); err != nil {
		t.Fatal(err)
	}
	verifiedPlayer, err := dataStore.GetUserByID(ctx, player.ID)
	if err != nil || verifiedPlayer.EmailVerifiedAt == nil {
		t.Fatalf("email verification failed: user=%#v err=%v", verifiedPlayer, err)
	}
	promotedPlayer, err := dataStore.PromoteUserToAdmin(ctx, player.Email)
	if err != nil {
		t.Fatal(err)
	}
	if promotedPlayer.Role != domain.RoleAdmin {
		t.Fatalf("promoted user role = %q, want admin", promotedPlayer.Role)
	}

	refreshToken, refreshHash, err := auth.NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CreateRefreshToken(
		ctx,
		player.ID,
		refreshHash,
		now.Add(time.Hour),
		"integration-client",
		"127.0.0.1",
	); err != nil {
		t.Fatal(err)
	}
	sessions, err := dataStore.ListSessions(ctx, player.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	if err := dataStore.RevokeRefreshToken(ctx, auth.HashRefreshToken(refreshToken)); err != nil {
		t.Fatal(err)
	}

	if _, err := dataStore.AllowRateLimit(ctx, "integration", 1, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.AllowRateLimit(ctx, "integration", 1, time.Minute, now); err != store.ErrRateLimit {
		t.Fatalf("second rate limit error = %v, want ErrRateLimit", err)
	}

	accountExport, err := dataStore.CreateAccountExport(ctx, player.ID)
	if err != nil || accountExport.Status != "pending" {
		t.Fatalf("account export=%#v err=%v", accountExport, err)
	}
	exportUserID, err := dataStore.StartAccountExport(ctx, accountExport.ID)
	if err != nil || exportUserID != player.ID {
		t.Fatalf("export user=%q err=%v", exportUserID, err)
	}
	exportData, exportMedia, err := dataStore.BuildAccountExport(ctx, player.ID)
	if err != nil || !json.Valid(exportData) || len(exportMedia) != 1 {
		t.Fatalf("export data valid=%v media=%#v err=%v", json.Valid(exportData), exportMedia, err)
	}
	if err := dataStore.CompleteAccountExport(
		ctx,
		accountExport.ID,
		"exports/integration.zip",
		now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.UpdateMediaInspection(
		ctx,
		upload.ID,
		20,
		1920,
		1080,
		"h264",
		now,
		now.Add(-time.Second),
		"processed/integration.mp4",
		"thumbnails/integration.jpg",
	); err != nil {
		t.Fatal(err)
	}
	cleanupObjects, err := dataStore.ListCleanupObjects(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var originalCleanup *store.CleanupObject
	for index := range cleanupObjects {
		if cleanupObjects[index].Kind == "original" &&
			cleanupObjects[index].ID == upload.ID {
			originalCleanup = &cleanupObjects[index]
			break
		}
	}
	if originalCleanup == nil {
		t.Fatalf("original media cleanup not returned: %#v", cleanupObjects)
	}
	if err := dataStore.CompleteCleanupObject(ctx, *originalCleanup); err != nil {
		t.Fatal(err)
	}
	processedSubmission, err := dataStore.GetSubmission(ctx, submission.ID)
	if err != nil || processedSubmission.MediaObjectKey != "processed/integration.mp4" {
		t.Fatalf("processed submission=%#v err=%v", processedSubmission, err)
	}
	if _, err := dataStore.RequestAccountDeletion(ctx, voter.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CancelAccountDeletion(ctx, voter.ID); err != nil {
		t.Fatal(err)
	}
	deleteUser := createTestUser(
		t,
		ctx,
		dataStore,
		"delete@example.test",
		"delete_me",
		domain.RoleUser,
		dateOfBirth,
		[]domain.PolicyAcceptance{{
			Kind: newPolicy.Kind, Version: newPolicy.Version,
		}},
	)
	if _, err := dataStore.RequestAccountDeletion(ctx, deleteUser.ID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'running', locked_by = 'integration-worker', locked_at = now()
		WHERE dedupe_key = 'account.delete:' || ($1::uuid)::text
	`, deleteUser.ID); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CancelAccountDeletion(ctx, deleteUser.ID); err != store.ErrForbidden {
		t.Fatalf("running account deletion cancellation error = %v, want ErrForbidden", err)
	}
	if err := dataStore.CompleteAccountDeletion(ctx, deleteUser.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.GetUserByID(ctx, deleteUser.ID); err != store.ErrNotFound {
		t.Fatalf("deleted user lookup error = %v, want ErrNotFound", err)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
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
