package worker

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/aiprovider"
	"github.com/arrorLabArts/heatcheck/internal/billing"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/mailer"
	"github.com/arrorLabArts/heatcheck/internal/media"
	"github.com/arrorLabArts/heatcheck/internal/mediaprocessor"
	"github.com/arrorLabArts/heatcheck/internal/store"
)

type Worker struct {
	store             *store.Store
	storage           *media.Storage
	processor         *mediaprocessor.Processor
	ai                *aiprovider.Client
	mailer            *mailer.Client
	billing           *billing.Client
	logger            *slog.Logger
	id                string
	pollInterval      time.Duration
	jobTimeout        time.Duration
	minConfidence     float64
	accountExportTTL  time.Duration
	safetyAlertEmail  string
	originalRetention time.Duration
}

type Config struct {
	ID                string
	PollInterval      time.Duration
	JobTimeout        time.Duration
	MinConfidence     float64
	AccountExportTTL  time.Duration
	SafetyAlertEmail  string
	OriginalRetention time.Duration
}

func New(
	dataStore *store.Store,
	storage *media.Storage,
	processor *mediaprocessor.Processor,
	ai *aiprovider.Client,
	mailClient *mailer.Client,
	billingClient *billing.Client,
	logger *slog.Logger,
	config Config,
) *Worker {
	return &Worker{
		store:             dataStore,
		storage:           storage,
		processor:         processor,
		ai:                ai,
		mailer:            mailClient,
		billing:           billingClient,
		logger:            logger,
		id:                config.ID,
		pollInterval:      config.PollInterval,
		jobTimeout:        config.JobTimeout,
		minConfidence:     config.MinConfidence,
		accountExportTTL:  config.AccountExportTTL,
		safetyAlertEmail:  config.SafetyAlertEmail,
		originalRetention: config.OriginalRetention,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("worker started", "worker_id", w.id)
	defer w.logger.Info("worker stopped", "worker_id", w.id)
	var lastMaintenance time.Time
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		_ = w.store.HeartbeatWorker(ctx, w.id, "1", "")
		if lastMaintenance.IsZero() || time.Since(lastMaintenance) >= time.Hour {
			if _, err := w.store.EnqueueJob(
				ctx,
				"maintenance.cleanup",
				"",
				"maintenance.cleanup",
				map[string]any{},
				5,
			); err != nil {
				w.logger.Error("enqueue maintenance", "error", err)
			} else {
				lastMaintenance = time.Now()
			}
		}
		job, err := w.store.ClaimJob(ctx, w.id, w.jobTimeout)
		if errors.Is(err, store.ErrNotFound) {
			if err := sleep(ctx, w.pollInterval); err != nil {
				return nil
			}
			continue
		}
		if err != nil {
			w.logger.Error("claim job", "error", err)
			if err := sleep(ctx, w.pollInterval); err != nil {
				return nil
			}
			continue
		}
		_ = w.store.HeartbeatWorker(ctx, w.id, "1", job.ID)
		started := time.Now()
		jobContext, cancel := context.WithTimeout(ctx, w.jobTimeout)
		result, handleErr := w.handle(jobContext, job)
		cancel()
		if handleErr == nil {
			if err := w.store.CompleteJob(ctx, job.ID, w.id, result); err != nil {
				w.logger.Error("complete job", "error", err, "job_id", job.ID)
			} else {
				w.logger.Info(
					"job completed",
					"job_id", job.ID,
					"kind", job.Kind,
					"attempt", job.Attempts,
					"duration_ms", time.Since(started).Milliseconds(),
				)
			}
		} else {
			if job.Attempts >= job.MaxAttempts && job.Kind == "submission.analyze" && job.EntityID != nil {
				if err := w.store.MarkAnalysisForManualReview(
					ctx,
					*job.EntityID,
					"Automated analysis was unavailable after repeated attempts.",
				); err != nil && !errors.Is(err, store.ErrConflict) {
					w.logger.Error("route analysis to manual review", "error", err, "job_id", job.ID)
				}
			}
			if job.Attempts >= job.MaxAttempts && job.Kind == "account.export" && job.EntityID != nil {
				_ = w.store.FailAccountExport(ctx, *job.EntityID, handleErr.Error())
			}
			if err := w.store.FailJob(ctx, job, w.id, handleErr); err != nil {
				w.logger.Error("fail job", "error", err, "job_id", job.ID)
			}
			w.logger.Error(
				"job failed",
				"error", handleErr,
				"job_id", job.ID,
				"kind", job.Kind,
				"attempt", job.Attempts,
			)
		}
		_ = w.store.HeartbeatWorker(ctx, w.id, "1", "")
	}
}

