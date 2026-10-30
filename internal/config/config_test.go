package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsInvalidNumericConfiguration(t *testing.T) {
	t.Setenv("WORKERS", "two")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WORKERS") {
		t.Fatalf("Load() error = %v, want WORKERS validation error", err)
	}
}

func TestLoadRejectsNonPositiveShutdownTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "0s")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") {
		t.Fatalf("Load() error = %v, want SHUTDOWN_TIMEOUT validation error", err)
	}
}
