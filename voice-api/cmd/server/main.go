package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"voice-api/internal/api"
	"voice-api/internal/config"
	"voice-api/internal/stt"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	transcriber := stt.NewWhisperCPP(cfg.WhisperBinary, cfg.WhisperModel, cfg.WhisperThreads)
	if err := transcriber.Validate(); err != nil {
		fatal(err)
	}
	handler := api.NewServer(transcriber, cfg.MaxUploadBytes, logger)
	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: handler.Routes(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("voice API started", "address", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-signalContext.Done()
	logger.Info("shutdown signal received")

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
	}
}

func fatal(err error) { slog.Error("startup failed", "error", err); os.Exit(1) }
