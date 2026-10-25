// Package controller adapts HTTP requests to application services.
package controller

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"voice-api/internal/jobs"
	"voice-api/internal/service"

	httpSwagger "github.com/swaggo/http-swagger/v2"
)

type HTTPController struct {
	jobs     *service.JobService
	maxBytes int64
	logger   *slog.Logger
}

func NewHTTPController(jobs *service.JobService, maxBytes int64, logger *slog.Logger) *HTTPController {
	return &HTTPController{jobs: jobs, maxBytes: maxBytes, logger: logger}
}

func (c *HTTPController) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", c.health)
	mux.HandleFunc("GET /openapi.yaml", c.openAPI)
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.URL("/openapi.yaml")))
	mux.HandleFunc("POST /audio", c.createJob)
	mux.HandleFunc("GET /jobs", c.listJobs)
	mux.HandleFunc("GET /jobs/{id}", c.getJob)
	mux.HandleFunc("GET /jobs/{id}/transcript", c.getTranscript)
	mux.HandleFunc("GET /search", c.search)
	return c.recover(mux)
}

func (c *HTTPController) health(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *HTTPController) createJob(w http.ResponseWriter, r *http.Request) {
	if !c.jobs.IsAccepting() {
		fail(w, http.StatusServiceUnavailable, "service is shutting down")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, c.maxBytes)
	defer r.Body.Close()
	reader, err := r.MultipartReader()
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			fail(w, http.StatusRequestEntityTooLarge, "audio is too large")
			return
		}
		fail(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" {
		fail(w, http.StatusBadRequest, "multipart field file is required")
		return
	}
	defer part.Close()
	fileName := filepath.Base(part.FileName())
	if strings.ToLower(filepath.Ext(fileName)) != ".wav" {
		fail(w, http.StatusUnsupportedMediaType, "only .wav files are supported")
		return
	}
	job, err := c.jobs.CreateAudio(r.Context(), fileName, r.URL.Query().Get("language"), part)
	if err != nil {
		c.writeCreateError(w, err)
		return
	}
	respond(w, http.StatusCreated, map[string]string{"jobId": job.ID})
}

func (c *HTTPController) writeCreateError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytesError):
		fail(w, http.StatusRequestEntityTooLarge, "audio is too large")
	case service.IsQueueFull(err):
		fail(w, http.StatusServiceUnavailable, "recognition queue is full")
	case service.IsStopping(err):
		fail(w, http.StatusServiceUnavailable, "service is shutting down")
	case errors.Is(err, io.ErrUnexpectedEOF):
		fail(w, http.StatusBadRequest, "read audio")
	default:
		c.logger.Error("create job", "error", err)
		fail(w, http.StatusInternalServerError, "create job")
	}
}

func (c *HTTPController) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := c.jobs.List(r.Context(), queryLimit(r, 100))
	if err != nil {
		c.logger.Error("list jobs", "error", err)
		fail(w, http.StatusInternalServerError, "list jobs")
		return
	}
	respond(w, http.StatusOK, map[string]any{"jobs": items})
}

func (c *HTTPController) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := c.jobs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, jobs.ErrNotFound) {
		fail(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		c.logger.Error("get job", "error", err)
		fail(w, http.StatusInternalServerError, "get job")
		return
	}
	respond(w, http.StatusOK, job)
}

func (c *HTTPController) getTranscript(w http.ResponseWriter, r *http.Request) {
	text, err := c.jobs.Transcript(r.Context(), r.PathValue("id"))
	if errors.Is(err, jobs.ErrNotFound) {
		fail(w, http.StatusNotFound, "job not found")
		return
	}
	if errors.Is(err, service.ErrTranscriptNotReady) {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		c.logger.Error("get transcript", "error", err)
		fail(w, http.StatusInternalServerError, "get transcript")
		return
	}
	respond(w, http.StatusOK, map[string]string{"jobId": r.PathValue("id"), "text": text})
}

func (c *HTTPController) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		fail(w, http.StatusBadRequest, "query parameter q is required")
		return
	}
	items, err := c.jobs.Search(r.Context(), query, queryLimit(r, 50))
	if err != nil {
		c.logger.Error("search transcripts", "error", err)
		fail(w, http.StatusInternalServerError, "search transcripts")
		return
	}
	respond(w, http.StatusOK, map[string]any{"results": items})
}

func queryLimit(r *http.Request, fallback int) int {
	value := r.URL.Query().Get("limit")
	limit, err := strconv.Atoi(value)
	if value == "" || err != nil {
		return fallback
	}
	return limit
}

func (c *HTTPController) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				c.logger.Error("request panic", "error", recovered)
				fail(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}
