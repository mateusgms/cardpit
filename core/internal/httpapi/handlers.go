package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/engine"
	"github.com/mateusgms/cardpit/core/internal/httpapi/webui"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// --- status -----------------------------------------------------------------

type statusResponse struct {
	Slots         []store.Slot        `json:"slots"`
	Volumes       []watcherVolume     `json:"volumes"`
	ActiveJobs    []store.Job         `json:"active_jobs"`
	DestMounted   bool                `json:"dest_mounted"`
	DestGUID      string              `json:"dest_guid"`
	WatcherPaused bool                `json:"watcher_paused"`
	Calibrating   *pendingCalibration `json:"calibrating,omitempty"`
	UIPlaceholder bool                `json:"ui_placeholder"`
	Version       string              `json:"version"`
}

type watcherVolume struct {
	VolumeGUID   string `json:"volume_guid"`
	Attached     bool   `json:"attached"`
	Serial       string `json:"serial"`
	Label        string `json:"label"`
	LocationPath string `json:"location_path"`
	LUN          int    `json:"lun"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slots, err := s.db.Slots.List(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	active, err := s.db.Jobs.Active(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var vols []watcherVolume
	for _, v := range s.watcher.Snapshot() {
		vols = append(vols, watcherVolume{
			VolumeGUID: v.VolumeGUID, Attached: v.Attached,
			Serial: v.Info.Serial, Label: v.Info.Label,
			LocationPath: v.Slot.LocationPath, LUN: v.Slot.LUN,
		})
	}
	guid := s.db.Settings.GetString(ctx, store.SetDestVolumeGUID, "")
	writeJSON(w, http.StatusOK, statusResponse{
		Slots:         slots,
		Volumes:       vols,
		ActiveJobs:    active,
		DestMounted:   s.manager.DestMounted(ctx),
		DestGUID:      guid,
		WatcherPaused: s.watcher.Paused(),
		Calibrating:   s.calibration.Load(),
		UIPlaceholder: webui.IsPlaceholder(),
		Version:       s.Version,
	})
}

// --- jobs -------------------------------------------------------------------

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size < 1 || size > 200 {
		size = 25
	}
	jobs, total, err := s.db.Jobs.ListPage(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs": jobs, "total": total, "page": page, "page_size": size,
	})
}

func (s *Server) handleJobFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id inválido")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size < 1 || size > 500 {
		size = 100
	}
	files, total, err := s.db.Files.ListByJob(r.Context(), id, size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files": files, "total": total, "page": page, "page_size": size,
	})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id inválido")
		return
	}
	if err := s.manager.Cancel(r.Context(), id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelando"})
}

// --- settings ---------------------------------------------------------------

// editableSettings whitelists PUT-able keys; secrets are sealed on write.
var editableSettings = map[string]bool{
	store.SetDestVolumeGUID:    true,
	store.SetDestTemplate:      true,
	store.SetMaxConcurrent:     true,
	store.SetVerifyMode:        true,
	store.SetEjectAfterCopy:    true,
	store.SetUnknownCardPolicy: true,
	store.SetRequireDCIM:       true,
	store.SetWatcherPaused:     true,
	store.SetTelegramChatIDs:   true,
	store.SetTelegramToken:     true, // sealed before storing
	store.SetAutoUpdate:        true,
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.db.Settings.All(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Apply defaults for keys never written.
	defaults := map[string]string{
		store.SetDestTemplate:      engine.DefaultTemplate,
		store.SetMaxConcurrent:     "4",
		store.SetVerifyMode:        "fast",
		store.SetEjectAfterCopy:    "true",
		store.SetUnknownCardPolicy: "ask",
		store.SetRequireDCIM:       "false",
		store.SetWatcherPaused:     "false",
		store.SetAutoUpdate:        "true",
	}
	for k, v := range defaults {
		if _, ok := all[k]; !ok {
			all[k] = v
		}
	}
	_, hasTg, _ := s.db.Settings.Get(ctx, store.SetTelegramToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":           all,
		"has_telegram_token": hasTg,
	})
}

var validEnum = map[string]map[string]bool{
	store.SetVerifyMode:        {"fast": true, "paranoid": true},
	store.SetUnknownCardPolicy: {"ask": true, "copy": true, "ignore": true},
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	for k, v := range body {
		if !editableSettings[k] {
			writeErr(w, http.StatusBadRequest, "configuração desconhecida: "+k)
			return
		}
		if enum, ok := validEnum[k]; ok && !enum[v] {
			writeErr(w, http.StatusBadRequest, "valor inválido para "+k)
			return
		}
		if k == store.SetMaxConcurrent {
			if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 16 {
				writeErr(w, http.StatusBadRequest, "max_concurrent_jobs deve ser 1–16")
				return
			}
		}
	}
	for k, v := range body {
		if k == store.SetTelegramToken {
			if v == "" {
				continue // never blank out the token accidentally
			}
			sealed, err := s.secrets.Seal([]byte(v))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			v = sealed
		}
		if err := s.db.Settings.Set(ctx, k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if k == store.SetWatcherPaused {
			s.watcher.SetPaused(v == "true")
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok",
		"note": "max_concurrent_jobs requer reinício do serviço para valer"})
}

// --- cards ------------------------------------------------------------------

func (s *Server) handleListCards(w http.ResponseWriter, r *http.Request) {
	cards, err := s.db.Cards.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cards": cards})
}

func (s *Server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Serial string `json:"serial"`
		Label  string `json:"label"`
		Alias  string `json:"alias"`
		Policy string `json:"policy"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Serial == "" {
		writeErr(w, http.StatusBadRequest, "serial é obrigatório")
		return
	}
	if body.Policy != "" && body.Policy != "copy" && body.Policy != "ignore" {
		writeErr(w, http.StatusBadRequest, "policy deve ser copy ou ignore")
		return
	}
	card, err := s.db.Cards.Create(r.Context(), body.Serial, body.Label, body.Alias, body.Policy)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, card)
}

