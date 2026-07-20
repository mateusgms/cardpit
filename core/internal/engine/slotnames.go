package engine

import (
	"context"
	"fmt"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// autoSlotNames is the fixed pool of names handed out to reader slots the
// first time they are seen. The operator labels each physical reader with
// the assigned name, so names come from the append-only slot_name_log and
// are never reused — not even after a slot is deleted.
var autoSlotNames = []string{
	"Moisés", "Davi", "Noé", "Abraão", "Salomão",
	"Elias", "Samuel", "Daniel", "José", "Josué",
	"Pedro", "Paulo", "João", "Isaías", "Jeremias",
	"Gideão", "Calebe", "Débora", "Ester", "Rute",
}

// nextSlotName picks the first pool name absent from both the ever-assigned
// log and the current slot aliases (calibration may have used a pool name
// without logging it). With the pool exhausted it falls back to "Leitor N".
func nextSlotName(everAssigned map[string]bool, assignedCount int, current []store.Slot) string {
	inUse := make(map[string]bool, len(everAssigned)+len(current))
	for name := range everAssigned {
		inUse[name] = true
	}
	for _, s := range current {
		inUse[s.Alias] = true
	}
	for _, name := range autoSlotNames {
		if !inUse[name] {
			return name
		}
	}
	return fmt.Sprintf("Leitor %d", assignedCount+1)
}

// autoNameSlot registers a fixed name for a never-seen (locationPath, lun)
// and announces it (Telegram/SSE) so the operator can label the reader.
// Errors fall back to an empty slot: the ingest must not depend on naming.
func (m *Manager) autoNameSlot(ctx context.Context, locationPath string, lun int) (store.Slot, bool) {
	used, count, err := m.db.SlotNames.UsedAliases(ctx)
	if err != nil {
		m.log.Error("engine: reading slot name log", "err", err)
		return store.Slot{}, false
	}
	current, err := m.db.Slots.List(ctx)
	if err != nil {
		m.log.Error("engine: listing slots for auto-name", "err", err)
		return store.Slot{}, false
	}
	name := nextSlotName(used, count, current)
	slot, err := m.db.Slots.Upsert(ctx, locationPath, lun, name)
	if err != nil {
		m.log.Error("engine: auto-naming slot", "err", err)
		return store.Slot{}, false
	}
	if err := m.db.SlotNames.Append(ctx, name, locationPath, lun); err != nil {
		m.log.Error("engine: appending slot name log", "err", err)
	}
	m.log.Info("engine: slot auto-nomeado",
		"alias", name, "location", locationPath, "lun", lun)
	m.bus.Publish(bus.Event{Topic: bus.TopicSlotAutoNamed, Payload: bus.SlotAutoNamed{
		SlotID: slot.ID, Alias: slot.Alias, LocationPath: locationPath, LUN: lun,
	}})
	return slot, true
}
