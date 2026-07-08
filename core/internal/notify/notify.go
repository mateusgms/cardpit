// Package notify turns bus events into user notifications. Everything is
// behind the Notifier interface (RF-04.8); Telegram is the only v1
// implementation. The copy path never blocks on this package: events arrive
// through a buffered bus subscription and sends go through a retrying queue.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/report"
	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// Notifier delivers user-facing notifications. msgRef identifies the
// job-start message so progress/completion can edit it; 0 means "no start
// message exists, send fresh".
type Notifier interface {
	JobStarted(ctx context.Context, in StartInfo) (msgRef int64, err error)
	JobProgress(ctx context.Context, msgRef int64, in ProgressInfo) error
	JobCompleted(ctx context.Context, msgRef int64, in CompletedInfo, reportPNG []byte) error
	JobFailed(ctx context.Context, msgRef int64, in FailInfo) error
	AskUnknownCard(ctx context.Context, in bus.CardUnknown) error
	DestMissing(ctx context.Context, in bus.DestMissing) error
	Test(ctx context.Context) error
}

type StartInfo struct {
	Ev bus.JobEvent
	At time.Time
}

type ProgressInfo struct {
	Ev bus.JobEvent
}

type CompletedInfo struct {
	Ev         bus.JobEvent
	StatsLine  string // "247 fotos (18,2 GiB) · 12 vídeos (41,7 GiB)"
	Duration   string
	Throughput string
}

type FailInfo struct {
	Ev bus.JobEvent
}

// Dispatcher subscribes to the bus, applies the progress-edit throttle
// (every ≥10% or ≥30s) and pushes work through the retry queue. The actual
// Notifier is hot-swappable: a supervisor rebuilds it when the Telegram
// settings change, without restarting the service.
type Dispatcher struct {
	db      *store.DB
	bus     *bus.Bus
	secrets secret.SecretBox
	log     *slog.Logger
	q       *queue

	// buildNotifier creates a Notifier for a token+chats pair; swapped in
	// tests. cleanup stops background work (bot long polling).
	buildNotifier func(ctx context.Context, token string, chats []int64) (n Notifier, cleanup func(), err error)

	mu       sync.Mutex
	notifier Notifier
	jobs     map[int64]*jobMsgState

	cfgMu      sync.Mutex
	curToken   string
	curChats   string
	curCleanup func()

	// throttle knobs (test-tunable)
	editMinPct      int
	editMinInterval time.Duration
}

type jobMsgState struct {
	msgRef     int64
	lastEditAt time.Time
	lastPct    int
}

func NewDispatcher(db *store.DB, b *bus.Bus, secrets secret.SecretBox, log *slog.Logger) *Dispatcher {
	d := &Dispatcher{
		db: db, bus: b, secrets: secrets, log: log,
		q:               newQueue(512, log),
		jobs:            map[int64]*jobMsgState{},
		editMinPct:      10,
		editMinInterval: 30 * time.Second,
	}
	d.buildNotifier = d.buildTelegram
	return d
}

// Test satisfies the httpapi hook (POST /api/telegram/test).
func (d *Dispatcher) Test(ctx context.Context) error {
	n := d.current()
	if n == nil {
		return fmt.Errorf("Telegram não configurado: informe token e chat_id nas Configurações")
	}
	return n.Test(ctx)
}

func (d *Dispatcher) current() Notifier {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.notifier
}

// Run processes events until ctx ends. The supervisor tick re-reads the
// Telegram settings so saving them in the UI takes effect without restart.
func (d *Dispatcher) Run(ctx context.Context) error {
	go d.q.run(ctx)

	sub := d.bus.Subscribe(256,
		bus.TopicJobStarted, bus.TopicJobProgress, bus.TopicJobCompleted,
		bus.TopicJobFailed, bus.TopicCardUnknown, bus.TopicDestMissing)
	defer sub.Close()

	d.superviseTelegram(ctx) // initial build
	supervise := time.NewTicker(15 * time.Second)
	defer supervise.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-supervise.C:
			d.superviseTelegram(ctx)
		case e := <-sub.C:
			d.handle(ctx, e)
		}
	}
}

func (d *Dispatcher) handle(ctx context.Context, e bus.Event) {
	n := d.current()
	if n == nil {
		return // no notifier configured; events flow to SSE regardless
	}
	switch e.Topic {
	case bus.TopicJobStarted:
		ev := e.Payload.(bus.JobEvent)
		d.mu.Lock()
		d.jobs[ev.JobID] = &jobMsgState{}
		d.mu.Unlock()
		at := e.At
		d.q.enqueue(task{desc: fmt.Sprintf("start job %d", ev.JobID), retries: 8, backoff: 5 * time.Second,
			run: func(tctx context.Context) error {
				ref, err := n.JobStarted(tctx, StartInfo{Ev: ev, At: at})
				if err != nil {
					return err
				}
				d.mu.Lock()
				if st, ok := d.jobs[ev.JobID]; ok {
					st.msgRef = ref
					st.lastEditAt = time.Now()
				}
				d.mu.Unlock()
				d.db.Jobs.SetTgMessageID(context.WithoutCancel(tctx), ev.JobID, ref)
				return nil
			}})

	case bus.TopicJobProgress:
		ev := e.Payload.(bus.JobEvent)
		ref, ok := d.shouldEdit(ev)
		if !ok {
			return
		}
		d.q.enqueue(task{desc: fmt.Sprintf("progress job %d", ev.JobID), retries: 0,
			run: func(tctx context.Context) error {
				return n.JobProgress(tctx, ref, ProgressInfo{Ev: ev})
			}})

	case bus.TopicJobCompleted:
		ev := e.Payload.(bus.JobEvent)
		ref := d.takeState(ev.JobID)
		d.q.enqueue(task{desc: fmt.Sprintf("completed job %d", ev.JobID), retries: 8, backoff: 5 * time.Second,
			run: func(tctx context.Context) error {
				info, png := d.completionPayload(tctx, ev)
				return n.JobCompleted(tctx, ref, info, png)
			}})

	case bus.TopicJobFailed:
		ev := e.Payload.(bus.JobEvent)
		ref := d.takeState(ev.JobID)
		d.q.enqueue(task{desc: fmt.Sprintf("failed job %d", ev.JobID), retries: 8, backoff: 5 * time.Second,
			run: func(tctx context.Context) error {
				return n.JobFailed(tctx, ref, FailInfo{Ev: ev})
			}})

	case bus.TopicCardUnknown:
		ev := e.Payload.(bus.CardUnknown)
		d.q.enqueue(task{desc: "ask unknown card", retries: 8, backoff: 5 * time.Second,
			run: func(tctx context.Context) error {
				return n.AskUnknownCard(tctx, ev)
			}})

	case bus.TopicDestMissing:
		ev := e.Payload.(bus.DestMissing)
		d.q.enqueue(task{desc: "dest missing alert", retries: 4, backoff: 5 * time.Second,
			run: func(tctx context.Context) error {
				return n.DestMissing(tctx, ev)
			}})
	}
}

