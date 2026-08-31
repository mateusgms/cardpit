package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/store"
)

var (
	errDetached      = errors.New("cartão removido durante a cópia")
	errUserCancelled = errors.New("cancelado pelo usuário")
	errDestMissing   = errors.New("SSD de destino ausente")
)

// spaceMargin is kept free on the destination beyond the payload size.
const spaceMargin = 256 << 20

type jobSettings struct {
	template string
	paranoid bool
	eject    bool
}

// jobRunner executes one ingest job end to end and owns its status
// transitions and events. It returns errDestMissing when the job should stay
// pending until the destination shows up; every other outcome is terminal.
type jobRunner struct {
	m         *Manager
	jobID     int64
	vol       bus.VolumeAttached
	cardAlias string
	slotAlias string
	set       jobSettings

	filesCopied, filesFailed, filesSkipped, filesTotal int
	bytesCopied, bytesTotal                            int64
	inflightBytes                                      int64
	eta                                                etaEstimator

	lastFlush  time.Time
	sinceFlush int
}

func (r *jobRunner) run(ctx context.Context) error {
	dctx := context.Background() // db/bus writes must survive job cancellation

	// 1. Scan the card.
	entries, err := scanSource(r.vol.Root)
	if err != nil {
		return r.fail(dctx, ctx, fmt.Errorf("lendo o cartão: %w", err))
	}

	// 2. Dedup plan: decide what actually needs copying.
	toCopy, skipped, err := r.planDedup(ctx, entries)
	if err != nil {
		return r.fail(dctx, ctx, fmt.Errorf("planejando dedup: %w", err))
	}
	r.filesSkipped = skipped
	r.filesTotal = len(toCopy)
	for _, e := range toCopy {
		r.bytesTotal += e.size
	}
	if err := r.m.db.Jobs.SetTotals(dctx, r.jobID, r.filesTotal, r.bytesTotal, r.filesSkipped); err != nil {
		r.m.log.Error("engine: persisting totals", "job", r.jobID, "err", err)
	}
	r.m.log.Debug("engine: scan and dedup plan done", "job", r.jobID,
		"scanned", len(entries), "to_copy", r.filesTotal, "skipped_dedup", r.filesSkipped)

	// 3. Destination must be mounted (never copy to a fallback).
	destRoot, err := r.m.resolveDest(ctx)
	if err != nil {
		r.m.db.Jobs.SetStatus(dctx, r.jobID, store.StatusPending)
		r.m.bus.Publish(bus.Event{Topic: bus.TopicDestMissing, Payload: bus.DestMissing{
			VolumeGUID: r.vol.VolumeGUID, CardAlias: r.cardAlias, SlotAlias: r.slotAlias,
		}})
		return errDestMissing
	}

	// 4. Free space check.
	free, err := r.m.p.Space.FreeSpace(ctx, destRoot)
	if err != nil {
		return r.fail(dctx, ctx, fmt.Errorf("verificando espaço livre: %w", err))
	}
	if need := uint64(r.bytesTotal) + spaceMargin; free < need {
		return r.fail(dctx, ctx, fmt.Errorf(
			"espaço insuficiente no destino: faltam %s", formatBytes(int64(need-free))))
	}

	// 5. Copy.
	r.m.db.Jobs.SetStatus(dctx, r.jobID, store.StatusCopying)
	r.eta.start(time.Now(), 0)
	r.publish(bus.TopicJobStarted, store.StatusCopying, "")

	for _, entry := range toCopy {
		if ctx.Err() != nil {
			break
		}
		dstDir := filepath.Join(destRoot, expandTemplate(r.set.template, entry.mtime, r.cardAlias))
		dstPath, hash, err := r.m.copier.copyOne(ctx, entry, dstDir, r.set.paranoid, func(n int64) {
			r.inflightBytes = n
			r.flushProgress(dctx, false)
		})
		r.inflightBytes = 0
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			r.filesFailed++
			r.m.log.Error("engine: file failed after retries",
				"job", r.jobID, "src", entry.src, "err", err)
			r.flushProgress(dctx, false)
			continue
		}
		if err := r.m.db.Files.Insert(dctx, store.IngestedFile{
			JobID: r.jobID, SrcPath: entry.src, DstPath: dstPath,
			Size: entry.size, Mtime: mtimeKey(entry.mtime), XXHash: hash,
			MediaType: entry.media,
		}); err != nil {
			r.m.log.Error("engine: recording ingested file", "job", r.jobID, "err", err)
		}
		r.filesCopied++
		r.bytesCopied += entry.size
		r.flushProgress(dctx, false)
	}
	r.flushProgress(dctx, true)

	// 6. Terminal state.
	if ctx.Err() != nil {
		cause := context.Cause(ctx)
		status := store.StatusFailed
		if errors.Is(cause, errUserCancelled) {
			status = store.StatusCancelled
		}
		msg := cause.Error()
		r.m.db.Jobs.Finish(dctx, r.jobID, status, msg)
		r.publish(bus.TopicJobFailed, status, msg)
		return cause
	}

	if r.filesFailed > 0 {
		msg := fmt.Sprintf("%d arquivo(s) falharam após as tentativas de retry", r.filesFailed)
		r.m.db.Jobs.Finish(dctx, r.jobID, store.StatusDone, msg)
		r.publish(bus.TopicJobCompleted, store.StatusDone, msg)
		return nil
	}

	r.m.db.Jobs.Finish(dctx, r.jobID, store.StatusDone, "")

	// 7. Eject: the physical "you may remove it" signal.
	if r.set.eject {
		if err := r.m.p.Eject.Eject(dctx, platform.VolumeID{GUIDPath: r.vol.VolumeGUID}); err != nil {
			r.m.log.Warn("engine: eject failed", "job", r.jobID, "err", err)
		}
	}
	r.publish(bus.TopicJobCompleted, store.StatusDone, "")
	return nil
}

