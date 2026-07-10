package engine

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/platform/fake"
	"github.com/mateusgms/cardpit/core/internal/store"
)

func platformVolume(guid string) platform.VolumeID {
	return platform.VolumeID{GUIDPath: guid}
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	m    *Manager
	b    *bus.Bus
	db   *store.DB
	root string
	dest string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	dest := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := db.Settings.Set(ctx, store.SetDestVolumeGUID, "fake-dest"); err != nil {
		t.Fatal(err)
	}
	b := bus.New()
	m := NewManager(db, fake.New(root, dest), b, discard)
	m.sem = make(chan struct{}, 4)
	m.copier = newCopier()
	m.copier.retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	return &env{m: m, b: b, db: db, root: root, dest: dest}
}

// insertCard creates a fake card; files maps relative path → (content, mtime).
type testFile struct {
	body  string
	mtime time.Time
}

func (e *env) insertCard(t *testing.T, slot, card string, files map[string]testFile) {
	t.Helper()
	dir := filepath.Join(e.root, slot, card)
	for rel, f := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !f.mtime.IsZero() {
			if err := os.Chtimes(p, f.mtime, f.mtime); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// attach builds the bus payload the watcher would emit for a fake card.
func (e *env) attach(t *testing.T, slot, card string) bus.VolumeAttached {
	t.Helper()
	guid := "fake://" + slot + "/" + card
	info, err := e.m.p.Info.VolumeInfo(context.Background(),
		platformVolume(guid))
	if err != nil {
		t.Fatal(err)
	}
	return bus.VolumeAttached{
		VolumeGUID: guid, Root: info.Root, Serial: info.Serial, Label: info.Label,
		SlotLocationPath: "FAKE#" + slot, SlotLUN: 0,
	}
}

func waitTopic(t *testing.T, sub *bus.Subscription, topic bus.Topic) bus.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-sub.C:
			if e.Topic == topic {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", topic)
		}
	}
}

func listDest(t *testing.T, dest string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dest, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dest, path)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}

func TestHappyPathCopiesOrganizedByDate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	d1 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.Local)
	d2 := time.Date(2026, 7, 2, 11, 0, 0, 0, time.Local)
	e.insertCard(t, "slot1", "CARD01", map[string]testFile{
		"DCIM/100/IMG_0001.JPG": {"photo-one", d1},
		"DCIM/100/IMG_0002.JPG": {"photo-two", d1},
		"DCIM/100/MOV_0001.MP4": {"video-one", d2},
	})
	// Register the card so it is "known".
	if _, err := e.db.Cards.Create(ctx, e.attach(t, "slot1", "CARD01").Serial, "CARD01", "Cartão A", "copy"); err != nil {
		t.Fatal(err)
	}

	e.m.handleAttach(ctx, e.attach(t, "slot1", "CARD01"))
	ev := waitTopic(t, sub, bus.TopicJobCompleted)
	je := ev.Payload.(bus.JobEvent)
	if je.FilesCopied != 3 || je.FilesFailed != 0 || je.CardAlias != "Cartão A" {
		t.Fatalf("job event: %+v", je)
	}

	files := listDest(t, e.dest)
	want := map[string]bool{
		"2026-07-01/IMG_0001.JPG": true,
		"2026-07-01/IMG_0002.JPG": true,
		"2026-07-02/MOV_0001.MP4": true,
	}
	if len(files) != 3 {
		t.Fatalf("dest files: %v", files)
	}
	for _, f := range files {
		if !want[f] {
			t.Fatalf("unexpected dest layout: %v", files)
		}
	}

	j, err := e.db.Jobs.Get(ctx, je.JobID)
	if err != nil || j.Status != store.StatusDone || j.FilesCopied != 3 {
		t.Fatalf("job row: %+v err=%v", j, err)
	}
	dbFiles, total, _ := e.db.Files.ListByJob(ctx, je.JobID, 100, 0)
	if total != 3 {
		t.Fatalf("ingested rows: %d", total)
	}
	for _, f := range dbFiles {
		if f.MediaType == "" || len(f.XXHash) != 16 {
			t.Fatalf("bad row: %+v", f)
		}
	}

	// Eject (default on) renames the card dir: volume no longer listed.
	vols, _ := e.m.p.Volumes.ListRemovableVolumes(ctx)
	if len(vols) != 0 {
		t.Fatalf("card still present after eject: %v", vols)
	}
}

