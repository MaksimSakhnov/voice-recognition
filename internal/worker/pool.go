package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"voice-api/internal/jobs"
	"voice-api/internal/queue"
	"voice-api/internal/stt"
)

// Pool owns a bounded job queue and a fixed number of transcription workers.
type Pool struct {
	repository  jobs.Store
	transcriber stt.Transcriber
	logger      *slog.Logger
	queue       chan jobs.Job
	stop        chan struct{}
	processCtx  context.Context
	cancel      context.CancelFunc

	mu        sync.Mutex
	accepting bool
	stopOnce  sync.Once
	workers   sync.WaitGroup
}

func New(repository jobs.Store, transcriber stt.Transcriber, queueSize int, logger *slog.Logger) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{repository: repository, transcriber: transcriber, logger: logger, queue: make(chan jobs.Job, queueSize), stop: make(chan struct{}), processCtx: ctx, cancel: cancel, accepting: true}
}

func (p *Pool) Start(workers int) {
	for range workers {
		p.workers.Add(1)
		go p.run()
	}
}

// Submit never blocks an HTTP handler. A full queue is reported to the caller.
func (p *Pool) Submit(job jobs.Job) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.accepting {
		return queue.ErrStopped
	}
	select {
	case p.queue <- job:
		return nil
	default:
		return queue.ErrFull
	}
}

// Restore feeds jobs found after an unclean restart. It is intentionally run by
// one goroutine, so recovery cannot create a goroutine per pending job.
func (p *Pool) Restore(ctx context.Context, pending []jobs.Job) {
	for _, job := range pending {
		p.mu.Lock()
		if !p.accepting {
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		select {
		case p.queue <- job:
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		}
	}
}

// Shutdown rejects new jobs, drains queued work until ctx expires, then cancels
// an in-flight whisper process. Queued jobs remain queued in PostgreSQL.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.accepting = false
	p.mu.Unlock()
	p.stopOnce.Do(func() { close(p.stop) })

	done := make(chan struct{})
	go func() { p.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		p.cancel()
		<-done
		return ctx.Err()
	}
}

func (p *Pool) run() {
	defer p.workers.Done()
	for {
		select {
		case job := <-p.queue:
			p.process(job)
		case <-p.stop:
			// Prefer already queued work during a graceful shutdown.
			select {
			case job := <-p.queue:
				p.process(job)
			default:
				return
			}
		case <-p.processCtx.Done():
			return
		}
	}
}

func (p *Pool) process(job jobs.Job) {
	if err := p.repository.MarkProcessing(p.processCtx, job.ID); err != nil {
		if !errors.Is(err, jobs.ErrNotFound) {
			p.logger.Error("mark job processing", "job_id", job.ID, "error", err)
		}
		return
	}
	text, err := p.transcriber.Transcribe(p.processCtx, job.AudioPath, job.Language)
	if err != nil {
		if p.processCtx.Err() != nil {
			if requeueErr := p.repository.RequeueProcessing(context.Background(), job.ID); requeueErr != nil && !errors.Is(requeueErr, jobs.ErrNotFound) {
				p.logger.Error("requeue interrupted job", "job_id", job.ID, "error", requeueErr)
			}
			return
		}
		if updateErr := p.repository.MarkFailed(context.Background(), job.ID, fmt.Sprintf("transcription failed: %v", err)); updateErr != nil {
			p.logger.Error("mark job failed", "job_id", job.ID, "error", updateErr)
		}
		return
	}
	if err := p.repository.MarkCompleted(context.Background(), job.ID, text); err != nil {
		p.logger.Error("mark job completed", "job_id", job.ID, "error", err)
	}
}
