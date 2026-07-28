// Package checkpoint persists migration jobs, checkpoints and source-to-target
// mappings in the target NPanel database. The tables are migrator-owned and use
// an isolated prefix so the commercial backend ignores them.
package checkpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	leaseDuration = 2 * time.Minute
)

type Store struct {
	db    *sql.DB
	owner string
}

type Job struct {
	ID              string     `json:"id"`
	SourceKey       string     `json:"sourceKey"`
	TargetKey       string     `json:"targetKey"`
	OptionsHash     string     `json:"optionsHash"`
	OptionsJSON     string     `json:"optionsJson,omitempty"`
	Status          string     `json:"status"`
	Phase           string     `json:"phase"`
	UserHighWater   int64      `json:"userHighWater"`
	OrderHighWater  int64      `json:"orderHighWater"`
	TrialAnchorTime time.Time  `json:"trialAnchorTime"`
	CancelRequested bool       `json:"cancelRequested"`
	LeaseOwner      string     `json:"-"`
	LeaseUntil      *time.Time `json:"leaseUntil,omitempty"`
	Total           int64      `json:"total"`
	Done            int64      `json:"done"`
	Errors          int64      `json:"errors"`
	StartedAt       time.Time  `json:"startedAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
	Resumable       bool       `json:"resumable"`
	EffectiveStatus string     `json:"effectiveStatus"`
}

type Checkpoint struct {
	JobID        string
	Phase        string
	LastSourceID int64
	Done         int64
	Total        int64
	Errors       int64
}

type EntityMapping struct {
	SourceID int64
	TargetID int64
}

type BatchRecord struct {
	JobID      string
	Phase      string
	CursorFrom int64
	CursorTo   int64
	Attempted  int
	Succeeded  int
	Failed     int
	Status     string
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// BindOwner enables ownership checks for state-changing task operations. It is
// called only after a new job is created or a resumable job is acquired.
func (s *Store) BindOwner(owner string) {
	s.owner = owner
}

func (s *Store) Owned() bool {
	return s != nil && s.owner != ""
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS npanel_migrator_jobs (
			id VARCHAR(36) NOT NULL,
			source_key CHAR(64) NOT NULL,
			target_key CHAR(64) NOT NULL,
			options_hash CHAR(64) NOT NULL,
			options_json LONGTEXT NOT NULL,
			status VARCHAR(20) NOT NULL,
			phase VARCHAR(40) NOT NULL,
			user_high_water BIGINT NOT NULL DEFAULT 0,
			order_high_water BIGINT NOT NULL DEFAULT 0,
			trial_anchor_time DATETIME(6) NOT NULL,
			cancel_requested TINYINT(1) NOT NULL DEFAULT 0,
			lease_owner VARCHAR(128) NOT NULL DEFAULT '',
			lease_until DATETIME(6) NULL,
			total BIGINT NOT NULL DEFAULT 0,
			done BIGINT NOT NULL DEFAULT 0,
			errors BIGINT NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL,
			started_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			finished_at DATETIME(6) NULL,
			PRIMARY KEY (id),
			KEY idx_npanel_migrator_jobs_match (source_key, target_key, options_hash),
			KEY idx_npanel_migrator_jobs_status (status, updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS npanel_migrator_checkpoints (
			job_id VARCHAR(36) NOT NULL,
			phase VARCHAR(40) NOT NULL,
			last_source_id BIGINT NOT NULL DEFAULT 0,
			done BIGINT NOT NULL DEFAULT 0,
			total BIGINT NOT NULL DEFAULT 0,
			errors BIGINT NOT NULL DEFAULT 0,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (job_id, phase)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS npanel_migrator_entity_map (
			job_id VARCHAR(36) NOT NULL,
			entity_type VARCHAR(40) NOT NULL,
			source_id BIGINT NOT NULL,
			target_id BIGINT NOT NULL,
			created_at DATETIME(6) NOT NULL,
			PRIMARY KEY (job_id, entity_type, source_id),
			KEY idx_npanel_migrator_entity_target (job_id, entity_type, target_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS npanel_migrator_batches (
			id BIGINT NOT NULL AUTO_INCREMENT,
			job_id VARCHAR(36) NOT NULL,
			phase VARCHAR(40) NOT NULL,
			cursor_from BIGINT NOT NULL,
			cursor_to BIGINT NOT NULL,
			attempted INT NOT NULL,
			succeeded INT NOT NULL,
			failed INT NOT NULL,
			status VARCHAR(20) NOT NULL,
			created_at DATETIME(6) NOT NULL,
			PRIMARY KEY (id),
			KEY idx_npanel_migrator_batches_job (job_id, phase, id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS npanel_migrator_errors (
			id BIGINT NOT NULL AUTO_INCREMENT,
			job_id VARCHAR(36) NOT NULL,
			phase VARCHAR(40) NOT NULL,
			entity_type VARCHAR(40) NOT NULL,
			source_id BIGINT NOT NULL,
			error_message TEXT NOT NULL,
			created_at DATETIME(6) NOT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY uq_npanel_migrator_error (job_id, phase, entity_type, source_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("创建迁移断点表失败: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateJob(ctx context.Context, job Job) error {
	now := time.Now()
	if job.StartedAt.IsZero() {
		job.StartedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.Status == "" {
		job.Status = StatusRunning
	}
	if job.Phase == "" {
		job.Phase = "init"
	}
	leaseUntil := now.Add(leaseDuration)
	_, err := s.db.ExecContext(ctx, `INSERT INTO npanel_migrator_jobs (
		id, source_key, target_key, options_hash, options_json, status, phase,
		user_high_water, order_high_water, trial_anchor_time, cancel_requested,
		lease_owner, lease_until, total, done, errors, last_error,
		started_at, updated_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 0, 0, 0, '', ?, ?, NULL)`,
		job.ID, job.SourceKey, job.TargetKey, job.OptionsHash, job.OptionsJSON,
		job.Status, job.Phase, job.UserHighWater, job.OrderHighWater,
		job.TrialAnchorTime, job.LeaseOwner, leaseUntil, job.StartedAt, job.UpdatedAt,
	)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, source_key, target_key, options_hash, options_json, status, phase,
		user_high_water, order_high_water, trial_anchor_time, cancel_requested,
		lease_owner, lease_until, total, done, errors, last_error,
		started_at, updated_at, finished_at
		FROM npanel_migrator_jobs WHERE id = ?`, id)
	job, err := scanJob(row)
	if err != nil {
		return nil, err
	}
	deriveJobState(job, time.Now())
	return job, nil
}