func TestDedupSkipsEverythingOnReinsert(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	mt := time.Date(2026, 7, 3, 9, 0, 0, 0, time.Local)
	files := map[string]testFile{
		"DCIM/IMG_0001.JPG": {"aaa", mt},
		"DCIM/IMG_0002.JPG": {"bbb", mt},
	}
	e.insertCard(t, "slot1", "CARD01", files)
	att := e.attach(t, "slot1", "CARD01")
	e.db.Cards.Create(ctx, att.Serial, "CARD01", "A", "copy")

	e.m.handleAttach(ctx, att)
	waitTopic(t, sub, bus.TopicJobCompleted)

	// "Reinsert": undo the eject rename.
	if err := os.Rename(
		filepath.Join(e.root, "slot1", ".ejected-CARD01"),
		filepath.Join(e.root, "slot1", "CARD01")); err != nil {
		t.Fatal(err)
	}
	e.m.handleAttach(ctx, att)
	ev := waitTopic(t, sub, bus.TopicJobCompleted)
	je := ev.Payload.(bus.JobEvent)
	if je.FilesCopied != 0 || je.FilesSkipped != 2 {
		t.Fatalf("reinsert: %+v", je)
	}
	if files := listDest(t, e.dest); len(files) != 2 {
		t.Fatalf("re-copied files: %v", files)
	}
}

func TestSizeMtimeCollisionIsResolvedByHash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	mt := time.Date(2026, 7, 4, 9, 0, 0, 0, time.Local)
	e.insertCard(t, "slot1", "CARD01", map[string]testFile{
		"IMG_0001.JPG": {"xxxx", mt},
	})
	att1 := e.attach(t, "slot1", "CARD01")
	e.db.Cards.Create(ctx, att1.Serial, "CARD01", "A", "copy")
	e.m.handleAttach(ctx, att1)
	waitTopic(t, sub, bus.TopicJobCompleted)

	// Second card: one file with same (size, mtime) but different content
	// (must copy), one with same (size, mtime, content) under another name
	// (must skip).
	e.insertCard(t, "slot2", "CARD02", map[string]testFile{
		"IMG_0009.JPG": {"yyyy", mt}, // same size+mtime, different bytes
		"RENAMED.JPG":  {"xxxx", mt}, // identical content → dedup
	})
	att2 := e.attach(t, "slot2", "CARD02")
	e.db.Cards.Create(ctx, att2.Serial, "CARD02", "B", "copy")
	e.m.handleAttach(ctx, att2)
	ev := waitTopic(t, sub, bus.TopicJobCompleted)
	je := ev.Payload.(bus.JobEvent)
	if je.FilesCopied != 1 || je.FilesSkipped != 1 {
		t.Fatalf("collision handling: %+v", je)
	}
}

func TestUnknownCardAskFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	e.insertCard(t, "slot1", "NEWCARD", map[string]testFile{"IMG.JPG": {"data", time.Now()}})
	att := e.attach(t, "slot1", "NEWCARD")
	e.m.handleAttach(ctx, att)

	ev := waitTopic(t, sub, bus.TopicCardUnknown)
	cu := ev.Payload.(bus.CardUnknown)
	if cu.Serial != att.Serial {
		t.Fatalf("unknown payload: %+v", cu)
	}
	if j, _ := e.db.Jobs.Get(ctx, cu.JobID); j.Status != store.StatusAwaitingDecision {
		t.Fatalf("job status: %+v", j)
	}

	e.m.handleDecision(ctx, bus.CardDecision{Serial: att.Serial, Action: "copy"})
	waitTopic(t, sub, bus.TopicJobCompleted)

	card, err := e.db.Cards.FindBySerial(ctx, att.Serial)
	if err != nil || card.Policy != "copy" {
		t.Fatalf("card after decision: %+v err=%v", card, err)
	}
}