func (w *Worker) handle(ctx context.Context, job domain.Job) (any, error) {
	if strings.HasPrefix(job.Kind, "email.") {
		var message mailer.Message
		if err := w.store.DecodeEmailPayload(job.Payload, &message); err != nil {
			return nil, err
		}
		if err := w.mailer.Send(ctx, message); err != nil {
			return nil, err
		}
		return map[string]string{"status": "sent"}, nil
	}
	switch job.Kind {
	case "submission.analyze":
		if job.EntityID == nil {
			return nil, errors.New("submission analysis job has no entity ID")
		}
		return w.analyzeSubmission(ctx, *job.EntityID)
	case "account.export":
		if job.EntityID == nil {
			return nil, errors.New("account export job has no entity ID")
		}
		return w.exportAccount(ctx, *job.EntityID)
	case "account.delete":
		if job.EntityID == nil {
			return nil, errors.New("account deletion job has no entity ID")
		}
		return w.deleteAccount(ctx, *job.EntityID)
	case "maintenance.cleanup":
		return w.cleanup(ctx)
	default:
		return nil, errors.New("unsupported job kind: " + job.Kind)
	}
}

func (w *Worker) exportAccount(ctx context.Context, exportID string) (any, error) {
	userID, err := w.store.StartAccountExport(ctx, exportID)
	if err != nil {
		return nil, err
	}
	data, mediaFiles, err := w.store.BuildAccountExport(ctx, userID)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "heatcheck-export-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create account export: %w", err)
	}
	filePath := file.Name()
	defer os.Remove(filePath)
	archive := zip.NewWriter(file)
	dataWriter, err := archive.Create("heatcheck-data.json")
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("create export data entry: %w", err)
	}
	if _, err := dataWriter.Write(data); err != nil {
		file.Close()
		return nil, fmt.Errorf("write export data: %w", err)
	}
	for _, mediaFile := range mediaFiles {
		object, err := w.storage.Open(ctx, mediaFile.ObjectKey)
		if err != nil {
			file.Close()
			return nil, err
		}
		entry, err := archive.Create(path.Join(
			"clips",
			mediaFile.SubmissionID+extensionForContentType(mediaFile.ContentType),
		))
		if err == nil {
			_, err = io.Copy(entry, object)
		}
		closeErr := object.Close()
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("write export clip: %w", err)
		}
		if closeErr != nil {
			file.Close()
			return nil, fmt.Errorf("close export clip: %w", closeErr)
		}
	}
	if err := archive.Close(); err != nil {
		file.Close()
		return nil, fmt.Errorf("finish account export: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close account export: %w", err)
	}
	file, err = os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open account export: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat account export: %w", err)
	}
	objectKey := path.Join("exports", userID, exportID+".zip")
	if err := w.storage.Put(ctx, objectKey, file, info.Size(), "application/zip"); err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(w.accountExportTTL)
	if err := w.store.CompleteAccountExport(ctx, exportID, objectKey, expiresAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":     "ready",
		"expires_at": expiresAt,
	}, nil
}

func (w *Worker) deleteAccount(ctx context.Context, userID string) (any, error) {
	keys, err := w.store.AccountDeletionMedia(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := w.billing.DeleteCustomer(ctx, userID); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := w.storage.Remove(ctx, key); err != nil {
			return nil, err
		}
	}
	if err := w.store.CompleteAccountDeletion(ctx, userID); err != nil {
		return nil, err
	}
	return map[string]string{"status": "deleted"}, nil
}

func (w *Worker) cleanup(ctx context.Context) (any, error) {
	objects, err := w.store.ListCleanupObjects(ctx, 500)
	if err != nil {
		return nil, err
	}
	for _, object := range objects {
		if err := w.storage.Remove(ctx, object.ObjectKey); err != nil {
			return nil, err
		}
		if err := w.store.CompleteCleanupObject(ctx, object); err != nil {
			return nil, err
		}
	}
	if err := w.store.CleanupDatabase(ctx); err != nil {
		return nil, err
	}
	return map[string]int{"objects_removed": len(objects)}, nil
}

func extensionForContentType(contentType string) string {
	switch strings.ToLower(contentType) {
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	default:
		return ".bin"
	}
}

