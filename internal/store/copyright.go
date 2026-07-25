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
	Notifications   []EmailNotification
}

type EmailNotification struct {
	Kind    string
	Payload any
}

type CopyrightRecipients struct {
	ClaimantEmail string
	UploaderEmail string
}

func (s *Store) GetCopyrightRecipients(
	ctx context.Context,
	noticeID string,
) (CopyrightRecipients, error) {
	var recipients CopyrightRecipients
	err := s.pool.QueryRow(ctx, `
		SELECT n.claimant_email, COALESCE(u.email::text, '')
		FROM copyright_notices n
		LEFT JOIN submissions sub ON sub.id = n.submission_id
		LEFT JOIN users u ON u.id = sub.user_id
		WHERE n.id = $1
	`, noticeID).Scan(&recipients.ClaimantEmail, &recipients.UploaderEmail)
	if err != nil {
		return CopyrightRecipients{}, mapError(err)
	}
	recipients.ClaimantEmail, err = s.cipher.Reveal(recipients.ClaimantEmail)
	if err != nil {
		return CopyrightRecipients{}, err
	}
	return recipients, nil
}

func (s *Store) scanCopyrightNotice(row scanner) (domain.CopyrightNotice, error) {
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
	if err != nil {
		return notice, mapError(err)
	}
	for target, value := range map[*string]string{
		&notice.ClaimantName:    notice.ClaimantName,
		&notice.ClaimantEmail:   notice.ClaimantEmail,
		&notice.ClaimantAddress: notice.ClaimantAddress,
		&notice.Signature:       notice.Signature,
	} {
		revealed, err := s.cipher.Reveal(value)
		if err != nil {
			return domain.CopyrightNotice{}, err
		}
		*target = revealed
	}
	return notice, nil
}