func TestUnknownCardAlwaysIgnore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	e.insertCard(t, "slot1", "NEWCARD", map[string]testFile{"IMG.JPG": {"data", time.Now()}})
	att := e.attach(t, "slot1", "NEWCARD")
	e.m.handleAttach(ctx, att)
	ev := waitTopic(t, sub, bus.TopicCardUnknown)
	jobID := ev.Payload.(bus.CardUnknown).JobID

	e.m.handleDecision(ctx, bus.CardDecision{Serial: att.Serial, Action: "always_ignore"})
	if j, _ := e.db.Jobs.Get(ctx, jobID); j.Status != store.StatusCancelled {
		t.Fatalf("job: %+v", j)
	}
	card, _ := e.db.Cards.FindBySerial(ctx, att.Serial)
	if card.Policy != "ignore" {
		t.Fatalf("card: %+v", card)
	}

	// Same card attaching again is now a known-ignored card: no new jobs.
	e.m.handleAttach(ctx, att)
	time.Sleep(50 * time.Millisecond)
	_, total, _ := e.db.Jobs.ListPage(ctx, 10, 0)
	if total != 1 {
		t.Fatalf("jobs after ignored re-attach: %d", total)
	}
}

func TestFreeSpaceRefusal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	// Destination claims only 1000 bytes free: any payload + margin fails.
	os.WriteFile(filepath.Join(e.dest, ".cardpit-freespace"), []byte("1000"), 0o644)
	e.insertCard(t, "slot1", "CARD01", map[string]testFile{"IMG.JPG": {"data", time.Now()}})
	att := e.attach(t, "slot1", "CARD01")
	e.db.Cards.Create(ctx, att.Serial, "CARD01", "A", "copy")
	e.m.handleAttach(ctx, att)

	ev := waitTopic(t, sub, bus.TopicJobFailed)
	je := ev.Payload.(bus.JobEvent)
	if !strings.Contains(je.Error, "espaço insuficiente") {
		t.Fatalf("error: %q", je.Error)
	}
	if files := listDest(t, e.dest); len(files) != 1 { // only the marker file
		t.Fatalf("files copied despite refusal: %v", files)
	}
}

func TestDestMissingThenAppears(t *testing.T) {
	root := t.TempDir()
	destParent := t.TempDir()
	dest := filepath.Join(destParent, "ssd") // does not exist yet
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	db.Settings.Set(ctx, store.SetDestVolumeGUID, "fake-dest")
	b := bus.New()
	m := NewManager(db, fake.New(root, dest), b, discard)
	m.sem = make(chan struct{}, 4)
	m.copier = newCopier()
	e := &env{m: m, b: b, db: db, root: root, dest: dest}

	sub := b.Subscribe(64)
	defer sub.Close()

	e.insertCard(t, "slot1", "CARD01", map[string]testFile{"IMG.JPG": {"data", time.Now()}})
	att := e.attach(t, "slot1", "CARD01")
	db.Cards.Create(ctx, att.Serial, "CARD01", "A", "copy")
	m.handleAttach(ctx, att)

	ev := waitTopic(t, sub, bus.TopicDestMissing)
	if dm := ev.Payload.(bus.DestMissing); dm.CardAlias != "A" {
		t.Fatalf("dest missing payload: %+v", dm)
	}
	// Wait for the runner goroutine to park the job in blocked.
	waitFor(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.blocked) == 1
	})
	if j, _ := db.Jobs.Get(ctx, blockedJobID(m)); j.Status != store.StatusPending {
		t.Fatalf("job while blocked: %+v", j)
	}

	// SSD appears; the retry tick resumes the job.
	os.MkdirAll(dest, 0o755)
	m.retryBlocked(ctx)
	ev = waitTopic(t, sub, bus.TopicJobCompleted)
	if je := ev.Payload.(bus.JobEvent); je.FilesCopied != 1 {
		t.Fatalf("after dest appears: %+v", je)
	}
}

