package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	UploadDir       string
	MaxUploadBytes  int64
	WhisperBinary   string
	WhisperModel    string
	WhisperThreads  int
	Workers         int
	QueueSize       int
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
}

func Load() (Config, error) {
	maxUploadMB, err := int64Env("MAX_UPLOAD_MB", 100)
	if err != nil {
		return Config{}, err
	}
	const bytesPerMiB int64 = 1024 * 1024
	if maxUploadMB < 1 || maxUploadMB > (1<<63-1)/bytesPerMiB {
		return Config{}, fmt.Errorf("MAX_UPLOAD_MB must be a positive value that fits in bytes")
	}
	whisperThreads, err := intEnv("WHISPER_THREADS", 6)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", 20*time.Second)
	if err != nil {
		return Config{}, err
	}
	workers, err := intEnv("WORKERS", 2)
	if err != nil {
		return Config{}, err
	}
	queueSize, err := intEnv("QUEUE_SIZE", 50)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		HTTPAddr:       env("HTTP_ADDR", ":18080"),
		DatabaseURL:    env("DATABASE_URL", "postgres://voice:voice@localhost:5432/voice?sslmode=disable"),
		UploadDir:      env("UPLOAD_DIR", "./data/audio"),
		MaxUploadBytes: maxUploadMB * bytesPerMiB,
		WhisperBinary:  env("WHISPER_BINARY", "whisper-cli"), WhisperModel: env("WHISPER_MODEL", "./models/ggml-large-v3-turbo-q8_0.bin"),
		WhisperThreads: whisperThreads, ShutdownTimeout: shutdownTimeout,
		Workers:   workers,
		QueueSize: queueSize,
		LogLevel:  logLevel(env("LOG_LEVEL", "INFO")),
	}
	if cfg.MaxUploadBytes < 1 || cfg.WhisperThreads < 1 || cfg.Workers < 1 || cfg.QueueSize < 1 || cfg.ShutdownTimeout <= 0 || cfg.DatabaseURL == "" || cfg.UploadDir == "" {
		return Config{}, fmt.Errorf("MAX_UPLOAD_MB, WHISPER_THREADS, WORKERS, QUEUE_SIZE, and SHUTDOWN_TIMEOUT must be positive; DATABASE_URL and UPLOAD_DIR are required")
	}
	return cfg, nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func intEnv(key string, fallback int) (int, error) {
	raw := env(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}
func int64Env(key string, fallback int64) (int64, error) {
	raw := env(key, strconv.FormatInt(fallback, 10))
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}
func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := env(key, fallback.String())
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return value, nil
}
func logLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}
