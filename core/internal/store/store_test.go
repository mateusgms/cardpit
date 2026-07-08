package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path) // second open must not re-apply
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}

func TestCardLifecycle(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, found, err := db.Cards.TouchSeen(ctx, "ABCD1234", "SDCARD"); err != nil || found {
		t.Fatalf("unknown card: found=%v err=%v", found, err)
	}
	c, err := db.Cards.Create(ctx, "ABCD1234", "SDCARD", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Alias != "SDCARD" || c.Policy != "copy" {
		t.Fatalf("defaults: %+v", c)
	}
	c2, found, err := db.Cards.TouchSeen(ctx, "ABCD1234", "RENAMED")
	if err != nil || !found || c2.Label != "RENAMED" || c2.LastSeenAt == "" {
		t.Fatalf("touch: found=%v card=%+v err=%v", found, c2, err)
	}
	if err := db.Cards.Update(ctx, c.ID, "Sandisk A", "ignore"); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Cards.FindBySerial(ctx, "ABCD1234")
	if got.Policy != "ignore" || got.Alias != "Sandisk A" {
		t.Fatalf("update: %+v", got)
	}
	if err := db.Cards.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Cards.Delete(ctx, c.ID); err != ErrNotFound {
		t.Fatalf("double delete: %v", err)
	}
}

func TestSlotUpsertByKey(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	s1, err := db.Slots.Upsert(ctx, "PCIROOT(0)#USB(2)", 0, "Leitor esquerdo")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := db.Slots.Upsert(ctx, "PCIROOT(0)#USB(2)", 0, "Renomeado")
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID != s2.ID || s2.Alias != "Renomeado" {
		t.Fatalf("upsert changed identity: %+v vs %+v", s1, s2)
	}
	s3, err := db.Slots.Upsert(ctx, "PCIROOT(0)#USB(2)", 1, "microSD")
	if err != nil {
		t.Fatal(err)
	}
	if s3.ID == s1.ID {
		t.Fatal("different LUN must be a different slot")
	}
}

func TestJobLifecycleAndPagination(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := db.Jobs.Create(ctx, Job{VolumeSerial: "S1", Status: StatusPending})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := db.Jobs.SetTotals(ctx, ids[0], 10, 1000, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Jobs.UpdateProgress(ctx, ids[0], 4, 400, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Jobs.Finish(ctx, ids[0], StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	j, err := db.Jobs.Get(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusDone || j.FilesTotal != 10 || j.FilesCopied != 4 || j.FinishedAt == "" || j.Error != "" {
		t.Fatalf("job: %+v", j)
	}

	page, total, err := db.Jobs.ListPage(ctx, 2, 2)
	if err != nil || total != 5 || len(page) != 2 {
		t.Fatalf("page: total=%d len=%d err=%v", total, len(page), err)
	}
	if page[0].ID != ids[2] { // newest first: 5,4 | 3,2 | 1
		t.Fatalf("expected id %d, got %d", ids[2], page[0].ID)
	}

	active, err := db.Jobs.Active(ctx)
	if err != nil || len(active) != 4 {
		t.Fatalf("active: %d err=%v", len(active), err)
	}
}

func TestAwaitingDecisionAndRecovery(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	awID, _ := db.Jobs.Create(ctx, Job{VolumeSerial: "AA11", Status: StatusAwaitingDecision})
	cpID, _ := db.Jobs.Create(ctx, Job{VolumeSerial: "BB22", Status: StatusCopying})

	j, err := db.Jobs.FindAwaitingBySerial(ctx, "AA11")
	if err != nil || j.ID != awID {
		t.Fatalf("find awaiting: %+v err=%v", j, err)
	}

	n, err := db.Jobs.FailInterrupted(ctx, "interrompido")
	if err != nil || n != 1 {
		t.Fatalf("recovery: n=%d err=%v", n, err)
	}
	if j, _ := db.Jobs.Get(ctx, cpID); j.Status != StatusFailed || j.Error != "interrompido" {
		t.Fatalf("copying job after recovery: %+v", j)
	}
	if j, _ := db.Jobs.Get(ctx, awID); j.Status != StatusCancelled {
		t.Fatalf("awaiting job after recovery: %+v", j)
	}
}

func TestDedupIndex(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	jobID, _ := db.Jobs.Create(ctx, Job{Status: StatusCopying})
	f := IngestedFile{
		JobID: jobID, SrcPath: "E:/DCIM/IMG_0001.JPG", DstPath: "D:/2026-07-08/IMG_0001.JPG",
		Size: 1234, Mtime: "2026-07-08T10:00:00Z", XXHash: "00deadbeef00cafe", MediaType: "photo",
	}
	if err := db.Files.Insert(ctx, f); err != nil {
		t.Fatal(err)
	}

	if ok, _ := db.Files.HasSizeMtime(ctx, 1234, f.Mtime); !ok {
		t.Fatal("HasSizeMtime miss")
	}
	if ok, _ := db.Files.HasSizeMtime(ctx, 999, f.Mtime); ok {
		t.Fatal("HasSizeMtime false positive")
	}
	if ok, _ := db.Files.ExistsHash(ctx, 1234, f.Mtime, f.XXHash); !ok {
		t.Fatal("ExistsHash miss")
	}
	if ok, _ := db.Files.ExistsHash(ctx, 1234, f.Mtime, "0000000000000000"); ok {
		t.Fatal("ExistsHash false positive")
	}

	files, total, err := db.Files.ListByJob(ctx, jobID, 10, 0)
	if err != nil || total != 1 || len(files) != 1 {
		t.Fatalf("list: total=%d err=%v", total, err)
	}
	stats, err := db.Files.StatsByJob(ctx, jobID)
	if err != nil || stats["photo"].Count != 1 || stats["photo"].Bytes != 1234 {
		t.Fatalf("stats: %+v err=%v", stats, err)
	}
}

func TestSettings(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if v := db.Settings.GetInt(ctx, SetMaxConcurrent, 4); v != 4 {
		t.Fatalf("default int: %d", v)
	}
	if err := db.Settings.Set(ctx, SetMaxConcurrent, "8"); err != nil {
		t.Fatal(err)
	}
	if v := db.Settings.GetInt(ctx, SetMaxConcurrent, 4); v != 8 {
		t.Fatalf("int: %d", v)
	}
	db.Settings.Set(ctx, SetEjectAfterCopy, "false")
	if db.Settings.GetBool(ctx, SetEjectAfterCopy, true) {
		t.Fatal("bool not read")
	}
	db.Settings.Set(ctx, SetAPIToken, "sealed-blob")
	all, err := db.Settings.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := all[SetAPIToken]; leaked {
		t.Fatal("secret leaked via All()")
	}
	if all[SetMaxConcurrent] != "8" {
		t.Fatalf("all: %+v", all)
	}
}
