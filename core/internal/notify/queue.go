package notify

import (
	"context"
	"log/slog"
	"time"
)

// queue is a bounded, serial outbox with per-task retry. Serial execution
// preserves ordering (start message before its progress edits); tasks with
// retries left are re-enqueued after an exponential backoff, capped at 5
// minutes. Progress edits carry retries=0 — a superseded edit is worthless.
type queue struct {
	tasks chan task
	log   *slog.Logger
}

type task struct {
	desc    string
	retries int
	backoff time.Duration
	run     func(ctx context.Context) error
}

const maxBackoff = 5 * time.Minute

func newQueue(size int, log *slog.Logger) *queue {
	return &queue{tasks: make(chan task, size), log: log}
}

// enqueue never blocks: when the outbox is full the task is dropped with a
// log (Telegram being down must not back-pressure the engine).
func (q *queue) enqueue(t task) {
	if t.backoff <= 0 {
		t.backoff = 5 * time.Second
	}
	select {
	case q.tasks <- t:
	default:
		q.log.Warn("notify: outbox full, dropping notification", "task", t.desc)
	}
}

func (q *queue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-q.tasks:
			err := t.run(ctx)
			if err == nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if t.retries <= 0 {
				q.log.Warn("notify: notification dropped", "task", t.desc, "err", err)
				continue
			}
			q.log.Warn("notify: send failed, will retry",
				"task", t.desc, "in", t.backoff, "retries_left", t.retries, "err", err)
			retry := task{
				desc: t.desc, retries: t.retries - 1,
				backoff: min(t.backoff*2, maxBackoff), run: t.run,
			}
			delay := t.backoff
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(delay):
					q.enqueue(retry)
				}
			}()
		}
	}
}