// planDedup partitions entries into files to copy and a skipped count.
// Cheap first stage: (size, mtime) lookup. On a hit the source is hashed
// (single extra read) and only a confirmed (size, mtime, hash) match skips.
func (r *jobRunner) planDedup(ctx context.Context, entries []fileEntry) ([]fileEntry, int, error) {
	var toCopy []fileEntry
	skipped := 0
	bufp := r.m.copier.bufPool.Get().(*[]byte)
	defer r.m.copier.bufPool.Put(bufp)
	for i := range entries {
		if err := ctx.Err(); err != nil {
			return nil, 0, context.Cause(ctx)
		}
		e := entries[i]
		key := mtimeKey(e.mtime)
		seen, err := r.m.db.Files.HasSizeMtime(ctx, e.size, key)
		if err != nil {
			return nil, 0, err
		}
		if seen {
			hash, err := hashFile(ctx, e.src, *bufp)
			if err != nil {
				return nil, 0, err
			}
			exists, err := r.m.db.Files.ExistsHash(ctx, e.size, key, hash)
			if err != nil {
				return nil, 0, err
			}
			if exists {
				skipped++
				continue
			}
			e.knownHash = hash
		}
		toCopy = append(toCopy, e)
	}
	return toCopy, skipped, nil
}

func (r *jobRunner) fail(dctx, ctx context.Context, err error) error {
	// A cancellation (detach/user) that surfaced as an I/O error is reported
	// as its cause, not as the wrapped error.
	if ctx.Err() != nil {
		err = context.Cause(ctx)
	}
	status := store.StatusFailed
	if errors.Is(err, errUserCancelled) {
		status = store.StatusCancelled
	}
	r.m.db.Jobs.Finish(dctx, r.jobID, status, err.Error())
	r.publish(bus.TopicJobFailed, status, err.Error())
	return err
}

// flushProgress persists and publishes progress every 2s or 50 files.
func (r *jobRunner) flushProgress(dctx context.Context, force bool) {
	r.sinceFlush++
	if !force && r.sinceFlush < 50 && time.Since(r.lastFlush) < 2*time.Second {
		return
	}
	r.sinceFlush = 0
	r.lastFlush = time.Now()
	if err := r.m.db.Jobs.UpdateProgress(dctx, r.jobID, r.filesCopied, r.bytesCopied, r.filesFailed); err != nil {
		r.m.log.Error("engine: persisting progress", "job", r.jobID, "err", err)
	}
	r.publish(bus.TopicJobProgress, store.StatusCopying, "")
}

func (r *jobRunner) publish(topic bus.Topic, status, errMsg string) {
	liveBytes := r.bytesCopied + r.inflightBytes
	bps, etaSeconds := int64(0), int64(0)
	if topic == bus.TopicJobProgress {
		bps, etaSeconds = r.eta.sample(time.Now(), liveBytes, r.bytesTotal)
	}
	r.m.bus.Publish(bus.Event{Topic: topic, Payload: bus.JobEvent{
		JobID:          r.jobID,
		VolumeGUID:     r.vol.VolumeGUID,
		CardAlias:      r.cardAlias,
		SlotAlias:      r.slotAlias,
		Status:         status,
		FilesTotal:     r.filesTotal,
		FilesCopied:    r.filesCopied,
		FilesSkipped:   r.filesSkipped,
		FilesFailed:    r.filesFailed,
		BytesTotal:     r.bytesTotal,
		BytesCopied:    liveBytes,
		BytesPerSecond: bps,
		ETASeconds:     etaSeconds,
		Error:          errMsg,
	}})
}

type etaEstimator struct {
	started, lastAt time.Time
	lastBytes       int64
	rate            float64
	samples         int
}

func (e *etaEstimator) start(now time.Time, bytes int64) {
	e.started, e.lastAt, e.lastBytes = now, now, bytes
	e.rate, e.samples = 0, 0
}

func (e *etaEstimator) sample(now time.Time, copied, total int64) (int64, int64) {
	if e.started.IsZero() {
		e.start(now, copied)
		return 0, 0
	}
	dt := now.Sub(e.lastAt).Seconds()
	delta := copied - e.lastBytes
	if dt > 0 && delta > 0 {
		instant := float64(delta) / dt
		if e.samples == 0 {
			e.rate = instant
		} else {
			e.rate = 0.3*instant + 0.7*e.rate
		}
		e.samples++
		e.lastAt, e.lastBytes = now, copied
	}
	if e.samples < 2 || now.Sub(e.started) < 5*time.Second || e.rate <= 0 || copied >= total {
		return 0, 0
	}
	remaining := float64(total-copied) / e.rate
	if remaining < 1 {
		remaining = 1
	}
	return int64(e.rate), int64(remaining + 0.5)
}
