package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

type CreateCopyrightNoticeParams struct {
	ClaimantName    string
	ClaimantEmail   string
	ClaimantAddress string
	Relationship    string
	CopyrightedWork string
	InfringingURL   string
	SubmissionID    *string
	GoodFaith       bool
	Accuracy        bool
	Signature       string
}

func scanCopyrightNotice(row scanner) (domain.CopyrightNotice, error) {
	var notice domain.CopyrightNotice
	err := row.Scan(
		&notice.ID,
		&notice.ClaimantName,
		&notice.ClaimantEmail,
		&notice.ClaimantAddress,
		&notice.Relationship,
		&notice.CopyrightedWork,
		&notice.InfringingURL,
		&notice.SubmissionID,
		&notice.GoodFaith,
		&notice.Accuracy,
		&notice.Signature,
		&notice.Status,
		&notice.ResolutionNote,
		&notice.CreatedAt,
		&notice.UpdatedAt,
		&notice.ActionedAt,
		&notice.CounterNoticeDue,
	)
	return notice, mapError(err)
}

func (s *Store) CreateCopyrightNotice(
	ctx context.Context,
	params CreateCopyrightNoticeParams,
) (domain.CopyrightNotice, error) {
	return scanCopyrightNotice(s.pool.QueryRow(ctx, `
		INSERT INTO copyright_notices (
			claimant_name, claimant_email, claimant_address, relationship,
			copyrighted_work, infringing_url, submission_id,
			good_faith, accuracy, signature
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING
			id, claimant_name, claimant_email, claimant_address,
			relationship, copyrighted_work, infringing_url, submission_id,
			good_faith, accuracy, signature, status, resolution_note,
			created_at, updated_at, actioned_at, counter_notice_due
	`,
		params.ClaimantName,
		params.ClaimantEmail,
		params.ClaimantAddress,
		params.Relationship,
		params.CopyrightedWork,
		params.InfringingURL,
		params.SubmissionID,
		params.GoodFaith,
		params.Accuracy,
		params.Signature,
	))
}

func (s *Store) ListCopyrightNotices(
	ctx context.Context,
	status string,
	limit int,
	offset int,
) ([]domain.CopyrightNotice, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id, claimant_name, claimant_email, claimant_address,
			relationship, copyrighted_work, infringing_url, submission_id,
			good_faith, accuracy, signature, status, resolution_note,
			created_at, updated_at, actioned_at, counter_notice_due
		FROM copyright_notices
		WHERE $1 = '' OR status = $1
		ORDER BY created_at
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var notices []domain.CopyrightNotice
	for rows.Next() {
		notice, err := scanCopyrightNotice(rows)
		if err != nil {
			return nil, err
		}
		notices = append(notices, notice)
	}
	return notices, mapError(rows.Err())
}

type ReviewCopyrightNoticeParams struct {
	NoticeID         string
	ActorID          string
	Status           string
	ResolutionNote   string
	CounterNoticeDue *time.Time
}

