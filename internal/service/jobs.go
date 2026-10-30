// Пакет service содержит сценарии использования приложения. Он не зависит от
// HTTP-обработчиков и конкретных реализаций PostgreSQL и worker'ов.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"voice-api/internal/jobs"
	"voice-api/internal/queue"
)

var ErrNotAccepting = errors.New("service is shutting down")
var ErrTranscriptNotReady = errors.New("transcript is not ready")
var ErrInvalidLanguage = errors.New("language must contain at most 16 characters")

type JobService struct {
	store     jobs.Store
	submitter queue.Submitter
	uploadDir string
	logger    *slog.Logger
	accepting atomic.Bool
}

func NewJobService(store jobs.Store, submitter queue.Submitter, uploadDir string, logger *slog.Logger) *JobService {
	service := &JobService{store: store, submitter: submitter, uploadDir: uploadDir, logger: logger}
	service.accepting.Store(true)
	return service
}

func (s *JobService) StopAccepting() { s.accepting.Store(false) }

func (s *JobService) IsAccepting() bool { return s.accepting.Load() }

// CreateAudio сохраняет загрузку и передаёт задание в асинхронную обработку.
// При неудачном приёме задания строка БД и аудиофайл удаляются вместе, когда
// это возможно. Если строку удалить не удалось, файл сохраняется, чтобы после
// перезапуска в очереди не оказалось задания без исходного файла.
func (s *JobService) CreateAudio(ctx context.Context, fileName, language string, audio io.Reader) (jobs.Job, error) {
	if !s.accepting.Load() {
		return jobs.Job{}, ErrNotAccepting
	}
	language = strings.TrimSpace(language)
	if len(language) > 16 {
		return jobs.Job{}, ErrInvalidLanguage
	}
	if language == "" {
		language = "ru"
	}
	if err := os.MkdirAll(s.uploadDir, 0o750); err != nil {
		return jobs.Job{}, fmt.Errorf("create upload directory: %w", err)
	}
	temporaryFile, err := os.CreateTemp(s.uploadDir, "audio-*.wav")
	if err != nil {
		return jobs.Job{}, fmt.Errorf("create temporary audio file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	if _, err := io.Copy(temporaryFile, audio); err != nil {
		_ = temporaryFile.Close()
		_ = os.Remove(temporaryPath)
		return jobs.Job{}, fmt.Errorf("save audio: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return jobs.Job{}, fmt.Errorf("close audio: %w", err)
	}

	job, err := s.store.Create(ctx, fileName, temporaryPath, language)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return jobs.Job{}, fmt.Errorf("create job: %w", err)
	}
	if err := s.submitter.Submit(job); err != nil {
		if deleteErr := s.store.DeleteQueued(ctx, job.ID); deleteErr != nil {
			s.logger.Error("remove rejected job", "job_id", job.ID, "error", deleteErr)
			return jobs.Job{}, err
		}
		_ = os.Remove(temporaryPath)
		return jobs.Job{}, err
	}
	return job, nil
}

func (s *JobService) List(ctx context.Context, limit int) ([]jobs.Job, error) {
	return s.store.List(ctx, limit)
}

func (s *JobService) Get(ctx context.Context, id string) (jobs.Job, error) {
	return s.store.Get(ctx, id)
}

func (s *JobService) Transcript(ctx context.Context, id string) (string, error) {
	job, err := s.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if job.Status != jobs.StatusCompleted || job.Transcript == nil {
		return "", fmt.Errorf("%w; job status: %s", ErrTranscriptNotReady, job.Status)
	}
	return *job.Transcript, nil
}

func (s *JobService) Search(ctx context.Context, query string, limit int) ([]jobs.SearchResult, error) {
	return s.store.Search(ctx, query, limit)
}

func IsQueueFull(err error) bool { return errors.Is(err, queue.ErrFull) }
func IsStopping(err error) bool {
	return errors.Is(err, queue.ErrStopped) || errors.Is(err, ErrNotAccepting)
}