func TestKickDestRetryResumesBlockedJob(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(t.TempDir(), "ssd") // does not exist yet
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db.Settings.Set(ctx, store.SetDestVolumeGUID, "fake-dest")
	b := bus.New()
	m := NewManager(db, fake.New(root, dest), b, discard)
	e := &env{m: m, b: b, db: db, root: root, dest: dest}

	sub := b.Subscribe(64)
	defer sub.Close()
	go m.Run(ctx)

	e.insertCard(t, "slot1", "CARD01", map[string]testFile{"IMG.JPG": {"data", time.Now()}})
	att := e.attach(t, "slot1", "CARD01")
	db.Cards.Create(ctx, att.Serial, "CARD01", "A", "copy")
	// Run subscribes on start; republish until the attach is picked up
	// (duplicates are deduped by isTracked).
	waitFor(t, func() bool {
		b.Publish(bus.Event{Topic: bus.TopicVolumeAttached, Payload: att})
		_, total, _ := db.Jobs.ListPage(ctx, 10, 0)
		return total >= 1
	})
	waitTopic(t, sub, bus.TopicDestMissing)
	waitFor(t, func() bool { return len(m.BlockedJobIDs()) == 1 })

	// SSD appears and the settings handler kicks: the job must resume well
	// before the 30s ticker (waitTopic times out at 10s).
	os.MkdirAll(dest, 0o755)
	m.KickDestRetry()
	ev := waitTopic(t, sub, bus.TopicJobCompleted)
	if je := ev.Payload.(bus.JobEvent); je.FilesCopied != 1 {
		t.Fatalf("after kick: %+v", je)
	}
}

func blockedJobID(m *Manager) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.blocked {
		return id
	}
	return 0
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

func TestDetachMidCopyLeavesNoCorruption(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(1024)
	defer sub.Close()

	files := map[string]testFile{}
	body := strings.Repeat("x", 64<<10)
	for i := 0; i < 300; i++ {
		files[fmt.Sprintf("DCIM/IMG_%04d.JPG", i)] = testFile{body, time.Now().Add(-time.Hour)}
	}
	e.insertCard(t, "slot1", "CARD01", files)
	att := e.attach(t, "slot1", "CARD01")
	e.db.Cards.Create(ctx, att.Serial, "CARD01", "A", "copy")

	e.m.handleAttach(ctx, att)
	waitTopic(t, sub, bus.TopicJobProgress) // at least one file landed
	e.m.handleDetach(ctx, att.VolumeGUID)   // card yanked

	ev := waitTopic(t, sub, bus.TopicJobFailed)
	je := ev.Payload.(bus.JobEvent)
	if !strings.Contains(je.Error, "cartão removido") {
		t.Fatalf("error: %q", je.Error)
	}

	// Integrity: no tmp leftovers; every recorded file exists complete.
	for _, f := range listDest(t, e.dest) {
		if strings.HasSuffix(f, tmpSuffix) {
			t.Fatalf("tmp leftover: %s", f)
		}
	}
	rows, _, err := e.db.Files.ListByJob(ctx, je.JobID, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		info, err := os.Stat(r.DstPath)
		if err != nil || info.Size() != r.Size {
			t.Fatalf("recorded file corrupt/missing: %+v err=%v", r, err)
		}
	}
	if j, _ := e.db.Jobs.Get(ctx, je.JobID); j.Status != store.StatusFailed {
		t.Fatalf("status: %+v", j)
	}
}

func TestCancelViaAPI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(1024)
	defer sub.Close()

	files := map[string]testFile{}
	body := strings.Repeat("y", 64<<10)
	for i := 0; i < 300; i++ {
		files[fmt.Sprintf("V/%04d.MP4", i)] = testFile{body, time.Now().Add(-time.Hour)}
	}
	e.insertCard(t, "slot1", "CARD01", files)
	att := e.attach(t, "slot1", "CARD01")
	e.db.Cards.Create(ctx, att.Serial, "CARD01", "A", "copy")
	e.m.handleAttach(ctx, att)

	ev := waitTopic(t, sub, bus.TopicJobProgress)
	jobID := ev.Payload.(bus.JobEvent).JobID
	if err := e.m.Cancel(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	waitTopic(t, sub, bus.TopicJobFailed)
	if j, _ := e.db.Jobs.Get(ctx, jobID); j.Status != store.StatusCancelled {
		t.Fatalf("status: %+v", j)
	}
}

