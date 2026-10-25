package jobs

import "time"

type Status string

const (
	StatusQueued     Status = "queued"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

type Job struct {
	ID               string     `json:"jobId"`
	OriginalFilename string     `json:"fileName"`
	AudioPath        string     `json:"-"`
	Language         string     `json:"language"`
	Status           Status     `json:"status"`
	Transcript       *string    `json:"-"`
	ErrorMessage     *string    `json:"error,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	FinishedAt       *time.Time `json:"finishedAt,omitempty"`
}

type SearchResult struct {
	JobID      string    `json:"jobId"`
	FileName   string    `json:"fileName"`
	Fragment   string    `json:"fragment"`
	FinishedAt time.Time `json:"finishedAt"`
}
