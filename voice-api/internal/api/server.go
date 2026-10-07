package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"voice-api/internal/stt"
)

type Server struct {
	transcriber stt.Transcriber
	maxBytes    int64
	logger      *slog.Logger
}

func NewServer(transcriber stt.Transcriber, maxBytes int64, logger *slog.Logger) *Server {
	return &Server{transcriber: transcriber, maxBytes: maxBytes, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /v1/transcriptions", s.transcribe)
	return s.recover(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) transcribe(w http.ResponseWriter, r *http.Request) {
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

	temp, err := os.CreateTemp("", "voice-api-*.wav")
	if err != nil {
		s.logger.Error("create temporary audio file", "error", err)
		fail(w, http.StatusInternalServerError, "prepare audio")
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err = io.Copy(temp, part); err != nil {
		_ = temp.Close()
		if errors.As(err, new(*http.MaxBytesError)) {
			fail(w, http.StatusRequestEntityTooLarge, "audio is too large")
		} else {
			fail(w, http.StatusBadRequest, "read audio")
		}
		return
	}
	if err = temp.Close(); err != nil {
		fail(w, http.StatusInternalServerError, "save audio")
		return
	}

	language := strings.TrimSpace(r.URL.Query().Get("language"))
	if language == "" {
		language = "ru"
	}
	text, err := s.transcriber.Transcribe(r.Context(), tempPath, language)
	if err != nil {
		s.logger.Error("transcription failed", "file_name", fileName, "error", err)
		fail(w, http.StatusUnprocessableEntity, "speech recognition failed")
		return
	}
	respond(w, http.StatusOK, map[string]string{"text": text})
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
