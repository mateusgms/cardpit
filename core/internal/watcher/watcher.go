// Package watcher polls the platform for removable volumes and turns raw
// presence into debounced attach/detach events on the bus.
//
// State machine per volume (keyed by GUIDPath):
//
//	absent → candidate (first seen; debounce running)
//	candidate → attached (still present after debounce; info read OK) → publish volume.attached
//	candidate → absent (blip; no event)
//	attached → absent (gone from a poll) → publish volume.detached immediately
package watcher

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/platform"
)

type Options struct {
	PollInterval time.Duration
	Debounce     time.Duration
}

// VolumeState is a snapshot entry for the status API.
type VolumeState struct {
	VolumeGUID string              `json:"volume_guid"`
	Attached   bool                `json:"attached"` // false = still debouncing
	Info       platform.VolumeInfo `json:"info"`
	Slot       platform.SlotKey    `json:"slot"`
}

type volState struct {
	id           platform.VolumeID
	firstSeen    time.Time
	attached     bool
	info         platform.VolumeInfo
	slot         platform.SlotKey
	slotAttempts int
}

type Watcher struct {
	p   platform.Platform
	bus *bus.Bus
	opt Options
	log *slog.Logger

	paused atomic.Bool

	mu     sync.Mutex
	states map[string]*volState
}

func New(p platform.Platform, b *bus.Bus, opt Options, log *slog.Logger) *Watcher {
	if opt.PollInterval <= 0 {
		opt.PollInterval = 2 * time.Second
	}
	return &Watcher{p: p, bus: b, opt: opt, log: log, states: make(map[string]*volState)}
}

// SetPaused stops promoting new volumes to attached ("pausar detecção").
// Detach detection keeps running; on unpause, cards still inserted attach on
// the next poll.
func (w *Watcher) SetPaused(v bool) { w.paused.Store(v) }
func (w *Watcher) Paused() bool     { return w.paused.Load() }

func (w *Watcher) Run(ctx context.Context) error {
	// Prime already-mounted media immediately. The first pass only creates
	// debounce candidates, giving event consumers time to subscribe.
	w.poll(ctx, time.Now())
	t := time.NewTicker(w.opt.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			w.poll(ctx, now)
		}
	}
}

// poll performs one scan cycle. Exported to tests via export_test.go.
func (w *Watcher) poll(ctx context.Context, now time.Time) {
	vols, err := w.p.Volumes.ListRemovableVolumes(ctx)
	if err != nil {
		w.log.Error("watcher: listing volumes", "err", err)
		return
	}
	present := make(map[string]platform.VolumeID, len(vols))
	for _, v := range vols {
		present[v.GUIDPath] = v
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// New volumes and debounce promotion.
	for guid, id := range present {
		st, ok := w.states[guid]
		if !ok {
			w.states[guid] = &volState{id: id, firstSeen: now}
			w.log.Info("watcher: volume candidate", "volume", guid)
			continue
		}
		if st.attached || w.paused.Load() {
			continue
		}
		if now.Sub(st.firstSeen) < w.opt.Debounce {
			continue
		}
		// Debounce elapsed: read info. Failure (media not ready yet) leaves
		// the volume a candidate; the next poll retries naturally.
		info, err := w.p.Info.VolumeInfo(ctx, id)
		if err != nil {
			w.log.Warn("watcher: volume info not ready", "volume", guid, "err", err)
			continue
		}
		slot, err := w.p.Slots.ResolveSlot(ctx, id)
		if err != nil {
			st.slotAttempts++
			// Device-tree interfaces can lag behind an already-mounted volume at
			// boot. Retry for a bounded grace period, then preserve the PRD's
			// degraded ingest behavior.
			if now.Sub(st.firstSeen) < w.opt.Debounce+5*time.Second {
				w.log.Debug("watcher: slot resolution not ready; retrying", "volume", guid, "attempt", st.slotAttempts, "err", err)
				continue
			}
			w.log.Warn("watcher: slot resolution failed", "volume", guid, "attempts", st.slotAttempts, "err", err)
			slot = platform.SlotKey{}
		}
		st.attached = true
		st.info = info
		st.slot = slot
		w.log.Info("watcher: volume attached",
			"volume", guid, "serial", info.Serial, "label", info.Label,
			"location_path", slot.LocationPath, "lun", slot.LUN)
		w.bus.Publish(bus.Event{Topic: bus.TopicVolumeAttached, At: now, Payload: bus.VolumeAttached{
			VolumeGUID:       guid,
			Root:             info.Root,
			Serial:           info.Serial,
			Label:            info.Label,
			Filesystem:       info.Filesystem,
			TotalBytes:       info.TotalBytes,
			FreeBytes:        info.FreeBytes,
			SlotLocationPath: slot.LocationPath,
			SlotLUN:          slot.LUN,
		}})
	}

	// Departed volumes.
	for guid, st := range w.states {
		if _, ok := present[guid]; ok {
			continue
		}
		delete(w.states, guid)
		if st.attached {
			w.log.Info("watcher: volume detached", "volume", guid)
			w.bus.Publish(bus.Event{Topic: bus.TopicVolumeDetached, At: now,
				Payload: bus.VolumeDetached{VolumeGUID: guid}})
		}
	}
}

// Snapshot returns the current view of tracked volumes.
func (w *Watcher) Snapshot() []VolumeState {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]VolumeState, 0, len(w.states))
	for guid, st := range w.states {
		out = append(out, VolumeState{
			VolumeGUID: guid, Attached: st.attached, Info: st.info, Slot: st.slot,
		})
	}
	return out
}
