package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeNotifier records calls.
type fakeNotifier struct {
	mu        sync.Mutex
	started   []StartInfo
	progress  []ProgressInfo
	completed []CompletedInfo
	pngs      [][]byte
	failed    []FailInfo
	asked     []bus.CardUnknown
}

func (f *fakeNotifier) JobStarted(_ context.Context, in StartInfo) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, in)
	return 1001, nil
}
func (f *fakeNotifier) JobProgress(_ context.Context, ref int64, in ProgressInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progress = append(f.progress, in)
	return nil
}
func (f *fakeNotifier) JobCompleted(_ context.Context, ref int64, in CompletedInfo, png []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, in)
	f.pngs = append(f.pngs, png)
	return nil
}
func (f *fakeNotifier) JobFailed(_ context.Context, ref int64, in FailInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, in)
	return nil
}
func (f *fakeNotifier) AskUnknownCard(_ context.Context, in bus.CardUnknown) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, in)
	return nil
}
func (f *fakeNotifier) DestMissing(context.Context, bus.DestMissing) error { return nil }
func (f *fakeNotifier) Test(context.Context) error                         { return nil }

func (f *fakeNotifier) counts() (int, int, int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started), len(f.progress), len(f.completed), len(f.failed), len(f.asked)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

func newTestDispatcher(t *testing.T) (*Dispatcher, *fakeNotifier, *store.DB, context.Context) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	d := NewDispatcher(db, bus.New(), secret.PlainBox{}, discard)
	fn := &fakeNotifier{}
	d.setNotifier(fn)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.q.run(ctx)
	return d, fn, db, ctx
}

func TestDispatcherLifecycleAndThrottle(t *testing.T) {
	d, fn, db, ctx := newTestDispatcher(t)
	jobID, _ := db.Jobs.Create(context.Background(), store.Job{Status: store.StatusCopying, VolumeSerial: "S"})
	db.Files.Insert(context.Background(), store.IngestedFile{
		JobID: jobID, SrcPath: "a", DstPath: "b", Size: 100,
		Mtime: "2026-07-08T00:00:00Z", XXHash: "x", MediaType: "photo",
	})
	db.Jobs.Finish(context.Background(), jobID, store.StatusDone, "")

	ev := bus.JobEvent{JobID: jobID, CardAlias: "A", SlotAlias: "Leitor", BytesTotal: 1000}

	d.handle(ctx, bus.Event{Topic: bus.TopicJobStarted, At: time.Now(), Payload: ev})
	waitFor(t, func() bool { s, _, _, _, _ := fn.counts(); return s == 1 })

	// Persisted message ref.
	waitFor(t, func() bool {
		j, _ := db.Jobs.Get(context.Background(), jobID)
		return j.TgMessageID == 1001
	})

	// 5% progress: below both thresholds → suppressed.
	ev5 := ev
	ev5.BytesCopied = 50
	d.handle(ctx, bus.Event{Topic: bus.TopicJobProgress, Payload: ev5})
	// 15% progress: ≥10% delta → edit goes out.
	ev15 := ev
	ev15.BytesCopied = 150
	d.handle(ctx, bus.Event{Topic: bus.TopicJobProgress, Payload: ev15})
	waitFor(t, func() bool { _, p, _, _, _ := fn.counts(); return p == 1 })

	// 18%: below delta again → suppressed.
	ev18 := ev
	ev18.BytesCopied = 180
	d.handle(ctx, bus.Event{Topic: bus.TopicJobProgress, Payload: ev18})
	time.Sleep(50 * time.Millisecond)
	if _, p, _, _, _ := fn.counts(); p != 1 {
		t.Fatalf("throttle failed: %d progress edits", p)
	}

	// Completion carries stats + png and clears state.
	evDone := ev
	evDone.BytesCopied = 1000
	d.handle(ctx, bus.Event{Topic: bus.TopicJobCompleted, Payload: evDone})
	waitFor(t, func() bool { _, _, c, _, _ := fn.counts(); return c == 1 })
	fn.mu.Lock()
	if !strings.Contains(fn.completed[0].StatsLine, "1 fotos") {
		t.Fatalf("stats line: %q", fn.completed[0].StatsLine)
	}
	if fn.pngs[0] == nil {
		t.Fatal("png missing")
	}
	fn.mu.Unlock()

	d.mu.Lock()
	_, still := d.jobs[jobID]
	d.mu.Unlock()
	if still {
		t.Fatal("job state not cleared after completion")
	}
}

func TestProgressBeforeStartIsDropped(t *testing.T) {
	d, fn, _, ctx := newTestDispatcher(t)
	d.handle(ctx, bus.Event{Topic: bus.TopicJobProgress, Payload: bus.JobEvent{JobID: 9, BytesTotal: 10, BytesCopied: 5}})
	time.Sleep(50 * time.Millisecond)
	if _, p, _, _, _ := fn.counts(); p != 0 {
		t.Fatalf("progress without start: %d", p)
	}
}

