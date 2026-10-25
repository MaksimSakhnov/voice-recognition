package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voice-api/internal/jobs"
	"voice-api/internal/queue"
	"voice-api/internal/service"
)

type controllerStoreStub struct {
	job     jobs.Job
	getErr  error
	deleted bool
}

func (s *controllerStoreStub) Create(_ context.Context, name, path, language string) (jobs.Job, error) {
	s.job = jobs.Job{ID: "job-1", OriginalFilename: name, AudioPath: path, Language: language, Status: jobs.StatusQueued}
	return s.job, nil
}
func (s *controllerStoreStub) Get(context.Context, string) (jobs.Job, error)       { return s.job, s.getErr }
func (s *controllerStoreStub) List(context.Context, int) ([]jobs.Job, error)       { return nil, nil }
func (s *controllerStoreStub) MarkProcessing(context.Context, string) error        { return nil }
func (s *controllerStoreStub) MarkCompleted(context.Context, string, string) error { return nil }
func (s *controllerStoreStub) MarkFailed(context.Context, string, string) error    { return nil }
func (s *controllerStoreStub) RequeueProcessing(context.Context, string) error     { return nil }
func (s *controllerStoreStub) DeleteQueued(context.Context, string) error {
	s.deleted = true
	return nil
}
func (s *controllerStoreStub) RequeueInterrupted(context.Context) ([]jobs.Job, error) {
	return nil, nil
}
func (s *controllerStoreStub) Search(context.Context, string, int) ([]jobs.SearchResult, error) {
	return nil, nil
}

type controllerSubmitterStub struct{ err error }

func (s controllerSubmitterStub) Submit(jobs.Job) error { return s.err }

func controllerHandler(t *testing.T, store jobs.Store, submitter queue.Submitter) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	application := service.NewJobService(store, submitter, t.TempDir(), logger)
	return NewHTTPController(application, 1024, logger).Routes()
}

func multipartRequest(t *testing.T, filename string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("RIFF test wav")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/audio?language=ru", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestCreateJobReturnsServiceUnavailableWhenQueueIsFull(t *testing.T) {
	store := &controllerStoreStub{}
	recorder := httptest.NewRecorder()
	controllerHandler(t, store, controllerSubmitterStub{err: queue.ErrFull}).ServeHTTP(recorder, multipartRequest(t, "sample.wav"))
	if recorder.Code != http.StatusServiceUnavailable || !store.deleted {
		t.Fatalf("status = %d, deleted = %t", recorder.Code, store.deleted)
	}
}

func TestCreateJobRejectsUnsupportedExtension(t *testing.T) {
	recorder := httptest.NewRecorder()
	controllerHandler(t, &controllerStoreStub{}, controllerSubmitterStub{}).ServeHTTP(recorder, multipartRequest(t, "sample.mp3"))
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnsupportedMediaType)
	}
}

func TestTranscriptReturnsConflictUntilCompleted(t *testing.T) {
	store := &controllerStoreStub{job: jobs.Job{ID: "job-1", Status: jobs.StatusProcessing}}
	recorder := httptest.NewRecorder()
	controllerHandler(t, store, controllerSubmitterStub{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jobs/job-1/transcript", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestTranscriptReturnsTextWhenCompleted(t *testing.T) {
	transcript := "распознанный текст"
	store := &controllerStoreStub{job: jobs.Job{ID: "job-1", Status: jobs.StatusCompleted, Transcript: &transcript}}
	recorder := httptest.NewRecorder()
	controllerHandler(t, store, controllerSubmitterStub{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jobs/job-1/transcript", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["text"] != transcript {
		t.Fatalf("response = %q, error = %v", recorder.Body.String(), err)
	}
}

func TestOpenAPISpecIsServed(t *testing.T) {
	recorder := httptest.NewRecorder()
	controllerHandler(t, &controllerStoreStub{}, controllerSubmitterStub{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "openapi: 3.0.3") {
		t.Fatal("OpenAPI document is missing its version")
	}
}

func TestSwaggerUIIsServed(t *testing.T) {
	recorder := httptest.NewRecorder()
	controllerHandler(t, &controllerStoreStub{}, controllerSubmitterStub{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "SwaggerUIBundle") {
		t.Fatal("Swagger UI HTML was not returned")
	}
}

var _ jobs.Store = (*controllerStoreStub)(nil)
var _ queue.Submitter = controllerSubmitterStub{}
