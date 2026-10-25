package stt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Transcriber interface {
	Transcribe(context.Context, string, string) (string, error)
}
type WhisperCPP struct {
	binary, model string
	threads       int
}

func NewWhisperCPP(binary, model string, threads int) WhisperCPP {
	return WhisperCPP{binary: binary, model: model, threads: threads}
}
func (w WhisperCPP) Validate() error {
	if _, err := exec.LookPath(w.binary); err != nil {
		return fmt.Errorf("find whisper binary %q: %w", w.binary, err)
	}
	info, err := os.Stat(w.model)
	if err != nil {
		return fmt.Errorf("read whisper model %q: %w", w.model, err)
	}
	if info.IsDir() {
		return fmt.Errorf("whisper model %q is a directory", w.model)
	}
	return nil
}
func (w WhisperCPP) Transcribe(ctx context.Context, audioPath, language string) (string, error) {
	outputFile, err := os.CreateTemp("", "whisper-transcript-*")
	if err != nil {
		return "", fmt.Errorf("create transcript output: %w", err)
	}
	outputPath := outputFile.Name()
	if err := outputFile.Close(); err != nil {
		return "", fmt.Errorf("close transcript output: %w", err)
	}
	defer os.Remove(outputPath)
	defer os.Remove(outputPath + ".txt")

	output, err := exec.CommandContext(ctx, w.binary, "-m", w.model, "-f", audioPath, "-l", language, "-t", fmt.Sprint(w.threads), "--no-timestamps", "-otxt", "-of", outputPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("whisper.cpp: %w: %s", err, strings.TrimSpace(string(output)))
	}
	transcript, err := os.ReadFile(outputPath + ".txt")
	if err != nil {
		return "", fmt.Errorf("read whisper transcript: %w", err)
	}
	text := strings.TrimSpace(string(transcript))
	if text == "" {
		return "", fmt.Errorf("whisper.cpp returned empty transcript")
	}
	return text, nil
}
