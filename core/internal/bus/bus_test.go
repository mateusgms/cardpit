package bus

import (
	"sync"
	"testing"
	"time"
)

func TestFanOutAndTopicFilter(t *testing.T) {
	b := New()
	all := b.Subscribe(8)
	onlyJobs := b.Subscribe(8, TopicJobStarted)
	defer all.Close()
	defer onlyJobs.Close()

	b.Publish(Event{Topic: TopicVolumeAttached, Payload: VolumeAttached{VolumeGUID: "v1"}})
	b.Publish(Event{Topic: TopicJobStarted, Payload: JobEvent{JobID: 1}})

	if e := <-all.C; e.Topic != TopicVolumeAttached {
		t.Fatalf("all: got %s", e.Topic)
	}
	if e := <-all.C; e.Topic != TopicJobStarted {
		t.Fatalf("all: got %s", e.Topic)
	}
	if e := <-onlyJobs.C; e.Topic != TopicJobStarted {
		t.Fatalf("filtered: got %s", e.Topic)
	}
	select {
	case e := <-onlyJobs.C:
		t.Fatalf("filtered sub got extra event %s", e.Topic)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestPublishNeverBlocksAndCountsDrops(t *testing.T) {
	b := New()
	s := b.Subscribe(1)
	defer s.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish(Event{Topic: TopicJobProgress})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on full subscriber")
	}
	if s.Dropped() != 99 {
		t.Fatalf("dropped = %d, want 99", s.Dropped())
	}
}

func TestCloseIsIdempotentAndConcurrentSafe(t *testing.T) {
	b := New()
	s := b.Subscribe(4)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Close()
		}()
	}
	wg.Wait()
	// Publishing after close must not panic (sub removed before channel close).
	b.Publish(Event{Topic: TopicJobStarted})
}

func TestEventTimestampDefaulted(t *testing.T) {
	b := New()
	s := b.Subscribe(1)
	defer s.Close()
	b.Publish(Event{Topic: TopicJobStarted})
	if e := <-s.C; e.At.IsZero() {
		t.Fatal("At not defaulted")
	}
}
