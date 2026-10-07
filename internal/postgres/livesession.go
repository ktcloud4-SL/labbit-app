package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const liveSessionSelect = `SELECT id, organization_id, class_id, source_terminal_session_id, instructor_user_id,
	created_at, ended_at, COALESCE(end_reason, '')
	FROM live_sessions`

func scanLiveSession(row pgx.Row) (repository.LiveSession, error) {
	var s repository.LiveSession
	err := row.Scan(&s.ID, &s.OrganizationID, &s.ClassID, &s.SourceTerminalSessionID, &s.InstructorUserID,
		&s.CreatedAt, &s.EndedAt, &s.EndReason)
	return s, err
}

func (q queries) LiveSessionByID(ctx context.Context, id uuid.UUID) (repository.LiveSession, error) {
	const op = "LiveSessionByID"

	session, err := scanLiveSession(q.db.QueryRow(ctx, liveSessionSelect+` WHERE id = $1`, id))
	if err != nil {
		return repository.LiveSession{}, normalize(op, err)
	}
	return session, nil
}

func (q queries) ActiveLiveSessionByClass(ctx context.Context, classID uuid.UUID) (repository.LiveSession, error) {
	const op = "ActiveLiveSessionByClass"

	session, err := scanLiveSession(q.db.QueryRow(ctx, liveSessionSelect+` WHERE class_id = $1 AND ended_at IS NULL`, classID))
	if err != nil {
		return repository.LiveSession{}, normalize(op, err)
	}
	return session, nil
}

func (q queries) CreateLiveSession(ctx context.Context, s repository.NewLiveSession) error {
	const op = "CreateLiveSession"

	_, err := q.db.Exec(ctx,
		`INSERT INTO live_sessions
		 (id, organization_id, class_id, source_terminal_session_id, instructor_user_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		s.ID, s.OrganizationID, s.ClassID, s.SourceTerminalSessionID, s.InstructorUserID, s.CreatedAt,
	)
	return normalize(op, err)
}

func (q queries) EndLiveSession(ctx context.Context, id uuid.UUID, endedAt time.Time, reason string) (bool, error) {
	return q.transition(ctx, "EndLiveSession",
		`UPDATE live_sessions SET ended_at = $2, end_reason = $3
		 WHERE id = $1 AND ended_at IS NULL`,
		id, endedAt, reason)
}

func (q queries) EndActiveLiveSessionBySourceTerminal(ctx context.Context, sourceTerminalSessionID uuid.UUID, endedAt time.Time, reason string) (bool, error) {
	const op = "EndActiveLiveSessionBySourceTerminal"

	tag, err := q.db.Exec(ctx,
		`UPDATE live_sessions SET ended_at = $2, end_reason = $3
		 WHERE source_terminal_session_id = $1 AND ended_at IS NULL`,
		sourceTerminalSessionID, endedAt, reason)
	if err != nil {
		return false, normalize(op, err)
	}
	return tag.RowsAffected() > 0, nil
}