func (s *Store) ListJobs(ctx context.Context, sourceKey, targetKey string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, source_key, target_key, options_hash, options_json, status, phase,
		user_high_water, order_high_water, trial_anchor_time, cancel_requested,
		lease_owner, lease_until, total, done, errors, last_error,
		started_at, updated_at, finished_at
		FROM npanel_migrator_jobs
		WHERE source_key = ? AND target_key = ?
		ORDER BY started_at DESC LIMIT ?`, sourceKey, targetKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	now := time.Now()
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		deriveJobState(job, now)
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(scanner rowScanner) (*Job, error) {
	var job Job
	var leaseUntil, finishedAt sql.NullTime
	if err := scanner.Scan(
		&job.ID, &job.SourceKey, &job.TargetKey, &job.OptionsHash, &job.OptionsJSON,
		&job.Status, &job.Phase, &job.UserHighWater, &job.OrderHighWater,
		&job.TrialAnchorTime, &job.CancelRequested, &job.LeaseOwner, &leaseUntil,
		&job.Total, &job.Done, &job.Errors, &job.LastError,
		&job.StartedAt, &job.UpdatedAt, &finishedAt,
	); err != nil {
		return nil, err
	}
	if leaseUntil.Valid {
		job.LeaseUntil = &leaseUntil.Time
	}
	if finishedAt.Valid {
		job.FinishedAt = &finishedAt.Time
	}
	return &job, nil
}

func deriveJobState(job *Job, now time.Time) {
	job.EffectiveStatus = job.Status
	if job.Status == StatusRunning && job.LeaseUntil != nil && job.LeaseUntil.Before(now) {
		job.EffectiveStatus = "interrupted"
	}
	job.Resumable = job.EffectiveStatus == "interrupted" ||
		job.Status == StatusFailed ||
		job.Status == StatusCancelled
}

func (s *Store) AcquireJob(ctx context.Context, id, owner string) error {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE npanel_migrator_jobs
		SET status = ?, lease_owner = ?, lease_until = ?, cancel_requested = 0,
			finished_at = NULL, last_error = '', updated_at = ?
		WHERE id = ? AND status <> ? AND (
			status <> ? OR lease_owner = ? OR lease_until IS NULL OR lease_until < ?
		)`,
		StatusRunning, owner, now.Add(leaseDuration), now,
		id, StatusCompleted, StatusRunning, owner, now,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("迁移任务已完成，或仍被另一个迁移器实例占用")
	}
	return nil
}

