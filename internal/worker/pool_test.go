package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"voice-api/internal/jobs"
	"voice-api/internal/stt"
)

type workerStoreStub struct {
	mu         sync.Mutex
	processing []string
	completed  chan string
	failed     chan string
}

func (s *workerStoreStub) Create(context.Context, string, string, string) (jobs.Job, error) {
	return jobs.Job{}, nil
}
func (s *workerStoreStub) Get(context.Context, string) (jobs.Job, error) {
	return jobs.Job{}, jobs.ErrNotFound
}
func (s *workerStoreStub) List(context.Context, int) ([]jobs.Job, error) { return nil, nil }
func (s *workerStoreStub) MarkProcessing(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processing = append(s.processing, id)
	return nil
}
func (s *workerStoreStub) MarkCompleted(_ context.Context, _ string, transcript string) error {
	s.completed <- transcript
	return nil
}
func (s *workerStoreStub) MarkFailed(_ context.Context, _ string, message string) error {
	s.failed <- message
	return nil
}
func (s *workerStoreStub) RequeueProcessing(context.Context, string) error        { return nil }
func (s *workerStoreStub) DeleteQueued(context.Context, string) error             { return nil }
func (s *workerStoreStub) RequeueInterrupted(context.Context) ([]jobs.Job, error) { return nil, nil }
func (s *workerStoreStub) Search(context.Context, string, int) ([]jobs.SearchResult, error) {
	return nil, nil
}

type transcriberStub struct {
	text string
	err  error
}

type blockingTranscriber struct {
	started chan struct{}
	block   chan struct{}
}

func (t blockingTranscriber) Transcribe(context.Context, string, string) (string, error) {
	close(t.started)
	<-t.block
	return "", context.Canceled
}

func (t transcriberStub) Transcribe(context.Context, string, string) (string, error) {
	return t.text, t.err
}

func newTestPool(store jobs.Store, transcriber stt.Transcriber) *Pool {
	return New(store, transcriber, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func shutdownPool(t *testing.T, pool *Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPoolCompletesJob(t *testing.T) {
	store := &workerStoreStub{completed: make(chan string, 1), failed: make(chan string, 1)}
	pool := newTestPool(store, transcriberStub{text: "готовый текст"})
	pool.Start(1)
	defer shutdownPool(t, pool)

	if err := pool.Submit(jobs.Job{ID: "job-1", AudioPath: "audio.wav", Language: "ru"}); err != nil {
		t.Fatal(err)
	}
	select {
	case transcript := <-store.completed:
		if transcript != "готовый текст" {
			t.Fatalf("transcript = %q", transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("job was not completed")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.processing) != 1 || store.processing[0] != "job-1" {
		t.Fatalf("processing = %#v", store.processing)
	}
}

func TestPoolMarksFailedJobAndKeepsWorkerRunning(t *testing.T) {
	store := &workerStoreStub{completed: make(chan string, 1), failed: make(chan string, 1)}
	pool := newTestPool(store, transcriberStub{err: errors.New("invalid wav")})
	pool.Start(1)
	defer shutdownPool(t, pool)

	if err := pool.Submit(jobs.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-store.failed:
		if message != "transcription failed: invalid wav" {
			t.Fatalf("failure message = %q", message)
		}
	case <-time.After(time.Second):
		t.Fatal("job was not marked failed")
	}
}

func TestShutdownReturnsWhenTranscriberIgnoresCancellation(t *testing.T) {
	store := &workerStoreStub{completed: make(chan string, 1), failed: make(chan string, 1)}
	transcriber := blockingTranscriber{started: make(chan struct{}), block: make(chan struct{})}
	pool := newTestPool(store, transcriber)
	pool.Start(1)
	if err := pool.Submit(jobs.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-transcriber.started:
	case <-time.After(time.Second):
		t.Fatal("transcriber did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := pool.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}
	close(transcriber.block)
}

var _ jobs.Store = (*workerStoreStub)(nil)
var _ stt.Transcriber = transcriberStub{}
