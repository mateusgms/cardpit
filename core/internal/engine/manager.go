// Package engine turns volume events into ingest jobs: policy decisions,
// concurrency, dedup, copying with verification, persistence and recovery.
package engine

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// destRetryInterval is how often pending jobs blocked on a missing
// destination SSD are retried. The watcher only reports removable volumes,
// so a fixed destination drive appearing is only noticed here.
const destRetryInterval = 30 * time.Second

type Manager struct {
	db     *store.DB
	p      platform.Platform
	bus    *bus.Bus
	log    *slog.Logger
	copier *copier

	sem  chan struct{}
	kick chan struct{} // wakes retryBlocked outside the 30s tick

	mu       sync.Mutex
	byGUID   map[string]*runningJob // volumes with an admitted job
	blocked  map[int64]queuedJob    // jobID → job waiting for the destination
	awaiting map[string]queuedJob   // card serial → job waiting for a decision
}

type runningJob struct {
	jobID  int64
	cancel context.CancelCauseFunc
}

// queuedJob carries everything needed to (re)start a job later.
type queuedJob struct {
	jobID     int64
	vol       bus.VolumeAttached
	cardAlias string
	slotAlias string
}

func NewManager(db *store.DB, p platform.Platform, b *bus.Bus, log *slog.Logger) *Manager {
	return &Manager{
		db:       db,
		p:        p,
		bus:      b,
		log:      log,
		kick:     make(chan struct{}, 1),
		byGUID:   map[string]*runningJob{},
		blocked:  map[int64]queuedJob{},
		awaiting: map[string]queuedJob{},
	}
}

// Recover is boot cleanup: interrupted jobs become failed, stale questions
// are cancelled, and orphan .cardpit-tmp files on the destination are swept.
func (m *Manager) Recover(ctx context.Context) error {
	n, err := m.db.Jobs.FailInterrupted(ctx, "interrompido por reinício do serviço")
	if err != nil {
		return err
	}
	if n > 0 {
		m.log.Warn("engine: failed interrupted jobs from previous run", "count", n)
	}
	destRoot, err := m.resolveDest(ctx)
	if err != nil {
		return nil // destination absent: nothing to sweep
	}
	swept := 0
	filepath.WalkDir(destRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), tmpSuffix) {
			if rmErr := os.Remove(path); rmErr == nil {
				swept++
			}
		}
		return nil
	})
	if swept > 0 {
		m.log.Info("engine: swept orphan tmp files", "count", swept)
	}
	return nil
}

// Run processes bus events until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	maxJobs := m.db.Settings.GetInt(ctx, store.SetMaxConcurrent, 4)
	if maxJobs < 1 {
		maxJobs = 1
	}
	m.sem = make(chan struct{}, maxJobs)
	m.copier = newCopier()

	sub := m.bus.Subscribe(256,
		bus.TopicVolumeAttached, bus.TopicVolumeDetached, bus.TopicCardDecision)
	defer sub.Close()

	destTicker := time.NewTicker(destRetryInterval)
	defer destTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-sub.C:
			switch p := e.Payload.(type) {
			case bus.VolumeAttached:
				m.handleAttach(ctx, p)
			case bus.VolumeDetached:
				m.handleDetach(ctx, p.VolumeGUID)
			case bus.CardDecision:
				m.handleDecision(ctx, p)
			}
		case <-destTicker.C:
			m.retryBlocked(ctx)
		case <-m.kick:
			m.retryBlocked(ctx)
		}
	}
}

func (m *Manager) handleAttach(ctx context.Context, va bus.VolumeAttached) {
	m.log.Debug("engine: volume attached",
		"volume", va.VolumeGUID, "serial", va.Serial, "label", va.Label, "root", va.Root)
	if m.isTracked(va.VolumeGUID) {
		m.log.Debug("engine: volume already tracked, ignoring", "volume", va.VolumeGUID)
		return
	}
	if m.db.Settings.GetBool(ctx, store.SetRequireDCIM, false) && !hasDCIM(va.Root) {
		m.log.Info("engine: card ignored (no DCIM)", "volume", va.VolumeGUID)
		return
	}

	card, known, err := m.db.Cards.TouchSeen(ctx, va.Serial, va.Label)
	if err != nil {
		m.log.Error("engine: card lookup", "err", err)
		return
	}
	slotAlias, slotID := m.slotAlias(ctx, va)

	switch {
	case known && card.Policy == "ignore":
		m.log.Info("engine: known card with ignore policy", "card", card.Alias)
	case known:
		m.createAndAdmit(ctx, va, card.ID, slotID, card.Alias, slotAlias)
	default:
		policy := m.db.Settings.GetString(ctx, store.SetUnknownCardPolicy, "ask")
		switch policy {
		case "ignore":
			m.log.Info("engine: unknown card ignored by policy", "serial", va.Serial)
		case "copy":
			m.createAndAdmit(ctx, va, 0, slotID, cardDisplayName(va), slotAlias)
		default: // "ask"
			m.askUnknown(ctx, va, slotID, slotAlias)
		}
	}
}