// RenewLease keeps a running task owned by this process from being mistaken for
// an interrupted task while a source query or a legacy module phase is busy.
func (s *Store) RenewLease(ctx context.Context, id, owner string) error {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE npanel_migrator_jobs
		SET lease_until = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_owner = ?`,
		now.Add(leaseDuration), now, id, StatusRunning, owner,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("迁移任务租约已失效或所有者发生变化")
	}
	return nil
}

func (s *Store) LoadCheckpoint(ctx context.Context, jobID, phase string) (Checkpoint, error) {
	var cp Checkpoint
	err := s.db.QueryRowContext(ctx, `SELECT job_id, phase, last_source_id, done, total, errors
		FROM npanel_migrator_checkpoints WHERE job_id = ? AND phase = ?`,
		jobID, phase,
	).Scan(&cp.JobID, &cp.Phase, &cp.LastSourceID, &cp.Done, &cp.Total, &cp.Errors)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{JobID: jobID, Phase: phase}, nil
	}
	return cp, err
}

func (s *Store) SaveCheckpointTx(ctx context.Context, tx *sql.Tx, cp Checkpoint, owner string) error {
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO npanel_migrator_checkpoints (
		job_id, phase, last_source_id, done, total, errors, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE
		last_source_id = VALUES(last_source_id),
		done = VALUES(done),
		total = VALUES(total),
		errors = VALUES(errors),
		updated_at = VALUES(updated_at)`,
		cp.JobID, cp.Phase, cp.LastSourceID, cp.Done, cp.Total, cp.Errors, now,
	); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE npanel_migrator_jobs
		SET phase = ?, done = ?, total = ?, errors = ?, lease_owner = ?,
			lease_until = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_owner = ?`,
		cp.Phase, cp.Done, cp.Total, cp.Errors, owner,
		now.Add(leaseDuration), now, cp.JobID, StatusRunning, owner,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("迁移任务租约已失效，拒绝提交当前批次")
	}
	return nil
}

func (s *Store) PutMappingsTx(
	ctx context.Context,
	tx *sql.Tx,
	jobID, entityType string,
	mappings []EntityMapping,
) error {
	if len(mappings) == 0 {
		return nil
	}
	query := `INSERT INTO npanel_migrator_entity_map
		(job_id, entity_type, source_id, target_id, created_at) VALUES `
	args := make([]any, 0, len(mappings)*5)
	now := time.Now()
	for i, mapping := range mappings {
		if i > 0 {
			query += ","
		}
		query += "(?, ?, ?, ?, ?)"
		args = append(args, jobID, entityType, mapping.SourceID, mapping.TargetID, now)
	}
	query += ` ON DUPLICATE KEY UPDATE target_id = VALUES(target_id)`
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) LoadMappings(ctx context.Context, jobID, entityType string) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_id, target_id
		FROM npanel_migrator_entity_map WHERE job_id = ? AND entity_type = ?`,
		jobID, entityType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64]int64)
	for rows.Next() {
		var sourceID, targetID int64
		if err := rows.Scan(&sourceID, &targetID); err != nil {
			return nil, err
		}
		result[sourceID] = targetID
	}
	return result, rows.Err()
}

func (s *Store) RecordBatchTx(ctx context.Context, tx *sql.Tx, batch BatchRecord) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO npanel_migrator_batches (
		job_id, phase, cursor_from, cursor_to, attempted, succeeded, failed, status, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		batch.JobID, batch.Phase, batch.CursorFrom, batch.CursorTo,
		batch.Attempted, batch.Succeeded, batch.Failed, batch.Status, time.Now(),
	)
	return err
}

func (s *Store) RecordErrorTx(
	ctx context.Context,
	tx *sql.Tx,
	jobID, phase, entityType string,
	sourceID int64,
	err error,
) error {
	message := "unknown error"
	if err != nil {
		message = err.Error()
	}
	_, execErr := tx.ExecContext(ctx, `INSERT INTO npanel_migrator_errors (
		job_id, phase, entity_type, source_id, error_message, created_at
	) VALUES (?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE error_message = VALUES(error_message), created_at = VALUES(created_at)`,
		jobID, phase, entityType, sourceID, message, time.Now(),
	)
	return execErr
}

func (s *Store) IsCancelRequested(ctx context.Context, jobID string) (bool, error) {
	var cancel bool
	err := s.db.QueryRowContext(ctx,
		`SELECT cancel_requested FROM npanel_migrator_jobs WHERE id = ?`, jobID,
	).Scan(&cancel)
	return cancel, err
}

func (s *Store) RequestCancel(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE npanel_migrator_jobs
		SET cancel_requested = 1, updated_at = ? WHERE id = ? AND status = ?`,
		time.Now(), jobID, StatusRunning,
	)
	return err
}

func (s *Store) MarkFinished(ctx context.Context, jobID, status, message string) error {
	if s.owner == "" {
		// The caller has not created/acquired this task, so changing an existing
		// job (for example after a resume fingerprint mismatch) is unsafe.
		return nil
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `UPDATE npanel_migrator_jobs
		SET status = ?, last_error = ?, lease_until = NULL, updated_at = ?, finished_at = ?
		WHERE id = ? AND lease_owner = ?`,
		status, message, now, now, jobID, s.owner,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("迁移任务所有权已变化，拒绝修改最终状态")
	}
	return nil
}