func (s *Server) handleUpdateCard(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id inválido")
		return
	}
	var body struct {
		Alias  string `json:"alias"`
		Policy string `json:"policy"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Policy != "copy" && body.Policy != "ignore" {
		writeErr(w, http.StatusBadRequest, "policy deve ser copy ou ignore")
		return
	}
	if err := s.db.Cards.Update(r.Context(), id, body.Alias, body.Policy); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id inválido")
		return
	}
	if err := s.db.Cards.Delete(r.Context(), id); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCardDecision lets the UI answer an unknown-card question (same
// contract as the Telegram inline buttons).
func (s *Server) handleCardDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Serial string `json:"serial"`
		Action string `json:"action"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	switch body.Action {
	case "copy", "ignore", "always_ignore":
	default:
		writeErr(w, http.StatusBadRequest, "action deve ser copy, ignore ou always_ignore")
		return
	}
	s.bus.Publish(bus.Event{Topic: bus.TopicCardDecision, Payload: bus.CardDecision{
		Serial: body.Serial, Action: body.Action,
	}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- slots ------------------------------------------------------------------

func (s *Server) handleListSlots(w http.ResponseWriter, r *http.Request) {
	slots, err := s.db.Slots.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (s *Server) handleUpdateSlot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id inválido")
		return
	}
	var body struct {
		Alias string `json:"alias"`
	}
	if err := readJSON(r, &body); err != nil || body.Alias == "" {
		writeErr(w, http.StatusBadRequest, "alias é obrigatório")
		return
	}
	if err := s.db.Slots.Rename(r.Context(), id, body.Alias); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteSlot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id inválido")
		return
	}
	if err := s.db.Slots.Delete(r.Context(), id); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCalibrate arms the wizard: the next attached volume gets the alias.
func (s *Server) handleCalibrate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias string `json:"alias"`
	}
	if err := readJSON(r, &body); err != nil || body.Alias == "" {
		writeErr(w, http.StatusBadRequest, "alias é obrigatório")
		return
	}
	pc := &pendingCalibration{
		Alias:    body.Alias,
		ArmedAt:  time.Now(),
		Deadline: time.Now().Add(2 * time.Minute),
	}
	s.calibration.Store(pc)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "aguardando cartão",
		"deadline": pc.Deadline.Format(time.RFC3339),
	})
}

func (s *Server) handleCancelCalibrate(w http.ResponseWriter, r *http.Request) {
	s.calibration.Store(nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- telegram ----------------------------------------------------------------

func (s *Server) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	if s.TgTest == nil {
		writeErr(w, http.StatusConflict, "notificador Telegram não configurado")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.TgTest(ctx); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "mensagem enviada"})
}

// --- update ------------------------------------------------------------------

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.CheckNow == nil {
		writeErr(w, http.StatusConflict, "atualizador não disponível")
		return
	}
	s.CheckNow()
	writeJSON(w, http.StatusOK, map[string]string{"status": "verificação iniciada"})
}
