package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("job not found")

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const jobColumns = `id::text, original_filename, audio_path, language, status::text, transcript, error_message, created_at, started_at, finished_at`

func (r *Repository) Create(ctx context.Context, fileName, audioPath, language string) (Job, error) {
	row := r.pool.QueryRow(ctx, `insert into jobs (original_filename, audio_path, language) values ($1, $2, $3) returning `+jobColumns, fileName, audioPath, language)
	return scanJob(row)
}

func (r *Repository) Get(ctx context.Context, id string) (Job, error) {
	job, err := scanJob(r.pool.QueryRow(ctx, `select `+jobColumns+` from jobs where id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

func (r *Repository) List(ctx context.Context, limit int) ([]Job, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `select `+jobColumns+` from jobs order by created_at desc limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *Repository) MarkProcessing(ctx context.Context, id string) error {
	return r.updateOne(ctx, `update jobs set status = 'processing', started_at = now(), finished_at = null, error_message = null where id = $1 and status = 'queued'`, id)
}
func (r *Repository) MarkCompleted(ctx context.Context, id, transcript string) error {
	return r.updateOne(ctx, `update jobs set status = 'completed', transcript = $2, error_message = null, finished_at = now() where id = $1 and status = 'processing'`, id, transcript)
}
func (r *Repository) MarkFailed(ctx context.Context, id, message string) error {
	return r.updateOne(ctx, `update jobs set status = 'failed', error_message = $2, finished_at = now() where id = $1 and status = 'processing'`, id, message)
}

func (r *Repository) RequeueInterrupted(ctx context.Context) ([]Job, error) {
	rows, err := r.pool.Query(ctx, `update jobs set status = 'queued', started_at = null where status in ('queued', 'processing') returning `+jobColumns)
	if err != nil {
		return nil, fmt.Errorf("requeue interrupted jobs: %w", err)
	}
	defer rows.Close()
	var result []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

func (r *Repository) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `select id::text, original_filename, substring(transcript from greatest(strpos(lower(transcript), lower($1)) - 80, 1) for 240), finished_at from jobs where status = 'completed' and transcript ilike '%' || $1 || '%' order by finished_at desc limit $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search transcripts: %w", err)
	}
	defer rows.Close()
	result := make([]SearchResult, 0)
	for rows.Next() {
		var item SearchResult
		if err := rows.Scan(&item.JobID, &item.FileName, &item.Fragment, &item.FinishedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) updateOne(ctx context.Context, query string, args ...any) error {
	result, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.OriginalFilename, &job.AudioPath, &job.Language, &job.Status, &job.Transcript, &job.ErrorMessage, &job.CreatedAt, &job.StartedAt, &job.FinishedAt)
	return job, err
}