func (s *Store) ReviewCopyrightNotice(
	ctx context.Context,
	params ReviewCopyrightNoticeParams,
) (domain.CopyrightNotice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CopyrightNotice{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var submissionID *string
	var currentStatus string
	err = tx.QueryRow(ctx, `
		SELECT submission_id, status
		FROM copyright_notices
		WHERE id = $1
		FOR UPDATE
	`, params.NoticeID).Scan(&submissionID, &currentStatus)
	if err != nil {
		return domain.CopyrightNotice{}, mapError(err)
	}

	switch params.Status {
	case "reviewing", "rejected", "closed":
	case "actioned":
		if submissionID != nil {
			command, err := tx.Exec(ctx, `
				UPDATE submissions
				SET moderation_status = 'removed', updated_at = now()
				WHERE id = $1
			`, *submissionID)
			if err != nil {
				return domain.CopyrightNotice{}, mapError(err)
			}
			if command.RowsAffected() == 0 {
				return domain.CopyrightNotice{}, ErrNotFound
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO moderation_actions (
					moderator_id, target_type, target_id, action, reason, notes
				)
				VALUES ($1, 'submission', $2, 'remove', 'copyright', $3)
			`, params.ActorID, *submissionID, params.ResolutionNote); err != nil {
				return domain.CopyrightNotice{}, mapError(err)
			}
		}
	case "restored":
		if submissionID == nil {
			return domain.CopyrightNotice{}, ErrInvalid
		}
		command, err := tx.Exec(ctx, `
			UPDATE submissions
			SET moderation_status = 'approved',
			    published_at = COALESCE(published_at, now()),
			    updated_at = now()
			WHERE id = $1
		`, *submissionID)
		if err != nil {
			return domain.CopyrightNotice{}, mapError(err)
		}
		if command.RowsAffected() == 0 {
			return domain.CopyrightNotice{}, ErrNotFound
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO moderation_actions (
				moderator_id, target_type, target_id, action, reason, notes
			)
			VALUES ($1, 'submission', $2, 'restore', 'copyright_claim_resolved', $3)
		`, params.ActorID, *submissionID, params.ResolutionNote); err != nil {
			return domain.CopyrightNotice{}, mapError(err)
		}
	default:
		return domain.CopyrightNotice{}, ErrInvalid
	}

	notice, err := scanCopyrightNotice(tx.QueryRow(ctx, `
		UPDATE copyright_notices
		SET status = $2,
		    resolution_note = $3,
		    actioned_at = CASE WHEN $2 = 'actioned' THEN now() ELSE actioned_at END,
		    counter_notice_due = $4,
		    updated_at = now()
		WHERE id = $1
		RETURNING
			id, claimant_name, claimant_email, claimant_address,
			relationship, copyrighted_work, infringing_url, submission_id,
			good_faith, accuracy, signature, status, resolution_note,
			created_at, updated_at, actioned_at, counter_notice_due
	`, params.NoticeID, params.Status, params.ResolutionNote, params.CounterNoticeDue))
	if err != nil {
		return domain.CopyrightNotice{}, err
	}

	metadata, _ := json.Marshal(map[string]string{
		"from_status": currentStatus,
		"to_status":   params.Status,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			actor_id, action, entity_type, entity_id, metadata
		)
		VALUES ($1, 'copyright.reviewed', 'copyright_notice', $2, $3)
	`, params.ActorID, params.NoticeID, metadata); err != nil {
		return domain.CopyrightNotice{}, mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.CopyrightNotice{}, mapError(err)
	}
	return notice, nil
}

type CreateCounterNoticeParams struct {
	NoticeID         string
	UserID           string
	FullName         string
	Address          string
	Phone            string
	Email            string
	GoodFaith        bool
	ConsentToProcess bool
	Signature        string
}

func (s *Store) CreateCounterNotice(
	ctx context.Context,
	params CreateCounterNoticeParams,
) (domain.CopyrightCounterNotice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var ownsSubmission bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM copyright_notices n
			JOIN submissions s ON s.id = n.submission_id
			WHERE n.id = $1
			  AND n.status = 'actioned'
			  AND s.user_id = $2
		)
	`, params.NoticeID, params.UserID).Scan(&ownsSubmission)
	if err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}
	if !ownsSubmission {
		return domain.CopyrightCounterNotice{}, ErrForbidden
	}

	var counter domain.CopyrightCounterNotice
	err = tx.QueryRow(ctx, `
		INSERT INTO copyright_counter_notices (
			notice_id, user_id, full_name, address, phone, email,
			good_faith, consent_to_process, signature
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING
			id, notice_id, user_id, full_name, address, phone, email,
			good_faith, consent_to_process, signature, status, created_at
	`,
		params.NoticeID,
		params.UserID,
		params.FullName,
		params.Address,
		params.Phone,
		params.Email,
		params.GoodFaith,
		params.ConsentToProcess,
		params.Signature,
	).Scan(
		&counter.ID,
		&counter.NoticeID,
		&counter.UserID,
		&counter.FullName,
		&counter.Address,
		&counter.Phone,
		&counter.Email,
		&counter.GoodFaith,
		&counter.ConsentToProcess,
		&counter.Signature,
		&counter.Status,
		&counter.CreatedAt,
	)
	if err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE copyright_notices
		SET status = 'countered', updated_at = now()
		WHERE id = $1
	`, params.NoticeID); err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			actor_id, action, entity_type, entity_id
		)
		VALUES ($1, 'copyright.counter_notice_received', 'copyright_notice', $2)
	`, params.UserID, params.NoticeID); err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}
	return counter, nil
}