func TestRecoverSweepsTmpAndFailsInterrupted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	id, _ := e.db.Jobs.Create(ctx, store.Job{Status: store.StatusCopying, VolumeSerial: "S"})
	sub := filepath.Join(e.dest, "2026-07-01")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "IMG.JPG.cardpit-tmp"), []byte("partial"), 0o644)
	os.WriteFile(filepath.Join(sub, "OK.JPG"), []byte("done"), 0o644)

	if err := e.m.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if j, _ := e.db.Jobs.Get(ctx, id); j.Status != store.StatusFailed {
		t.Fatalf("interrupted job: %+v", j)
	}
	files := listDest(t, e.dest)
	if len(files) != 1 || files[0] != "2026-07-01/OK.JPG" {
		t.Fatalf("after sweep: %v", files)
	}
}

func TestParanoidModeCopies(t *testing.T) {
	c := newCopier()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jpg")
	os.WriteFile(src, []byte("content"), 0o644)
	info, _ := os.Stat(src)
	dst, hash, err := c.copyOne(context.Background(),
		fileEntry{src: src, name: "src.jpg", size: info.Size(), mtime: info.ModTime()},
		filepath.Join(dir, "out"), true)
	if err != nil || len(hash) != 16 {
		t.Fatalf("paranoid copy: %v %q", err, hash)
	}
	if b, _ := os.ReadFile(dst); string(b) != "content" {
		t.Fatalf("content: %q", b)
	}
}

func TestConcurrentSameNameCopiesDoNotClobber(t *testing.T) {
	c := newCopier()
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		src := filepath.Join(dir, fmt.Sprintf("card%d", i), "IMG_0001.JPG")
		os.MkdirAll(filepath.Dir(src), 0o755)
		body := strings.Repeat(fmt.Sprintf("%d", i), 1000)
		os.WriteFile(src, []byte(body), 0o644)
		info, _ := os.Stat(src)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = c.copyOne(context.Background(),
				fileEntry{src: src, name: "IMG_0001.JPG", size: info.Size(), mtime: info.ModTime()},
				out, false)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	files := listDest(t, out)
	if len(files) != workers {
		t.Fatalf("expected %d distinct files, got %v", workers, files)
	}
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(out, f))
		if err != nil || len(b) != 1000 {
			t.Fatalf("file %s corrupt: len=%d err=%v", f, len(b), err)
		}
		if seen[string(b[:1])] {
			t.Fatalf("duplicate content — a copy was clobbered: %v", files)
		}
		seen[string(b[:1])] = true
	}
}

func TestPickFreeName(t *testing.T) {
	dir := t.TempDir()
	if got := pickFreeName(dir, "a.jpg"); got != filepath.Join(dir, "a.jpg") {
		t.Fatalf("free: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("1"), 0o644)
	if got := pickFreeName(dir, "a.jpg"); got != filepath.Join(dir, "a (1).jpg") {
		t.Fatalf("taken: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "a (1).jpg"), []byte("2"), 0o644)
	if got := pickFreeName(dir, "a.jpg"); got != filepath.Join(dir, "a (2).jpg") {
		t.Fatalf("taken twice: %q", got)
	}
}

func TestExpandTemplate(t *testing.T) {
	mt := time.Date(2026, 7, 8, 23, 30, 0, 0, time.Local)
	if got := expandTemplate("", mt, "X"); got != "2026-07-08" {
		t.Fatalf("default: %q", got)
	}
	if got := expandTemplate(`{YYYY}/{MM}/{card_alias}`, mt, "Sony A7/IV"); got != "2026/07/Sony A7-IV" {
		t.Fatalf("tokens: %q", got)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"IMG.JPG": "photo", "raw.CR3": "photo", "clip.MP4": "video",
		"clip.MOV": "video", "notes.txt": "other",
	}
	for name, want := range cases {
		if got := classify(name); got != want {
			t.Fatalf("%s: %s != %s", name, got, want)
		}
	}
}
