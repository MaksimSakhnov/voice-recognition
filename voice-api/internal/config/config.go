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
	MaxUploadBytes  int64
	WhisperBinary   string
	WhisperModel    string
	WhisperThreads  int
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:       env("HTTP_ADDR", ":8080"),
		MaxUploadBytes: int64Env("MAX_UPLOAD_MB", 100) * 1024 * 1024,
		WhisperBinary:  env("WHISPER_BINARY", "whisper-cli"), WhisperModel: env("WHISPER_MODEL", "./models/ggml-large-v3-turbo-q8_0.bin"),
		WhisperThreads: intEnv("WHISPER_THREADS", 6), ShutdownTimeout: durationEnv("SHUTDOWN_TIMEOUT", 20*time.Second),
		LogLevel: logLevel(env("LOG_LEVEL", "INFO")),
	}
	if cfg.MaxUploadBytes < 1 || cfg.WhisperThreads < 1 {
		return Config{}, fmt.Errorf("MAX_UPLOAD_MB and WHISPER_THREADS must be positive")
	}
	return cfg, nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func intEnv(key string, fallback int) int {
	value, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return value
}
func int64Env(key string, fallback int64) int64 {
	value, err := strconv.ParseInt(env(key, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
func durationEnv(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(env(key, fallback.String()))
	if err != nil {
		return fallback
	}
	return value
}
func logLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}