func cardDisplayName(va bus.VolumeAttached) string {
	if va.Label != "" {
		return va.Label
	}
	return va.Serial
}

func (m *Manager) isTracked(guid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byGUID[guid]; ok {
		return true
	}
	for _, q := range m.blocked {
		if q.vol.VolumeGUID == guid {
			return true
		}
	}
	for _, q := range m.awaiting {
		if q.vol.VolumeGUID == guid {
			return true
		}
	}
	return false
}

// slotAlias resolves the human name for the physical slot; uncalibrated
// slots fall back to the raw location path (RF-02.4).
func (m *Manager) slotAlias(ctx context.Context, va bus.VolumeAttached) (string, int64) {
	if va.SlotLocationPath == "" {
		return "slot desconhecido", 0
	}
	slot, found, err := m.db.Slots.FindByKey(ctx, va.SlotLocationPath, va.SlotLUN)
	if err != nil || !found {
		return fmt.Sprintf("%s (LUN %d)", va.SlotLocationPath, va.SlotLUN), 0
	}
	return slot.Alias, slot.ID
}

func (m *Manager) createAndAdmit(ctx context.Context, va bus.VolumeAttached, cardID, slotID int64, cardAlias, slotAlias string) {
	jobID, err := m.db.Jobs.Create(ctx, store.Job{
		CardID: cardID, SlotID: slotID,
		VolumeSerial: va.Serial, CardLabel: va.Label,
		SlotLocation: va.SlotLocationPath, SlotLUN: va.SlotLUN,
		Status: store.StatusPending,
	})
	if err != nil {
		m.log.Error("engine: creating job", "err", err)
		return
	}
	m.log.Debug("engine: job created",
		"job", jobID, "card", cardAlias, "slot", slotAlias)
	m.admit(ctx, queuedJob{jobID: jobID, vol: va, cardAlias: cardAlias, slotAlias: slotAlias})
}

func (m *Manager) askUnknown(ctx context.Context, va bus.VolumeAttached, slotID int64, slotAlias string) {
	jobID, err := m.db.Jobs.Create(ctx, store.Job{
		SlotID:       slotID,
		VolumeSerial: va.Serial, CardLabel: va.Label,
		SlotLocation: va.SlotLocationPath, SlotLUN: va.SlotLUN,
		Status: store.StatusAwaitingDecision,
	})
	if err != nil {
		m.log.Error("engine: creating awaiting job", "err", err)
		return
	}
	m.mu.Lock()
	m.awaiting[va.Serial] = queuedJob{jobID: jobID, vol: va, cardAlias: cardDisplayName(va), slotAlias: slotAlias}
	m.mu.Unlock()
	m.log.Info("engine: unknown card, asking", "serial", va.Serial, "slot", slotAlias)
	m.bus.Publish(bus.Event{Topic: bus.TopicCardUnknown, Payload: bus.CardUnknown{
		JobID: jobID, VolumeGUID: va.VolumeGUID, Serial: va.Serial,
		Label: va.Label, SlotAlias: slotAlias,
	}})
}

// admit runs q as soon as a concurrency slot frees up.
func (m *Manager) admit(ctx context.Context, q queuedJob) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case m.sem <- struct{}{}:
		}
		defer func() { <-m.sem }()
		m.log.Debug("engine: job admitted", "job", q.jobID, "card", q.cardAlias)

		jctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		m.mu.Lock()
		m.byGUID[q.vol.VolumeGUID] = &runningJob{jobID: q.jobID, cancel: cancel}
		m.mu.Unlock()

		runner := &jobRunner{
			m: m, jobID: q.jobID, vol: q.vol,
			cardAlias: q.cardAlias, slotAlias: q.slotAlias,
			set: jobSettings{
				template: m.db.Settings.GetString(ctx, store.SetDestTemplate, DefaultTemplate),
				paranoid: m.db.Settings.GetString(ctx, store.SetVerifyMode, "fast") == "paranoid",
				eject:    m.db.Settings.GetBool(ctx, store.SetEjectAfterCopy, true),
			},
		}
		err := runner.run(jctx)

		m.mu.Lock()
		delete(m.byGUID, q.vol.VolumeGUID)
		if err == errDestMissing {
			m.blocked[q.jobID] = q
		}
		m.mu.Unlock()
		if err == errDestMissing {
			m.log.Info("engine: job waiting for destination",
				"job", q.jobID, "card", q.cardAlias)
		}
	}()
}

// KickDestRetry asks the run loop to retry destination-blocked jobs now,
// instead of on the next 30s tick. Non-blocking; safe from any goroutine.
func (m *Manager) KickDestRetry() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// BlockedJobIDs returns the jobs currently parked waiting for the
// destination, so the status API can explain why they sit in the queue.
func (m *Manager) BlockedJobIDs() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.blocked))
	for id := range m.blocked {
		ids = append(ids, id)
	}
	return ids
}

