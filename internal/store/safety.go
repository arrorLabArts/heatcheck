package store

import (
	"context"
	"encoding/json"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

type CreateReportParams struct {
	ReporterID string
	TargetType string
	TargetID   string
	Reason     string
	Details    string
	Priority   string
	AlertEmail any
}

func scanReport(row scanner) (domain.Report, error) {
	var report domain.Report
	err := row.Scan(
		&report.ID,
		&report.ReporterID,
		&report.TargetType,
		&report.TargetID,
		&report.Reason,
		&report.Details,
		&report.Status,
		&report.Priority,
		&report.AssignedTo,
		&report.ResolutionNote,
		&report.CreatedAt,
		&report.UpdatedAt,
		&report.ResolvedAt,
	)
	return report, mapError(err)
}

func (s *Store) CreateReport(ctx context.Context, params CreateReportParams) (domain.Report, error) {
	exists, err := s.targetExists(ctx, params.TargetType, params.TargetID)
	if err != nil {
		return domain.Report{}, err
	}
	if !exists {
		return domain.Report{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Report{}, mapError(err)
	}
	defer tx.Rollback(ctx)
	report, err := scanReport(tx.QueryRow(ctx, `
		INSERT INTO reports (
			reporter_id, target_type, target_id, reason, details, priority
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING
			id, reporter_id, target_type, target_id, reason, details,
			status, priority, assigned_to, resolution_note,
			created_at, updated_at, resolved_at
	`,
		params.ReporterID,
		params.TargetType,
		params.TargetID,
		params.Reason,
		params.Details,
		params.Priority,
	))
	if err != nil {
		return domain.Report{}, err
	}
	if params.AlertEmail != nil {
		payload, err := s.encodeEmailPayload(params.AlertEmail)
		if err != nil {
			return domain.Report{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (kind, entity_id, payload, max_attempts)
			VALUES ('email.safety_alert', $1, $2, 12)
		`, report.ID, payload); err != nil {
			return domain.Report{}, mapError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Report{}, mapError(err)
	}
	return report, nil
}

func (s *Store) targetExists(ctx context.Context, targetType, targetID string) (bool, error) {
	var query string
	switch targetType {
	case "submission":
		query = `SELECT EXISTS (SELECT 1 FROM submissions WHERE id = $1)`
	case "user":
		query = `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)`
	case "challenge":
		query = `SELECT EXISTS (SELECT 1 FROM challenges WHERE id = $1)`
	default:
		return false, ErrInvalid
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, query, targetID).Scan(&exists); err != nil {
		return false, mapError(err)
	}
	return exists, nil
}

func (s *Store) BlockUser(ctx context.Context, blockerID, blockedID string) error {
	command, err := s.pool.Exec(ctx, `
		INSERT INTO user_blocks (blocker_id, blocked_user_id)
		SELECT $1, id
		FROM users
		WHERE id = $2 AND status <> 'deleted' AND id <> $1
		ON CONFLICT DO NOTHING
	`, blockerID, blockedID)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM user_blocks
				WHERE blocker_id = $1 AND blocked_user_id = $2
			)
		`, blockerID, blockedID).Scan(&exists); err != nil {
			return mapError(err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) UnblockUser(ctx context.Context, blockerID, blockedID string) error {
	command, err := s.pool.Exec(ctx, `
		DELETE FROM user_blocks
		WHERE blocker_id = $1 AND blocked_user_id = $2
	`, blockerID, blockedID)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListReports(
	ctx context.Context,
	status string,
	limit int,
	offset int,
) ([]domain.Report, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id, reporter_id, target_type, target_id, reason, details,
			status, priority, assigned_to, resolution_note,
			created_at, updated_at, resolved_at
		FROM reports
		WHERE $1 = '' OR status = $1
		ORDER BY
			CASE priority
				WHEN 'urgent' THEN 1
				WHEN 'high' THEN 2
				WHEN 'normal' THEN 3
				ELSE 4
			END,
			created_at
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var reports []domain.Report
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, mapError(rows.Err())
}

type CreateModerationActionParams struct {
	ModeratorID string
	TargetType  string
	TargetID    string
	Action      string
	Reason      string
	Notes       string
	ReportID    *string
	Metadata    json.RawMessage
}

func (s *Store) CreateModerationAction(
	ctx context.Context,
	params CreateModerationActionParams,
) (domain.ModerationAction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ModerationAction{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var command string
	commandArgs := []any{params.TargetID}
	switch params.TargetType + ":" + params.Action {
	case "submission:approve", "submission:restore":
		command = `
			UPDATE submissions
			SET moderation_status = 'approved',
			    published_at = CASE
			        WHEN verification_status = 'passed' THEN COALESCE(published_at, now())
			        ELSE NULL
			    END,
			    updated_at = now()
			WHERE id = $1
		`
	case "submission:reject":
		command = `
			UPDATE submissions
			SET moderation_status = 'rejected', updated_at = now()
			WHERE id = $1
		`
	case "submission:remove":
		command = `
			UPDATE submissions
			SET moderation_status = 'removed', updated_at = now()
			WHERE id = $1
		`
	case "user:suspend":
		command = `
			UPDATE users target
			SET status = 'suspended', updated_at = now()
			FROM users actor
			WHERE target.id = $1
			  AND actor.id = $2
			  AND target.id <> actor.id
			  AND target.status <> 'deleted'
			  AND (actor.role = 'admin' OR target.role = 'user')
		`
		commandArgs = append(commandArgs, params.ModeratorID)
	case "user:unsuspend":
		command = `
			UPDATE users target
			SET status = 'active', updated_at = now()
			FROM users actor
			WHERE target.id = $1
			  AND actor.id = $2
			  AND target.id <> actor.id
			  AND target.status = 'suspended'
			  AND (actor.role = 'admin' OR target.role = 'user')
		`
		commandArgs = append(commandArgs, params.ModeratorID)
	case "challenge:close":
		command = `
			UPDATE challenges SET status = 'closed', updated_at = now()
			WHERE id = $1
		`
	case "challenge:archive":
		command = `
			UPDATE challenges SET status = 'archived', updated_at = now()
			WHERE id = $1
		`
	case "user:warn":
		command = `
			SELECT target.id
			FROM users target
			JOIN users actor ON actor.id = $2
			WHERE target.id = $1
			  AND target.id <> actor.id
			  AND target.status <> 'deleted'
			  AND (actor.role = 'admin' OR target.role = 'user')
		`
		commandArgs = append(commandArgs, params.ModeratorID)
	default:
		return domain.ModerationAction{}, ErrInvalid
	}

	tag, err := tx.Exec(ctx, command, commandArgs...)
	if err != nil {
		return domain.ModerationAction{}, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ModerationAction{}, ErrNotFound
	}

	if len(params.Metadata) == 0 {
		params.Metadata = json.RawMessage(`{}`)
	}
	var action domain.ModerationAction
	err = tx.QueryRow(ctx, `
		INSERT INTO moderation_actions (
			moderator_id, target_type, target_id, action,
			reason, notes, report_id, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING
			id, moderator_id, target_type, target_id, action,
			reason, notes, report_id, metadata, created_at
	`,
		params.ModeratorID,
		params.TargetType,
		params.TargetID,
		params.Action,
		params.Reason,
		params.Notes,
		params.ReportID,
		params.Metadata,
	).Scan(
		&action.ID,
		&action.ModeratorID,
		&action.TargetType,
		&action.TargetID,
		&action.Action,
		&action.Reason,
		&action.Notes,
		&action.ReportID,
		&action.Metadata,
		&action.CreatedAt,
	)
	if err != nil {
		return domain.ModerationAction{}, mapError(err)
	}

	if params.ReportID != nil {
		command, err := tx.Exec(ctx, `
			UPDATE reports
			SET status = 'resolved',
			    resolution_note = $2,
			    resolved_at = now(),
			    updated_at = now()
			WHERE id = $1 AND target_type = $3 AND target_id = $4
		`, *params.ReportID, params.Notes, params.TargetType, params.TargetID)
		if err != nil {
			return domain.ModerationAction{}, mapError(err)
		}
		if command.RowsAffected() == 0 {
			return domain.ModerationAction{}, ErrInvalid
		}
	}

	auditMetadata, _ := json.Marshal(map[string]string{
		"action": params.Action,
		"reason": params.Reason,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			actor_id, action, entity_type, entity_id, metadata
		)
		VALUES ($1, 'moderation.action', $2, $3, $4)
	`, params.ModeratorID, params.TargetType, params.TargetID, auditMetadata); err != nil {
		return domain.ModerationAction{}, mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.ModerationAction{}, mapError(err)
	}
	return action, nil
}

func (s *Store) CreateAppeal(
	ctx context.Context,
	userID string,
	actionID string,
	reason string,
) (domain.Appeal, error) {
	var appeal domain.Appeal
	err := s.pool.QueryRow(ctx, `
		INSERT INTO appeals (user_id, action_id, reason)
		SELECT $1, a.id, $3
		FROM moderation_actions a
		LEFT JOIN submissions s
		  ON a.target_type = 'submission' AND s.id = a.target_id
		WHERE a.id = $2
		  AND a.action IN ('reject', 'remove', 'suspend', 'warn')
		  AND (
			(a.target_type = 'user' AND a.target_id = $1)
			OR (a.target_type = 'submission' AND s.user_id = $1)
		  )
		RETURNING
			id, user_id, action_id, reason, status, reviewed_by,
			resolution_note, created_at, resolved_at
	`, userID, actionID, reason).Scan(
		&appeal.ID,
		&appeal.UserID,
		&appeal.ActionID,
		&appeal.Reason,
		&appeal.Status,
		&appeal.ReviewedBy,
		&appeal.ResolutionNote,
		&appeal.CreatedAt,
		&appeal.ResolvedAt,
	)
	return appeal, mapError(err)
}

func (s *Store) ListAppeals(
	ctx context.Context,
	status string,
	limit int,
	offset int,
) ([]domain.Appeal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id, user_id, action_id, reason, status, reviewed_by,
			resolution_note, created_at, resolved_at
		FROM appeals
		WHERE $1 = '' OR status = $1
		ORDER BY created_at
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var appeals []domain.Appeal
	for rows.Next() {
		var appeal domain.Appeal
		if err := rows.Scan(
			&appeal.ID,
			&appeal.UserID,
			&appeal.ActionID,
			&appeal.Reason,
			&appeal.Status,
			&appeal.ReviewedBy,
			&appeal.ResolutionNote,
			&appeal.CreatedAt,
			&appeal.ResolvedAt,
		); err != nil {
			return nil, mapError(err)
		}
		appeals = append(appeals, appeal)
	}
	return appeals, mapError(rows.Err())
}

func (s *Store) ReviewAppeal(
	ctx context.Context,
	appealID string,
	reviewerID string,
	status string,
	resolutionNote string,
) (domain.Appeal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Appeal{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var actionID, targetType, targetID, originalAction string
	err = tx.QueryRow(ctx, `
		SELECT ap.action_id, a.target_type, a.target_id, a.action
		FROM appeals ap
		JOIN moderation_actions a ON a.id = ap.action_id
		WHERE ap.id = $1 AND ap.status = 'pending'
		FOR UPDATE OF ap
	`, appealID).Scan(&actionID, &targetType, &targetID, &originalAction)
	if err != nil {
		return domain.Appeal{}, mapError(err)
	}
	if targetType == "user" {
		var allowed bool
		if err := tx.QueryRow(ctx, `
			SELECT reviewer.role = 'admin' OR target.role = 'user'
			FROM users reviewer, users target
			WHERE reviewer.id = $1 AND target.id = $2
		`, reviewerID, targetID).Scan(&allowed); err != nil {
			return domain.Appeal{}, mapError(err)
		}
		if !allowed {
			return domain.Appeal{}, ErrForbidden
		}
	}

	if status == "reversed" {
		var reversalAction string
		var command string
		commandArgs := []any{targetID, actionID}
		switch targetType + ":" + originalAction {
		case "submission:reject", "submission:remove":
			reversalAction = "restore"
			command = `
				UPDATE submissions
				SET moderation_status = 'approved',
				    published_at = CASE
				        WHEN verification_status = 'passed' THEN COALESCE(published_at, now())
				        ELSE NULL
				    END,
				    updated_at = now()
				WHERE id = $1
				  AND (
				      SELECT latest.id
				      FROM moderation_actions latest
				      WHERE latest.target_type = 'submission'
				        AND latest.target_id = $1::uuid
				        AND latest.action IN ('approve', 'reject', 'remove', 'restore')
				      ORDER BY latest.created_at DESC, latest.id DESC
				      LIMIT 1
				  ) = $2::uuid
			`
		case "user:suspend":
			reversalAction = "unsuspend"
			command = `
				UPDATE users
				SET status = 'active', updated_at = now()
				WHERE id = $1 AND status = 'suspended'
				  AND (
				      SELECT latest.id
				      FROM moderation_actions latest
				      WHERE latest.target_type = 'user'
				        AND latest.target_id = $1::uuid
				        AND latest.action IN ('suspend', 'unsuspend')
				      ORDER BY latest.created_at DESC, latest.id DESC
				      LIMIT 1
				  ) = $2::uuid
			`
		case "user:warn":
			reversalAction = ""
		default:
			return domain.Appeal{}, ErrInvalid
		}
		if command != "" {
			tag, err := tx.Exec(ctx, command, commandArgs...)
			if err != nil {
				return domain.Appeal{}, mapError(err)
			}
			if tag.RowsAffected() == 0 {
				return domain.Appeal{}, ErrInvalid
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO moderation_actions (
					moderator_id, target_type, target_id, action, reason, notes,
					metadata
				)
				VALUES (
					$1, $2, $3, $4, 'appeal_reversed', $5,
					jsonb_build_object(
						'appeal_id', $6::text,
						'original_action_id', $7::text
					)
				)
			`,
				reviewerID,
				targetType,
				targetID,
				reversalAction,
				resolutionNote,
				appealID,
				actionID,
			); err != nil {
				return domain.Appeal{}, mapError(err)
			}
		}
	}

	var appeal domain.Appeal
	err = tx.QueryRow(ctx, `
		UPDATE appeals
		SET status = $3,
		    reviewed_by = $2,
		    resolution_note = $4,
		    resolved_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING
			id, user_id, action_id, reason, status, reviewed_by,
			resolution_note, created_at, resolved_at
	`, appealID, reviewerID, status, resolutionNote).Scan(
		&appeal.ID,
		&appeal.UserID,
		&appeal.ActionID,
		&appeal.Reason,
		&appeal.Status,
		&appeal.ReviewedBy,
		&appeal.ResolutionNote,
		&appeal.CreatedAt,
		&appeal.ResolvedAt,
	)
	if err != nil {
		return domain.Appeal{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			actor_id, action, entity_type, entity_id, metadata
		)
		VALUES (
			$1, 'appeal.reviewed', 'appeal', $2,
			jsonb_build_object('status', $3::text)
		)
	`, reviewerID, appealID, status); err != nil {
		return domain.Appeal{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Appeal{}, mapError(err)
	}
	return appeal, nil
}

func (s *Store) ListAuditEvents(
	ctx context.Context,
	limit int,
	offset int,
) ([]domain.AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_id, action, entity_type, entity_id, metadata, created_at
		FROM audit_events
		ORDER BY id DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var events []domain.AuditEvent
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(
			&event.ID,
			&event.ActorID,
			&event.Action,
			&event.EntityType,
			&event.EntityID,
			&event.Metadata,
			&event.CreatedAt,
		); err != nil {
			return nil, mapError(err)
		}
		events = append(events, event)
	}
	return events, mapError(rows.Err())
}

func (s *Store) DismissReport(
	ctx context.Context,
	reportID string,
	moderatorID string,
	note string,
) (domain.Report, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Report{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	report, err := scanReport(tx.QueryRow(ctx, `
		UPDATE reports
		SET status = 'dismissed',
		    resolution_note = $3,
		    assigned_to = $2,
		    resolved_at = now(),
		    updated_at = now()
		WHERE id = $1 AND status IN ('open', 'reviewing')
		RETURNING
			id, reporter_id, target_type, target_id, reason, details,
			status, priority, assigned_to, resolution_note,
			created_at, updated_at, resolved_at
	`, reportID, moderatorID, note))
	if err != nil {
		return domain.Report{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			actor_id, action, entity_type, entity_id, metadata
		)
		VALUES ($1, 'report.dismissed', 'report', $2, $3)
	`, moderatorID, reportID, json.RawMessage(`{}`)); err != nil {
		return domain.Report{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Report{}, mapError(err)
	}
	return report, nil
}
