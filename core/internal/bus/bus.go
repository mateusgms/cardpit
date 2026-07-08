// Package bus is the internal pub/sub event backbone. The watcher publishes
// volume events, the job manager publishes job lifecycle events, and the
// Telegram notifier, SSE endpoint and future webhooks are plain subscribers.
//
// Publish never blocks: a subscriber whose buffer is full loses the event
// (a drop counter is kept per subscription). The copy path must never stall
// because a notification consumer is slow or offline.
package bus

import (
	"sync"
	"sync/atomic"
	"time"
)

type Topic string

const (
	TopicVolumeAttached Topic = "volume.attached"
	TopicVolumeDetached Topic = "volume.detached"
	TopicJobStarted     Topic = "job.started"
	TopicJobProgress    Topic = "job.progress"
	TopicJobCompleted   Topic = "job.completed"
	TopicJobFailed      Topic = "job.failed"
	TopicCardUnknown    Topic = "card.unknown"
	TopicCardDecision   Topic = "card.decision"
	TopicDestMissing    Topic = "dest.missing"
	TopicSlotCalibrated Topic = "slot.calibrated"
)

type Event struct {
	Topic   Topic
	At      time.Time
	Payload any // one concrete struct per topic; see events.go
}

type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscription]struct{}
}

func New() *Bus {
	return &Bus{subs: make(map[*Subscription]struct{})}
}

// Subscription receives events on C until Close is called. Events published
// while C's buffer is full are dropped for this subscriber only.
type Subscription struct {
	C       <-chan Event
	c       chan Event
	bus     *Bus
	topics  map[Topic]struct{} // nil = all topics
	dropped atomic.Int64
	once    sync.Once
}

// Subscribe registers a subscriber with the given buffer size. With no
// topics, every event is delivered.
func (b *Bus) Subscribe(buf int, topics ...Topic) *Subscription {
	s := &Subscription{c: make(chan Event, buf), bus: b}
	s.C = s.c
	if len(topics) > 0 {
		s.topics = make(map[Topic]struct{}, len(topics))
		for _, t := range topics {
			s.topics[t] = struct{}{}
		}
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s)
		s.bus.mu.Unlock()
		close(s.c)
	})
}

// Dropped reports how many events were lost to a full buffer.
func (s *Subscription) Dropped() int64 { return s.dropped.Load() }

// Publish delivers e to every matching subscriber without ever blocking.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if s.topics != nil {
			if _, ok := s.topics[e.Topic]; !ok {
				continue
			}
		}
		select {
		case s.c <- e:
		default:
			s.dropped.Add(1)
		}
	}
}