func TestTimeBasedEdit(t *testing.T) {
	d, fn, db, ctx := newTestDispatcher(t)
	d.editMinInterval = 30 * time.Millisecond
	jobID, _ := db.Jobs.Create(context.Background(), store.Job{Status: store.StatusCopying})
	ev := bus.JobEvent{JobID: jobID, BytesTotal: 10000}
	d.handle(ctx, bus.Event{Topic: bus.TopicJobStarted, At: time.Now(), Payload: ev})
	waitFor(t, func() bool { s, _, _, _, _ := fn.counts(); return s == 1 })

	ev2 := ev
	ev2.BytesCopied = 100 // only 1%
	time.Sleep(50 * time.Millisecond)
	d.handle(ctx, bus.Event{Topic: bus.TopicJobProgress, Payload: ev2})
	waitFor(t, func() bool { _, p, _, _, _ := fn.counts(); return p == 1 })
}

func TestQueueRetriesThenSucceeds(t *testing.T) {
	q := newQueue(16, discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.run(ctx)

	var mu sync.Mutex
	attempts := 0
	q.enqueue(task{desc: "flaky", retries: 5, backoff: 5 * time.Millisecond,
		run: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts < 3 {
				return errors.New("telegram down")
			}
			return nil
		}})
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts == 3
	})
}

func TestQueueDropsWithoutRetries(t *testing.T) {
	q := newQueue(16, discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.run(ctx)

	var mu sync.Mutex
	attempts := 0
	q.enqueue(task{desc: "droppable", retries: 0,
		run: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			return errors.New("nope")
		}})
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("droppable retried: %d", attempts)
	}
}

func TestParseCallback(t *testing.T) {
	cases := []struct {
		in         string
		serial, ac string
		ok         bool
	}{
		{"cardpit:card:AB12:copy", "AB12", "copy", true},
		{"cardpit:card:AB12:always_ignore", "AB12", "always_ignore", true},
		{"cardpit:card:AB12:rm -rf", "", "", false},
		{"other:stuff", "", "", false},
		{"cardpit:card::copy", "", "", false},
	}
	for _, c := range cases {
		s, a, ok := parseCallback(c.in)
		if s != c.serial || a != c.ac || ok != c.ok {
			t.Fatalf("%q → %q %q %v", c.in, s, a, ok)
		}
	}
}

// fakeTG asserts tgNotifier behavior (edit fallback, caption without png).
type fakeTG struct {
	mu        sync.Mutex
	editFails bool
	sent      []string
	edited    []string
	photos    int
}

func (f *fakeTG) SendMessage(_ context.Context, chatID int64, text string, kb *models.InlineKeyboardMarkup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return int64(len(f.sent)), nil
}
func (f *fakeTG) EditMessageText(_ context.Context, chatID, msgID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editFails {
		return errors.New("message to edit not found")
	}
	f.edited = append(f.edited, text)
	return nil
}
func (f *fakeTG) SendPhoto(_ context.Context, chatID int64, png []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.photos++
	return nil
}

func TestTGNotifierCompletionPaths(t *testing.T) {
	ctx := context.Background()
	in := CompletedInfo{Ev: bus.JobEvent{SlotAlias: "Leitor esquerdo", CardAlias: "A"}}

	// Happy: edit start message + photo with "pode remover" caption.
	tg := &fakeTG{}
	n := &tgNotifier{client: tg, chats: []int64{1}}
	if err := n.JobCompleted(ctx, 42, in, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if len(tg.edited) != 1 || tg.photos != 1 {
		t.Fatalf("edited=%d photos=%d", len(tg.edited), tg.photos)
	}

	// Edit fails → falls back to a fresh message; no png → caption as text.
	tg2 := &fakeTG{editFails: true}
	n2 := &tgNotifier{client: tg2, chats: []int64{1}}
	if err := n2.JobCompleted(ctx, 42, in, nil); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(tg2.sent, "\n---\n")
	if !strings.Contains(joined, "cópia concluída") || !strings.Contains(joined, "Pode remover o cartão do Leitor esquerdo") {
		t.Fatalf("fallback messages: %q", joined)
	}
}

func TestUnknownCardKeyboard(t *testing.T) {
	tg := &fakeTG{}
	n := &tgNotifier{client: tg, chats: []int64{1, 2}}
	if err := n.AskUnknownCard(context.Background(), bus.CardUnknown{
		Serial: "AA", Label: "SD", SlotAlias: "Leitor",
	}); err != nil {
		t.Fatal(err)
	}
	if len(tg.sent) != 2 {
		t.Fatalf("asked in %d chats", len(tg.sent))
	}
}