func (m *Manager) retryBlocked(ctx context.Context) {
	m.mu.Lock()
	if len(m.blocked) == 0 {
		m.mu.Unlock()
		return
	}
	queued := make([]queuedJob, 0, len(m.blocked))
	for _, q := range m.blocked {
		queued = append(queued, q)
	}
	m.mu.Unlock()

	if _, err := m.resolveDest(ctx); err != nil {
		m.log.Debug("engine: destination still absent, jobs stay blocked",
			"blocked", len(queued), "err", err)
		return // still absent
	}
	m.log.Info("engine: destination appeared, resuming blocked jobs", "count", len(queued))
	for _, q := range queued {
		m.mu.Lock()
		delete(m.blocked, q.jobID)
		m.mu.Unlock()
		m.admit(ctx, q)
	}
}

func (m *Manager) handleDetach(ctx context.Context, guid string) {
	m.mu.Lock()
	run := m.byGUID[guid]
	var blockedJob, awaitingJob *queuedJob
	for id, q := range m.blocked {
		if q.vol.VolumeGUID == guid {
			qq := q
			blockedJob = &qq
			delete(m.blocked, id)
			break
		}
	}
	for serial, q := range m.awaiting {
		if q.vol.VolumeGUID == guid {
			qq := q
			awaitingJob = &qq
			delete(m.awaiting, serial)
			break
		}
	}
	m.mu.Unlock()

	if run != nil {
		m.log.Warn("engine: card removed mid-job", "volume", guid, "job", run.jobID)
		run.cancel(errDetached)
	}
	if blockedJob != nil {
		m.db.Jobs.Finish(ctx, blockedJob.jobID, store.StatusFailed,
			"cartão removido antes de a cópia iniciar")
	}
	if awaitingJob != nil {
		m.db.Jobs.Finish(ctx, awaitingJob.jobID, store.StatusCancelled,
			"cartão removido antes da decisão")
	}
}

func (m *Manager) handleDecision(ctx context.Context, cd bus.CardDecision) {
	m.mu.Lock()
	q, ok := m.awaiting[cd.Serial]
	if ok {
		delete(m.awaiting, cd.Serial)
	}
	m.mu.Unlock()
	if !ok {
		m.log.Warn("engine: decision for unknown/expired question", "serial", cd.Serial)
		return
	}

	switch cd.Action {
	case "copy":
		// Register the card (default alias; the UI offers renaming) so the
		// next insertion is a known card.
		card, err := m.db.Cards.Create(ctx, cd.Serial, q.vol.Label, "", "copy")
		if err != nil {
			m.log.Error("engine: registering card on copy decision", "err", err)
		} else {
			m.db.Jobs.SetCard(ctx, q.jobID, card.ID)
			q.cardAlias = card.Alias
		}
		m.db.Jobs.SetStatus(ctx, q.jobID, store.StatusPending)
		m.admit(ctx, q)
	case "always_ignore":
		if _, err := m.db.Cards.Create(ctx, cd.Serial, q.vol.Label, "", "ignore"); err != nil {
			m.log.Error("engine: registering ignored card", "err", err)
		}
		m.db.Jobs.Finish(ctx, q.jobID, store.StatusCancelled, "ignorado pelo usuário (sempre)")
	default: // "ignore"
		m.db.Jobs.Finish(ctx, q.jobID, store.StatusCancelled, "ignorado pelo usuário")
	}
}

// Cancel aborts a job from the API: running jobs are cancelled with cause,
// queued ones are finished directly.
func (m *Manager) Cancel(ctx context.Context, jobID int64) error {
	m.mu.Lock()
	for _, run := range m.byGUID {
		if run.jobID == jobID {
			m.mu.Unlock()
			run.cancel(errUserCancelled)
			return nil
		}
	}
	if q, ok := m.blocked[jobID]; ok {
		delete(m.blocked, jobID)
		m.mu.Unlock()
		return m.db.Jobs.Finish(ctx, q.jobID, store.StatusCancelled, "cancelado pelo usuário")
	}
	for serial, q := range m.awaiting {
		if q.jobID == jobID {
			delete(m.awaiting, serial)
			m.mu.Unlock()
			return m.db.Jobs.Finish(ctx, q.jobID, store.StatusCancelled, "cancelado pelo usuário")
		}
	}
	m.mu.Unlock()
	return fmt.Errorf("job %d não está ativo", jobID)
}

// DestMounted reports whether the configured destination is currently
// resolvable (for the status API).
func (m *Manager) DestMounted(ctx context.Context) bool {
	_, err := m.resolveDest(ctx)
	return err == nil
}

// resolveDest maps the configured destination volume GUID to a mount path.
func (m *Manager) resolveDest(ctx context.Context) (string, error) {
	guid := m.db.Settings.GetString(ctx, store.SetDestVolumeGUID, "")
	if guid == "" {
		m.log.Debug("engine: no destination configured (dest_volume_guid empty)")
		return "", errDestMissing
	}
	root, err := m.p.Dest.ResolveDest(ctx, guid)
	m.log.Debug("engine: resolving destination", "guid", guid, "root", root, "err", err)
	return root, err
}
