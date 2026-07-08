package watcher_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/platform/fake"
	"github.com/mateusgms/cardpit/core/internal/watcher"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func setup(t *testing.T) (*watcher.Watcher, *bus.Bus, string) {
	t.Helper()
	root := t.TempDir()
	p := fake.New(root, t.TempDir())
	b := bus.New()
	w := watcher.New(p, b, watcher.Options{PollInterval: time.Second, Debounce: 3 * time.Second}, discard)
	return w, b, root
}

func insert(t *testing.T, root, slot, card string) {
	t.Helper()
	dir := filepath.Join(root, slot, card)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "IMG_0001.JPG"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func drain(s *bus.Subscription) []bus.Event {
	var out []bus.Event
	for {
		select {
		case e := <-s.C:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestDebouncePromotesAfterWindow(t *testing.T) {
	w, b, root := setup(t)
	sub := b.Subscribe(16)
	defer sub.Close()
	ctx := context.Background()
	t0 := time.Now()

	insert(t, root, "slot1", "CARD01")
	w.Poll(ctx, t0)
	w.Poll(ctx, t0.Add(2*time.Second)) // debounce not yet elapsed
	if evs := drain(sub); len(evs) != 0 {
		t.Fatalf("attached too early: %v", evs)
	}
	w.Poll(ctx, t0.Add(3*time.Second))
	evs := drain(sub)
	if len(evs) != 1 || evs[0].Topic != bus.TopicVolumeAttached {
		t.Fatalf("events: %v", evs)
	}
	att := evs[0].Payload.(bus.VolumeAttached)
	if att.SlotLocationPath != "FAKE#slot1" || att.Label != "CARD01" || att.Serial == "" {
		t.Fatalf("payload: %+v", att)
	}
	// No duplicate attach on further polls.
	w.Poll(ctx, t0.Add(5*time.Second))
	if evs := drain(sub); len(evs) != 0 {
		t.Fatalf("duplicate attach: %v", evs)
	}
}

func TestBlipEmitsNothing(t *testing.T) {
	w, b, root := setup(t)
	sub := b.Subscribe(16)
	defer sub.Close()
	ctx := context.Background()
	t0 := time.Now()

	insert(t, root, "slot1", "CARD01")
	w.Poll(ctx, t0)
	os.RemoveAll(filepath.Join(root, "slot1", "CARD01")) // gone before debounce
	w.Poll(ctx, t0.Add(1*time.Second))
	w.Poll(ctx, t0.Add(4*time.Second))
	if evs := drain(sub); len(evs) != 0 {
		t.Fatalf("blip produced events: %v", evs)
	}
}

func TestDetachFiresImmediately(t *testing.T) {
	w, b, root := setup(t)
	sub := b.Subscribe(16)
	defer sub.Close()
	ctx := context.Background()
	t0 := time.Now()

	insert(t, root, "slot1", "CARD01")
	w.Poll(ctx, t0)
	w.Poll(ctx, t0.Add(3*time.Second))
	drain(sub) // consume attach

	os.RemoveAll(filepath.Join(root, "slot1", "CARD01"))
	w.Poll(ctx, t0.Add(4*time.Second))
	evs := drain(sub)
	if len(evs) != 1 || evs[0].Topic != bus.TopicVolumeDetached {
		t.Fatalf("events: %v", evs)
	}
}

func TestPauseSuppressesAttachOnly(t *testing.T) {
	w, b, root := setup(t)
	sub := b.Subscribe(16)
	defer sub.Close()
	ctx := context.Background()
	t0 := time.Now()

	w.SetPaused(true)
	insert(t, root, "slot1", "CARD01")
	w.Poll(ctx, t0)
	w.Poll(ctx, t0.Add(10*time.Second))
	if evs := drain(sub); len(evs) != 0 {
		t.Fatalf("attach while paused: %v", evs)
	}

	// Unpause: still-present card attaches on next poll.
	w.SetPaused(false)
	w.Poll(ctx, t0.Add(11*time.Second))
	evs := drain(sub)
	if len(evs) != 1 || evs[0].Topic != bus.TopicVolumeAttached {
		t.Fatalf("after unpause: %v", evs)
	}
}

func TestTwoCardsTwoAttaches(t *testing.T) {
	w, b, root := setup(t)
	sub := b.Subscribe(16)
	defer sub.Close()
	ctx := context.Background()
	t0 := time.Now()

	insert(t, root, "slot1", "CARD01")
	insert(t, root, "slot2", "CARD02")
	w.Poll(ctx, t0)
	w.Poll(ctx, t0.Add(3*time.Second))
	evs := drain(sub)
	if len(evs) != 2 {
		t.Fatalf("want 2 attaches, got %v", evs)
	}
	slots := map[string]bool{}
	for _, e := range evs {
		slots[e.Payload.(bus.VolumeAttached).SlotLocationPath] = true
	}
	if !slots["FAKE#slot1"] || !slots["FAKE#slot2"] {
		t.Fatalf("slots: %v", slots)
	}
	if len(w.Snapshot()) != 2 {
		t.Fatalf("snapshot: %+v", w.Snapshot())
	}
}
