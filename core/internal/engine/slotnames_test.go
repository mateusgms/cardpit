package engine

import (
	"context"
	"testing"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/platform"
	"github.com/mateusgms/cardpit/core/internal/store"
)

func TestNextSlotName(t *testing.T) {
	if got := nextSlotName(nil, 0, nil); got != "Moisés" {
		t.Fatalf("first name = %q; want Moisés", got)
	}
	if got := nextSlotName(map[string]bool{"Moisés": true}, 1, nil); got != "Davi" {
		t.Fatalf("second name = %q; want Davi", got)
	}
	// A calibration-chosen alias blocks the pool name even without a log entry.
	if got := nextSlotName(nil, 0, []store.Slot{{Alias: "Moisés"}}); got != "Davi" {
		t.Fatalf("name with calibrated Moisés = %q; want Davi", got)
	}
	// Pool exhausted → sequential fallback based on the log size.
	all := map[string]bool{}
	for _, n := range autoSlotNames {
		all[n] = true
	}
	if got := nextSlotName(all, len(autoSlotNames), nil); got != "Leitor 21" {
		t.Fatalf("fallback name = %q; want Leitor 21", got)
	}
}

func TestSlotAutoNaming(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	e.insertCard(t, "slot1", "CARD01", map[string]testFile{"IMG.JPG": {"a", time.Now()}})
	att1 := e.attach(t, "slot1", "CARD01")
	e.db.Cards.Create(ctx, att1.Serial, "CARD01", "A", "copy")
	e.m.handleAttach(ctx, att1)

	ev := waitTopic(t, sub, bus.TopicSlotAutoNamed)
	named := ev.Payload.(bus.SlotAutoNamed)
	if named.Alias != "Moisés" || named.LocationPath != "FAKE#slot1" {
		t.Fatalf("auto-named: %+v", named)
	}
	je := waitTopic(t, sub, bus.TopicJobCompleted).Payload.(bus.JobEvent)
	if je.SlotAlias != "Moisés" {
		t.Fatalf("job slot alias = %q; want Moisés", je.SlotAlias)
	}

	// Second reader gets the second name.
	e.insertCard(t, "slot2", "CARD02", map[string]testFile{"IMG.JPG": {"b", time.Now()}})
	att2 := e.attach(t, "slot2", "CARD02")
	e.db.Cards.Create(ctx, att2.Serial, "CARD02", "B", "copy")
	e.m.handleAttach(ctx, att2)
	if got := waitTopic(t, sub, bus.TopicSlotAutoNamed).Payload.(bus.SlotAutoNamed); got.Alias != "Davi" {
		t.Fatalf("second slot alias = %q; want Davi", got.Alias)
	}
	waitTopic(t, sub, bus.TopicJobCompleted)

	// Re-attach on a known reader reuses the stored name, no new assignment.
	if alias, _ := e.m.slotAlias(ctx, att1); alias != "Moisés" {
		t.Fatalf("re-attach alias = %q; want Moisés", alias)
	}
	if hist, _ := e.db.SlotNames.List(ctx); len(hist) != 2 {
		t.Fatalf("history entries = %d; want 2", len(hist))
	}

	// Deleting a slot never returns its name to the pool: the label on the
	// physical reader must stay unique forever. (Deleted slot has no jobs —
	// jobs.slot_id keeps a FK to slots.)
	slot3, ok := e.m.autoNameSlot(ctx, "FAKE#slot3", 0)
	if !ok || slot3.Alias != "Noé" {
		t.Fatalf("third slot: %+v ok=%v; want Noé", slot3, ok)
	}
	if err := e.db.Slots.Delete(ctx, slot3.ID); err != nil {
		t.Fatal(err)
	}
	if alias, _ := e.m.slotAlias(ctx, e.attachOn(t, "slot4", "CARD04")); alias != "Abraão" {
		t.Fatalf("post-delete alias = %q; want Abraão", alias)
	}
}

// attachOn builds an attach payload without requiring card files on disk.
func (e *env) attachOn(t *testing.T, slot, card string) bus.VolumeAttached {
	t.Helper()
	e.insertCard(t, slot, card, map[string]testFile{"IMG.JPG": {"x", time.Now()}})
	return e.attach(t, slot, card)
}

func TestSlotCalibrationAliasKept(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.db.Slots.Upsert(ctx, "FAKE#slot1", 0, "Leitor esquerdo"); err != nil {
		t.Fatal(err)
	}
	if alias, _ := e.m.slotAlias(ctx, e.attachOn(t, "slot1", "CARD01")); alias != "Leitor esquerdo" {
		t.Fatalf("alias = %q; want Leitor esquerdo", alias)
	}
	if hist, _ := e.db.SlotNames.List(ctx); len(hist) != 0 {
		t.Fatalf("history entries = %d; want 0", len(hist))
	}
}

func TestUnknownCardCopiedByDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.b.Subscribe(64)
	defer sub.Close()

	// No policy setting written: the kiosk default copies right away.
	e.insertCard(t, "slot1", "NEWCARD", map[string]testFile{"IMG.JPG": {"data", time.Now()}})
	e.m.handleAttach(ctx, e.attach(t, "slot1", "NEWCARD"))
	je := waitTopic(t, sub, bus.TopicJobCompleted).Payload.(bus.JobEvent)
	if je.FilesCopied != 1 {
		t.Fatalf("job event: %+v", je)
	}
}

// stubDestList serves fixed candidates for the default-destination seed test.
type stubDestList struct{ cands []platform.DestCandidate }

func (s stubDestList) ListDestCandidates(context.Context) ([]platform.DestCandidate, error) {
	return s.cands, nil
}

func TestSeedDefaultDest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	dCand := platform.DestCandidate{DriveLetter: "D:", GUIDPath: `\\?\Volume{d}\`, Label: "SSD"}
	sysCand := platform.DestCandidate{DriveLetter: "C:", GUIDPath: `\\?\Volume{c}\`, System: true}
	e.m.p.DestList = stubDestList{cands: []platform.DestCandidate{sysCand, dCand}}

	// Already configured (newEnv sets fake-dest): never overwritten.
	e.m.seedDefaultDest(ctx)
	if got := e.db.Settings.GetString(ctx, store.SetDestVolumeGUID, ""); got != "fake-dest" {
		t.Fatalf("dest overwritten: %q", got)
	}

	// Empty setting: D: is picked, C: (system) is not.
	e.db.Settings.Set(ctx, store.SetDestVolumeGUID, "")
	e.m.seedDefaultDest(ctx)
	if got := e.db.Settings.GetString(ctx, store.SetDestVolumeGUID, ""); got != dCand.GUIDPath {
		t.Fatalf("seeded dest = %q; want %q", got, dCand.GUIDPath)
	}

	// No D: present: stays empty and retries later.
	e.db.Settings.Set(ctx, store.SetDestVolumeGUID, "")
	e.m.p.DestList = stubDestList{cands: []platform.DestCandidate{sysCand}}
	e.m.seedDefaultDest(ctx)
	if got := e.db.Settings.GetString(ctx, store.SetDestVolumeGUID, ""); got != "" {
		t.Fatalf("dest seeded without D:: %q", got)
	}
}
