// Пакет queue определяет границу между оркестрацией заданий и их исполнителем.
package queue

import (
	"errors"

	"voice-api/internal/jobs"
)

var ErrFull = errors.New("recognition queue is full")
var ErrStopped = errors.New("recognition queue is stopping")

// Submitter принимает задание для асинхронной обработки.
type Submitter interface {
	Submit(jobs.Job) error
}
