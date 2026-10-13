package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"voice-api/internal/jobs"
)

type Server struct {
	maxBytes  int64
	uploadDir string
	jobs      *jobs.Repository
	logger    *slog.Logger
}

func NewServer(repository *jobs.Repository, maxBytes int64, uploadDir string, logger *slog.Logger) *Server {
	return &Server{jobs: repository, maxBytes: maxBytes, uploadDir: uploadDir, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /audio", s.createJob)
	mux.HandleFunc("GET /jobs", s.listJobs)
	mux.HandleFunc("GET /jobs/{id}", s.getJob)
	mux.HandleFunc("GET /jobs/{id}/transcript", s.getTranscript)
	mux.HandleFunc("GET /search", s.search)
	return s.recover(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytes)
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

	if err := os.MkdirAll(s.uploadDir, 0o750); err != nil {
		s.logger.Error("create upload directory", "error", err)
		fail(w, http.StatusInternalServerError, "prepare audio storage")
		return
	}
	temp, err := os.CreateTemp(s.uploadDir, "audio-*.wav")
	if err != nil {
		s.logger.Error("create temporary audio file", "error", err)
		fail(w, http.StatusInternalServerError, "prepare audio")
		return
	}
	tempPath := temp.Name()
	if _, err = io.Copy(temp, part); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		if errors.As(err, new(*http.MaxBytesError)) {
			fail(w, http.StatusRequestEntityTooLarge, "audio is too large")
		} else {
			fail(w, http.StatusBadRequest, "read audio")
		}
		return
	}
	if err = temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		fail(w, http.StatusInternalServerError, "save audio")
		return
	}

	language := strings.TrimSpace(r.URL.Query().Get("language"))
	if language == "" {
		language = "ru"
	}
	job, err := s.jobs.Create(r.Context(), fileName, tempPath, language)
	if err != nil {
		_ = os.Remove(tempPath)
		s.logger.Error("create job", "file_name", fileName, "error", err)
		fail(w, http.StatusInternalServerError, "create job")
		return
	}
	respond(w, http.StatusCreated, map[string]string{"jobId": job.ID})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := s.jobs.List(r.Context(), queryLimit(r, 100))
	if err != nil {
		s.logger.Error("list jobs", "error", err)
		fail(w, http.StatusInternalServerError, "list jobs")
		return
	}
	respond(w, http.StatusOK, map[string]any{"jobs": items})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, jobs.ErrNotFound) {
		fail(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		s.logger.Error("get job", "error", err)
		fail(w, http.StatusInternalServerError, "get job")
		return
	}
	respond(w, http.StatusOK, job)
}

func (s *Server) getTranscript(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, jobs.ErrNotFound) {
		fail(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		s.logger.Error("get transcript", "error", err)
		fail(w, http.StatusInternalServerError, "get transcript")
		return
	}
	if job.Status != jobs.StatusCompleted || job.Transcript == nil {
		fail(w, http.StatusConflict, "transcript is not ready; job status: "+string(job.Status))
		return
	}
	respond(w, http.StatusOK, map[string]string{"jobId": job.ID, "text": *job.Transcript})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		fail(w, http.StatusBadRequest, "query parameter q is required")
		return
	}
	items, err := s.jobs.Search(r.Context(), query, queryLimit(r, 50))
	if err != nil {
		s.logger.Error("search transcripts", "error", err)
		fail(w, http.StatusInternalServerError, "search transcripts")
		return
	}
	respond(w, http.StatusOK, map[string]any{"results": items})
}

func queryLimit(r *http.Request, fallback int) int {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return limit
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "error", recovered)
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