func (w *Worker) analyzeSubmission(ctx context.Context, submissionID string) (any, error) {
	input, err := w.store.GetSubmissionAnalysisInput(ctx, submissionID)
	if err != nil {
		return nil, err
	}
	object, err := w.storage.Open(ctx, input.MediaObjectKey)
	if err != nil {
		return nil, err
	}
	defer object.Close()
	inspection, err := w.processor.Process(ctx, object, input.MediaSize)
	if err != nil {
		var rejected *mediaprocessor.RejectedError
		if errors.As(err, &rejected) {
			if updateErr := w.store.RejectMediaUpload(ctx, input.MediaUploadID); updateErr != nil {
				return nil, updateErr
			}
			details := map[string]any{
				"source": "media_validation",
				"reason": rejected.Reason,
			}
			if updateErr := w.store.CompleteAutomatedAnalysis(
				ctx,
				submissionID,
				"failed",
				"rejected",
				details,
			); updateErr != nil {
				return nil, updateErr
			}
			return details, nil
		}
		return nil, err
	}
	processedObjectKey := path.Join("processed", input.UserID, input.MediaUploadID+".mp4")
	thumbnailObjectKey := path.Join("thumbnails", input.UserID, input.MediaUploadID+".jpg")
	if err := w.storage.Put(
		ctx,
		processedObjectKey,
		bytes.NewReader(inspection.ProcessedVideo),
		int64(len(inspection.ProcessedVideo)),
		"video/mp4",
	); err != nil {
		return nil, err
	}
	if err := w.storage.Put(
		ctx,
		thumbnailObjectKey,
		bytes.NewReader(inspection.Thumbnail),
		int64(len(inspection.Thumbnail)),
		"image/jpeg",
	); err != nil {
		w.removeOrphan(processedObjectKey)
		return nil, err
	}
	if err := w.store.UpdateMediaInspection(
		ctx,
		input.MediaUploadID,
		inspection.DurationSeconds,
		inspection.Width,
		inspection.Height,
		inspection.VideoCodec,
		time.Now().UTC(),
		time.Now().UTC().Add(w.originalRetention),
		processedObjectKey,
		thumbnailObjectKey,
	); err != nil {
		w.removeOrphan(processedObjectKey)
		w.removeOrphan(thumbnailObjectKey)
		return nil, err
	}
	analysis, err := w.ai.Analyze(ctx, aiprovider.VerificationInput{
		UserID:               input.UserID,
		ChallengeTitle:       input.ChallengeTitle,
		ChallengeDescription: input.ChallengeDescription,
		ChallengeRules:       input.ChallengeRules,
		Caption:              input.Caption,
		Frames:               inspection.Frames,
	})
	if err != nil {
		return nil, err
	}
	if categories := urgentModerationCategories(analysis.Moderation); len(categories) > 0 {
		body := fmt.Sprintf(
			"OpenAI flagged a private HeatCheck submission for urgent safety review.\n\nSubmission: %s\nCategories: %s\nThe submission has not been published.",
			submissionID,
			strings.Join(categories, ", "),
		)
		if _, err := w.store.EnqueueEmailNotification(
			ctx,
			"email.ai_safety_alert",
			submissionID,
			"email.ai_safety_alert:"+submissionID,
			mailer.Message{
				To:       w.safetyAlertEmail,
				Subject:  "Urgent HeatCheck AI moderation review",
				TextBody: body,
				HTMLBody: "<pre>" + body + "</pre>",
			},
			12,
		); err != nil {
			return nil, err
		}
	}
	verificationStatus := "failed"
	moderationStatus := "approved"
	if analysis.Moderation.Flagged ||
		analysis.Verification.RequiresManualReview ||
		analysis.Verification.Confidence < w.minConfidence {
		verificationStatus = "manual_review"
		moderationStatus = "pending"
	} else if analysis.Verification.ChallengePassed {
		verificationStatus = "passed"
	}
	details := map[string]any{
		"analysis": analysis,
		"media": map[string]any{
			"duration_seconds": inspection.DurationSeconds,
			"width":            inspection.Width,
			"height":           inspection.Height,
			"video_codec":      inspection.VideoCodec,
			"sampled_frames":   len(inspection.Frames),
		},
	}
	if err := w.store.CompleteAutomatedAnalysis(
		ctx,
		submissionID,
		verificationStatus,
		moderationStatus,
		details,
	); err != nil {
		return nil, err
	}
	return map[string]any{
		"verification_status": verificationStatus,
		"moderation_status":   moderationStatus,
	}, nil
}

func (w *Worker) removeOrphan(objectKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.storage.Remove(ctx, objectKey); err != nil {
		w.logger.Error("remove orphaned object", "error", err, "object_key", objectKey)
	}
}

func urgentModerationCategories(moderation aiprovider.Moderation) []string {
	if !moderation.Flagged {
		return nil
	}
	var categories []string
	for _, category := range []string{
		"sexual/minors",
		"self-harm/intent",
		"self-harm/instructions",
		"violence/graphic",
	} {
		if moderation.Categories[category] {
			categories = append(categories, category)
		}
	}
	return categories
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
