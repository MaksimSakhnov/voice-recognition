package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"voice-api/internal/jobs"
	"voice-api/internal/queue"
)

type storeStub struct {
	created jobs.Job
	get     jobs.Job
	getErr  error
	deleted string
}

func (s *storeStub) Create(_ context.Context, name, path, language string) (jobs.Job, error) {
	s.created = jobs.Job{ID: "job-1", OriginalFilename: name, AudioPath: path, Language: language, Status: jobs.StatusQueued}
	return s.created, nil
}
func (s *storeStub) Get(context.Context, string) (jobs.Job, error)          { return s.get, s.getErr }
func (s *storeStub) List(context.Context, int) ([]jobs.Job, error)          { return nil, nil }
func (s *storeStub) MarkProcessing(context.Context, string) error           { return nil }
func (s *storeStub) MarkCompleted(context.Context, string, string) error    { return nil }
func (s *storeStub) MarkFailed(context.Context, string, string) error       { return nil }
func (s *storeStub) RequeueProcessing(context.Context, string) error        { return nil }
func (s *storeStub) DeleteQueued(_ context.Context, id string) error        { s.deleted = id; return nil }
func (s *storeStub) RequeueInterrupted(context.Context) ([]jobs.Job, error) { return nil, nil }
func (s *storeStub) Search(context.Context, string, int) ([]jobs.SearchResult, error) {
	return nil, nil
}

type submitterStub struct {
	err error
	job jobs.Job
}

func (s *submitterStub) Submit(job jobs.Job) error { s.job = job; return s.err }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestCreateAudioSavesAndSubmits(t *testing.T) {
	store := &storeStub{}
	submitter := &submitterStub{}
	service := NewJobService(store, submitter, t.TempDir(), testLogger())

	job, err := service.CreateAudio(context.Background(), "sample.wav", " en ", strings.NewReader("audio data"))
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "job-1" || submitter.job.ID != job.ID || store.created.Language != "en" {
		t.Fatalf("unexpected created job: %#v, submitted: %#v", job, submitter.job)
	}
	content, err := os.ReadFile(job.AudioPath)
	if err != nil || string(content) != "audio data" {
		t.Fatalf("saved audio = %q, err = %v", content, err)
	}
}

func TestCreateAudioCleansUpWhenQueueIsFull(t *testing.T) {
	store := &storeStub{}
	service := NewJobService(store, &submitterStub{err: queue.ErrFull}, t.TempDir(), testLogger())

	_, err := service.CreateAudio(context.Background(), "sample.wav", "", strings.NewReader("audio data"))
	if !errors.Is(err, queue.ErrFull) {
		t.Fatalf("error = %v, want queue full", err)
	}
	if store.deleted != "job-1" {
		t.Fatalf("deleted job = %q, want job-1", store.deleted)
	}
	files, readErr := os.ReadDir(service.uploadDir)
	if readErr != nil || len(files) != 0 {
		t.Fatalf("temporary files = %v, err = %v", files, readErr)
	}
}

func TestTranscript(t *testing.T) {
	text := "готовый текст"
	store := &storeStub{get: jobs.Job{ID: "job-1", Status: jobs.StatusCompleted, Transcript: &text}}
	service := NewJobService(store, &submitterStub{}, t.TempDir(), testLogger())

	got, err := service.Transcript(context.Background(), "job-1")
	if err != nil || got != text {
		t.Fatalf("Transcript() = %q, %v", got, err)
	}
	store.get = jobs.Job{ID: "job-1", Status: jobs.StatusProcessing}
	if _, err := service.Transcript(context.Background(), "job-1"); !errors.Is(err, ErrTranscriptNotReady) {
		t.Fatalf("error = %v, want ErrTranscriptNotReady", err)
	}
}

func TestCreateAudioRejectsDuringShutdown(t *testing.T) {
	service := NewJobService(&storeStub{}, &submitterStub{}, t.TempDir(), testLogger())
	service.StopAccepting()
	_, err := service.CreateAudio(context.Background(), "sample.wav", "ru", strings.NewReader("audio"))
	if !errors.Is(err, ErrNotAccepting) {
		t.Fatalf("error = %v, want ErrNotAccepting", err)
	}
}

var _ jobs.Store = (*storeStub)(nil)
var _ queue.Submitter = (*submitterStub)(nil)