func (s *Store) CreateCopyrightNotice(
	ctx context.Context,
	params CreateCopyrightNoticeParams,
) (domain.CopyrightNotice, error) {
	claimantName, err := s.cipher.Protect(params.ClaimantName)
	if err != nil {
		return domain.CopyrightNotice{}, err
	}
	claimantEmail, err := s.cipher.Protect(params.ClaimantEmail)
	if err != nil {
		return domain.CopyrightNotice{}, err
	}
	claimantAddress, err := s.cipher.Protect(params.ClaimantAddress)
	if err != nil {
		return domain.CopyrightNotice{}, err
	}
	signature, err := s.cipher.Protect(params.Signature)
	if err != nil {
		return domain.CopyrightNotice{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CopyrightNotice{}, mapError(err)
	}
	defer tx.Rollback(ctx)
	notice, err := s.scanCopyrightNotice(tx.QueryRow(ctx, `
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
		claimantName,
		claimantEmail,
		claimantAddress,
		params.Relationship,
		params.CopyrightedWork,
		params.InfringingURL,
		params.SubmissionID,
		params.GoodFaith,
		params.Accuracy,
		signature,
	))
	if err != nil {
		return domain.CopyrightNotice{}, err
	}
	for _, notification := range params.Notifications {
		payload, err := s.encodeEmailPayload(notification.Payload)
		if err != nil {
			return domain.CopyrightNotice{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (kind, entity_id, payload, max_attempts)
			VALUES ($1, $2, $3, 12)
		`, notification.Kind, notice.ID, payload); err != nil {
			return domain.CopyrightNotice{}, mapError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CopyrightNotice{}, mapError(err)
	}
	return notice, nil
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
		notice, err := s.scanCopyrightNotice(rows)
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
	Notifications    []EmailNotification
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
	if !validCopyrightTransition(currentStatus, params.Status) {
		return domain.CopyrightNotice{}, ErrInvalid
	}

	switch params.Status {
	case "reviewing", "rejected", "closed":
	case "actioned":
		if submissionID != nil {
			command, err := tx.Exec(ctx, `
				UPDATE submissions
				SET moderation_status = 'removed', updated_at = now()
				WHERE id = $1 AND moderation_status <> 'removed'
			`, *submissionID)
			if err != nil {
				return domain.CopyrightNotice{}, mapError(err)
			}
			if command.RowsAffected() > 0 {
				if _, err := tx.Exec(ctx, `
					INSERT INTO moderation_actions (
						moderator_id, target_type, target_id, action, reason, notes,
						metadata
					)
					VALUES (
						$1,
						'submission',
						$2,
						'remove',
						'copyright',
						$3,
						jsonb_build_object('copyright_notice_id', $4::text)
					)
				`, params.ActorID, *submissionID, params.ResolutionNote, params.NoticeID); err != nil {
					return domain.CopyrightNotice{}, mapError(err)
				}
			}
		}
	case "restored":
		if submissionID == nil {
			return domain.CopyrightNotice{}, ErrInvalid
		}
		command, err := tx.Exec(ctx, `
			UPDATE submissions
			SET moderation_status = 'approved',
			    published_at = CASE
			        WHEN verification_status = 'passed' THEN COALESCE(published_at, now())
			        ELSE NULL
			    END,
			    updated_at = now()
			WHERE id = $1::uuid
			  AND moderation_status = 'removed'
			  AND NOT EXISTS (
			      SELECT 1
			      FROM copyright_notices other
			      WHERE other.submission_id = $1::uuid
			        AND other.id <> $2
			        AND other.status IN ('actioned', 'countered')
			  )
			  AND (
			      SELECT action.reason
			      FROM moderation_actions action
			      WHERE action.target_type = 'submission'
			        AND action.target_id = $1::uuid
			        AND action.action IN ('approve', 'reject', 'remove', 'restore')
			      ORDER BY action.created_at DESC, action.id DESC
			      LIMIT 1
			  ) = 'copyright'
		`, *submissionID, params.NoticeID)
		if err != nil {
			return domain.CopyrightNotice{}, mapError(err)
		}
		if command.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO moderation_actions (
					moderator_id, target_type, target_id, action, reason, notes,
					metadata
				)
				VALUES (
					$1,
					'submission',
					$2,
					'restore',
					'copyright_claim_resolved',
					$3,
					jsonb_build_object('copyright_notice_id', $4::text)
				)
			`, params.ActorID, *submissionID, params.ResolutionNote, params.NoticeID); err != nil {
				return domain.CopyrightNotice{}, mapError(err)
			}
		}
	default:
		return domain.CopyrightNotice{}, ErrInvalid
	}

	notice, err := s.scanCopyrightNotice(tx.QueryRow(ctx, `
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
	for _, notification := range params.Notifications {
		payload, err := s.encodeEmailPayload(notification.Payload)
		if err != nil {
			return domain.CopyrightNotice{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (kind, entity_id, payload, max_attempts)
			VALUES ($1, $2, $3, 12)
		`, notification.Kind, params.NoticeID, payload); err != nil {
			return domain.CopyrightNotice{}, mapError(err)
		}
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
	Notifications    []EmailNotification
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
	fullName, err := s.cipher.Protect(params.FullName)
	if err != nil {
		return domain.CopyrightCounterNotice{}, err
	}
	address, err := s.cipher.Protect(params.Address)
	if err != nil {
		return domain.CopyrightCounterNotice{}, err
	}
	phone, err := s.cipher.Protect(params.Phone)
	if err != nil {
		return domain.CopyrightCounterNotice{}, err
	}
	email, err := s.cipher.Protect(params.Email)
	if err != nil {
		return domain.CopyrightCounterNotice{}, err
	}
	signature, err := s.cipher.Protect(params.Signature)
	if err != nil {
		return domain.CopyrightCounterNotice{}, err
	}
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
		fullName,
		address,
		phone,
		email,
		params.GoodFaith,
		params.ConsentToProcess,
		signature,
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
	for target, value := range map[*string]string{
		&counter.FullName:  counter.FullName,
		&counter.Address:   counter.Address,
		&counter.Phone:     counter.Phone,
		&counter.Email:     counter.Email,
		&counter.Signature: counter.Signature,
	} {
		revealed, err := s.cipher.Reveal(value)
		if err != nil {
			return domain.CopyrightCounterNotice{}, err
		}
		*target = revealed
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
	for _, notification := range params.Notifications {
		payload, err := s.encodeEmailPayload(notification.Payload)
		if err != nil {
			return domain.CopyrightCounterNotice{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (kind, entity_id, payload, max_attempts)
			VALUES ($1, $2, $3, 12)
		`, notification.Kind, params.NoticeID, payload); err != nil {
			return domain.CopyrightCounterNotice{}, mapError(err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.CopyrightCounterNotice{}, mapError(err)
	}
	return counter, nil
}

func validCopyrightTransition(from, to string) bool {
	transitions := map[string]map[string]bool{
		"received": {
			"reviewing": true,
			"actioned":  true,
			"rejected":  true,
			"closed":    true,
		},
		"reviewing": {
			"actioned": true,
			"rejected": true,
			"closed":   true,
		},
		"actioned": {
			"restored": true,
			"closed":   true,
		},
		"countered": {
			"restored": true,
			"closed":   true,
		},
		"restored": {
			"closed": true,
		},
	}
	return transitions[from][to]
}
