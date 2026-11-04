package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Report struct {
	ID                 string    `json:"id"`
	SubjectType        string    `json:"subject_type"`
	SubjectID          string    `json:"subject_id"`
	ChannelID          string    `json:"channel_id"`
	Reason             string    `json:"reason"`
	Details            string    `json:"details"`
	ReporterID         string    `json:"reporter_id,omitempty"`
	Status             string    `json:"status"`
	ModerationCaseID   string    `json:"moderation_case_id,omitempty"`
	ModerationSignalID string    `json:"moderation_signal_id,omitempty"`
	Error              string    `json:"error,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

const reportColumns = `id, subject_type, subject_id, channel_id, reason, details,
	COALESCE(reporter_id, ''), status, COALESCE(moderation_case_id, ''),
	COALESCE(moderation_signal_id, ''), error, created_at, updated_at`

func scanReport(scanner rowScanner) (Report, error) {
	var report Report
	err := scanner.Scan(
		&report.ID, &report.SubjectType, &report.SubjectID, &report.ChannelID,
		&report.Reason, &report.Details, &report.ReporterID, &report.Status,
		&report.ModerationCaseID, &report.ModerationSignalID, &report.Error,
		&report.CreatedAt, &report.UpdatedAt,
	)
	return report, err
}

func (s *Store) CreateReport(ctx context.Context, report Report) (Report, error) {
	id, err := NewID("rep_")
	if err != nil {
		return Report{}, err
	}
	report.ID = id
	var reporter any
	if report.ReporterID != "" {
		reporter = report.ReporterID
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO reports (
			id, subject_type, subject_id, channel_id, reason, details, reporter_id,
			status, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING `+reportColumns,
		report.ID, report.SubjectType, report.SubjectID, report.ChannelID,
		report.Reason, report.Details, reporter, report.Status,
		report.CreatedAt,
	)
	return scanReport(row)
}

func (s *Store) UpdateReportOutcome(
	ctx context.Context,
	id, status, caseID, signalID, errorMessage string,
	at time.Time,
) (Report, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE reports
		SET status = $2, moderation_case_id = NULLIF($3, ''),
		    moderation_signal_id = NULLIF($4, ''), error = $5, updated_at = $6
		WHERE id = $1
		RETURNING `+reportColumns,
		id, status, caseID, signalID, errorMessage, at,
	)
	report, err := scanReport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	return report, err
}

func (s *Store) GetReport(ctx context.Context, id string) (Report, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+reportColumns+` FROM reports WHERE id = $1`, id)
	report, err := scanReport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	return report, err
}

func (s *Store) ListReports(ctx context.Context, actorID string, limit, offset int) ([]Report, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM reports WHERE reporter_id = $1
	`, actorID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+reportColumns+`
		FROM reports
		WHERE reporter_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, actorID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	reports := make([]Report, 0, limit)
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, 0, err
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return reports, total, nil
}
