package fake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mateusgms/cardpit/core/internal/platform"
)

func setup(t *testing.T) (platform.Platform, string, string) {
	t.Helper()
	root := t.TempDir()
	dest := t.TempDir()
	return New(root, dest), root, dest
}

func insertCard(t *testing.T, root, slot, card string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, slot, card)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListAndInfo(t *testing.T) {
	p, root, _ := setup(t)
	ctx := context.Background()

	vols, err := p.Volumes.ListRemovableVolumes(ctx)
	if err != nil || len(vols) != 0 {
		t.Fatalf("empty root: %v %v", vols, err)
	}

	insertCard(t, root, "slot1", "CARD01", map[string]string{
		"DCIM/100/IMG_0001.JPG": "aaaa",
		"DCIM/100/IMG_0002.JPG": "bbbbbb",
	})
	vols, err = p.Volumes.ListRemovableVolumes(ctx)
	if err != nil || len(vols) != 1 {
		t.Fatalf("one card: %v %v", vols, err)
	}
	if vols[0].GUIDPath != "fake://slot1/CARD01" {
		t.Fatalf("guid: %q", vols[0].GUIDPath)
	}

	info, err := p.Info.VolumeInfo(ctx, vols[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Label != "CARD01" || info.TotalBytes != 10 || len(info.Serial) != 8 {
		t.Fatalf("info: %+v", info)
	}

	// Serial must be stable across calls (re-insertion identity).
	info2, _ := p.Info.VolumeInfo(ctx, vols[0])
	if info2.Serial != info.Serial {
		t.Fatal("serial not stable")
	}

	key, err := p.Slots.ResolveSlot(ctx, vols[0])
	if err != nil || key.LocationPath != "FAKE#slot1" || key.LUN != 0 {
		t.Fatalf("slot: %+v %v", key, err)
	}
}

func TestSerialOverrideFile(t *testing.T) {
	p, root, _ := setup(t)
	insertCard(t, root, "slot1", "CARD01", map[string]string{".cardpit-serial": "CAFE0001\n"})
	info, err := p.Info.VolumeInfo(context.Background(), platform.VolumeID{GUIDPath: "fake://slot1/CARD01"})
	if err != nil || info.Serial != "CAFE0001" {
		t.Fatalf("serial: %+v %v", info, err)
	}
}

func TestEjectDetaches(t *testing.T) {
	p, root, _ := setup(t)
	ctx := context.Background()
	insertCard(t, root, "slot1", "CARD01", map[string]string{"a.jpg": "x"})

	if err := p.Eject.Eject(ctx, platform.VolumeID{GUIDPath: "fake://slot1/CARD01"}); err != nil {
		t.Fatal(err)
	}
	vols, _ := p.Volumes.ListRemovableVolumes(ctx)
	if len(vols) != 0 {
		t.Fatalf("still listed after eject: %v", vols)
	}
}

func TestDestResolution(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dest := filepath.Join(t.TempDir(), "ssd")
	p := New(root, dest)

	if _, err := p.Dest.ResolveDest(ctx, "any-guid"); err != platform.ErrDestNotPresent {
		t.Fatalf("missing dest: %v", err)
	}
	os.MkdirAll(dest, 0o755)
	got, err := p.Dest.ResolveDest(ctx, "any-guid")
	if err != nil || got != dest {
		t.Fatalf("dest: %q %v", got, err)
	}
}

func TestListDestCandidates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dest := filepath.Join(t.TempDir(), "ssd")
	p := New(root, dest)

	got, err := p.DestList.ListDestCandidates(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing dest: %v %v", got, err)
	}

	os.MkdirAll(dest, 0o755)
	got, err = p.DestList.ListDestCandidates(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("existing dest: %v %v", got, err)
	}
	if got[0].GUIDPath != "fake-dest" || got[0].Label == "" || got[0].System {
		t.Fatalf("candidate: %+v", got[0])
	}
}

func TestFreeSpaceOverride(t *testing.T) {
	p, _, dest := setup(t)
	ctx := context.Background()
	n, err := p.Space.FreeSpace(ctx, dest)
	if err != nil || n != 1<<40 {
		t.Fatalf("default: %d %v", n, err)
	}
	os.WriteFile(filepath.Join(dest, ".cardpit-freespace"), []byte("1000"), 0o644)
	n, _ = p.Space.FreeSpace(ctx, dest)
	if n != 1000 {
		t.Fatalf("override: %d", n)
	}
}
