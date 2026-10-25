// Package queue defines the boundary between job orchestration and its executor.
package queue

import (
	"errors"

	"voice-api/internal/jobs"
)

var ErrFull = errors.New("recognition queue is full")
var ErrStopped = errors.New("recognition queue is stopping")

// Submitter accepts a job for asynchronous processing.
type Submitter interface {
	Submit(jobs.Job) error
}