// shouldEdit applies the RF-04.2 throttle and returns the message ref.
func (d *Dispatcher) shouldEdit(ev bus.JobEvent) (int64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.jobs[ev.JobID]
	if !ok || st.msgRef == 0 {
		return 0, false // start message not delivered yet
	}
	pct := 0
	if ev.BytesTotal > 0 {
		pct = int(ev.BytesCopied * 100 / ev.BytesTotal)
	}
	if pct-st.lastPct < d.editMinPct && time.Since(st.lastEditAt) < d.editMinInterval {
		return 0, false
	}
	st.lastPct = pct
	st.lastEditAt = time.Now()
	return st.msgRef, true
}

func (d *Dispatcher) takeState(jobID int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.jobs[jobID]
	if !ok {
		return 0
	}
	delete(d.jobs, jobID)
	return st.msgRef
}

// completionPayload gathers stats and renders the PNG. Failures fall back to
// a text-only completion (the message must go out either way).
func (d *Dispatcher) completionPayload(ctx context.Context, ev bus.JobEvent) (CompletedInfo, []byte) {
	info := CompletedInfo{Ev: ev, StatsLine: "", Duration: "—", Throughput: "—"}
	job, err := d.db.Jobs.Get(ctx, ev.JobID)
	if err != nil {
		d.log.Error("notify: loading job for completion", "job", ev.JobID, "err", err)
		return info, nil
	}
	rawStats, err := d.db.Files.StatsByJob(ctx, ev.JobID)
	if err != nil {
		d.log.Error("notify: loading stats", "job", ev.JobID, "err", err)
		return info, nil
	}
	stats := map[string]report.TypeStat{}
	for k, v := range rawStats {
		stats[k] = report.TypeStat{Count: v.Count, Bytes: v.Bytes}
	}
	info.StatsLine = statsLine(stats)
	info.Duration, info.Throughput = durThroughput(job)

	largest, err := d.db.Files.LargestByJob(ctx, ev.JobID, 10)
	if err != nil {
		d.log.Error("notify: loading largest files", "job", ev.JobID, "err", err)
	}
	png, err := report.Render(report.Input{
		Job: job, CardAlias: ev.CardAlias, SlotAlias: ev.SlotAlias,
		Stats: stats, Largest: largest,
	})
	if err != nil {
		d.log.Error("notify: rendering report png", "job", ev.JobID, "err", err)
		png = nil
	}
	return info, png
}

// superviseTelegram (re)builds the notifier when token/chats change.
func (d *Dispatcher) superviseTelegram(ctx context.Context) {
	token := ""
	if sealed, ok, _ := d.db.Settings.Get(ctx, store.SetTelegramToken); ok {
		if plain, err := d.secrets.Open(sealed); err == nil {
			token = string(plain)
		} else {
			d.log.Error("notify: unsealing telegram token", "err", err)
		}
	}
	chatsRaw := d.db.Settings.GetString(ctx, store.SetTelegramChatIDs, "")
	d.applyConfig(ctx, token, chatsRaw)
}

func (d *Dispatcher) applyConfig(ctx context.Context, token, chatsRaw string) {
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	if token == d.curToken && chatsRaw == d.curChats {
		return
	}
	if d.curCleanup != nil {
		d.curCleanup()
		d.curCleanup = nil
	}
	d.curToken, d.curChats = token, chatsRaw

	if token == "" {
		d.setNotifier(nil)
		d.log.Info("notify: telegram disabled (no token)")
		return
	}
	chats := parseChatIDs(chatsRaw)
	if len(chats) == 0 {
		d.setNotifier(nil)
		d.log.Warn("notify: telegram token set but no chat_ids allowlisted; notifier disabled")
		return
	}
	n, cleanup, err := d.buildNotifier(ctx, token, chats)
	if err != nil {
		d.log.Error("notify: building telegram notifier", "err", err)
		d.setNotifier(nil)
		// Force a retry on the next supervise tick.
		d.curToken, d.curChats = "", ""
		return
	}
	d.curCleanup = cleanup
	d.setNotifier(n)
	d.log.Info("notify: telegram notifier active", "chats", len(chats))
}

func (d *Dispatcher) setNotifier(n Notifier) {
	d.mu.Lock()
	d.notifier = n
	d.mu.Unlock()
}

func parseChatIDs(raw string) []int64 {
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err == nil {
			out = append(out, id)
		}
	}
	return out
}
