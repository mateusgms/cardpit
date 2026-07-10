package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mateusgms/cardpit/core/internal/bus"
	"github.com/mateusgms/cardpit/core/internal/engine"
	"github.com/mateusgms/cardpit/core/internal/logging"
	"github.com/mateusgms/cardpit/core/internal/platform/fake"
	"github.com/mateusgms/cardpit/core/internal/secret"
	"github.com/mateusgms/cardpit/core/internal/store"
	"github.com/mateusgms/cardpit/core/internal/watcher"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type testServer struct {
	s   *Server
	ts  *httptest.Server
	db  *store.DB
	bus *bus.Bus
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	b := bus.New()
	p := fake.New(t.TempDir(), t.TempDir())
	w := watcher.New(p, b, watcher.Options{PollInterval: time.Hour, Debounce: 0}, discard)
	m := engine.NewManager(db, p, b, discard)

	s := New(db, b, w, m, secret.PlainBox{}, "127.0.0.1:0", discard, logging.NewRing(64), new(slog.LevelVar))
	s.DestCandidates = p.DestList
	if err := s.initToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.watchCalibration(ctx)

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return &testServer{s: s, ts: ts, db: db, bus: b}
}

func (e *testServer) req(t *testing.T, method, path, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestAuth(t *testing.T) {
	e := newTestServer(t)
	if resp, _ := e.req(t, "GET", "/api/status", "", nil); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := e.req(t, "GET", "/api/status", "wrong", nil); resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp, _ := e.req(t, "GET", "/api/status", e.s.token, nil); resp.StatusCode != 200 {
		t.Fatalf("right token: %d", resp.StatusCode)
	}
	// UI is served without auth.
	if resp, _ := e.req(t, "GET", "/", "", nil); resp.StatusCode != 200 {
		t.Fatalf("ui: %d", resp.StatusCode)
	}
}

func TestTokenPersistsAcrossRestart(t *testing.T) {
	e := newTestServer(t)
	first := e.s.token
	s2 := New(e.db, e.bus, e.s.watcher, e.s.manager, secret.PlainBox{}, "", discard, logging.NewRing(64), new(slog.LevelVar))
	if err := s2.initToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s2.token != first {
		t.Fatal("token changed across restart")
	}
}

func TestCardsCRUD(t *testing.T) {
	e := newTestServer(t)
	tok := e.s.token

	resp, data := e.req(t, "POST", "/api/cards", tok,
		map[string]string{"serial": "AA11BB22", "label": "SD64", "alias": "Sandisk A"})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, data)
	}
	var card store.Card
	json.Unmarshal(data, &card)

	resp, _ = e.req(t, "PUT", fmt.Sprintf("/api/cards/%d", card.ID), tok,
		map[string]string{"alias": "Renomeado", "policy": "ignore"})
	if resp.StatusCode != 200 {
		t.Fatalf("update: %d", resp.StatusCode)
	}

	_, data = e.req(t, "GET", "/api/cards", tok, nil)
	var list struct {
		Cards []store.Card `json:"cards"`
	}
	json.Unmarshal(data, &list)
	if len(list.Cards) != 1 || list.Cards[0].Alias != "Renomeado" || list.Cards[0].Policy != "ignore" {
		t.Fatalf("list: %+v", list)
	}

	resp, _ = e.req(t, "DELETE", fmt.Sprintf("/api/cards/%d", card.ID), tok, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp, _ = e.req(t, "DELETE", fmt.Sprintf("/api/cards/%d", card.ID), tok, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("double delete: %d", resp.StatusCode)
	}
}

func TestCalibrationFlow(t *testing.T) {
	e := newTestServer(t)
	tok := e.s.token

	resp, _ := e.req(t, "POST", "/api/slots/calibrate", tok, map[string]string{"alias": "Leitor esquerdo"})
	if resp.StatusCode != 200 {
		t.Fatalf("arm: %d", resp.StatusCode)
	}

	sub := e.bus.Subscribe(8, bus.TopicSlotCalibrated)
	defer sub.Close()
	e.bus.Publish(bus.Event{Topic: bus.TopicVolumeAttached, Payload: bus.VolumeAttached{
		VolumeGUID: "fake://slot1/C", SlotLocationPath: "FAKE#slot1", SlotLUN: 2,
	}})

	select {
	case ev := <-sub.C:
		sc := ev.Payload.(bus.SlotCalibrated)
		if sc.Alias != "Leitor esquerdo" || sc.LUN != 2 {
			t.Fatalf("calibrated: %+v", sc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("calibration never fired")
	}

	_, data := e.req(t, "GET", "/api/slots", tok, nil)
	var list struct {
		Slots []store.Slot `json:"slots"`
	}
	json.Unmarshal(data, &list)
	if len(list.Slots) != 1 || list.Slots[0].LocationPath != "FAKE#slot1" {
		t.Fatalf("slots: %+v", list)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	e := newTestServer(t)
	tok := e.s.token

	// Defaults come back even before anything is written.
	_, data := e.req(t, "GET", "/api/settings", tok, nil)
	var got struct {
		Settings map[string]string `json:"settings"`
		HasTg    bool              `json:"has_telegram_token"`
	}
	json.Unmarshal(data, &got)
	if got.Settings[store.SetVerifyMode] != "fast" || got.HasTg {
		t.Fatalf("defaults: %+v", got)
	}

	resp, data := e.req(t, "PUT", "/api/settings", tok, map[string]string{
		store.SetVerifyMode:    "paranoid",
		store.SetTelegramToken: "123:abc",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("put: %d %s", resp.StatusCode, data)
	}

	_, data = e.req(t, "GET", "/api/settings", tok, nil)
	json.Unmarshal(data, &got)
	if got.Settings[store.SetVerifyMode] != "paranoid" || !got.HasTg {
		t.Fatalf("after put: %+v", got)
	}
	if _, leaked := got.Settings[store.SetTelegramToken]; leaked {
		t.Fatal("telegram token leaked in settings response")
	}
	// Stored sealed, not plaintext.
	raw, _, _ := e.db.Settings.Get(context.Background(), store.SetTelegramToken)
	if raw == "123:abc" || !strings.HasPrefix(raw, "plain:") {
		t.Fatalf("token not sealed: %q", raw)
	}

	resp, _ = e.req(t, "PUT", "/api/settings", tok, map[string]string{"bogus_key": "1"})
	if resp.StatusCode != 400 {
		t.Fatalf("bogus key: %d", resp.StatusCode)
	}
	resp, _ = e.req(t, "PUT", "/api/settings", tok, map[string]string{store.SetVerifyMode: "yolo"})
	if resp.StatusCode != 400 {
		t.Fatalf("bad enum: %d", resp.StatusCode)
	}
}

func TestJobsPagination(t *testing.T) {
	e := newTestServer(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		e.db.Jobs.Create(ctx, store.Job{Status: store.StatusDone, VolumeSerial: "S"})
	}
	_, data := e.req(t, "GET", "/api/jobs?page=2&page_size=3", e.s.token, nil)
	var got struct {
		Jobs  []store.Job `json:"jobs"`
		Total int         `json:"total"`
	}
	json.Unmarshal(data, &got)
	if got.Total != 7 || len(got.Jobs) != 3 {
		t.Fatalf("pagination: total=%d len=%d", got.Total, len(got.Jobs))
	}
}

func TestSSEStreamWithQueryToken(t *testing.T) {
	e := newTestServer(t)

	req, _ := http.NewRequest("GET", e.ts.URL+"/api/events?token="+e.s.token, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sse status: %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	readEvent := func() string {
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("sse read: %v", err)
			}
			line = strings.TrimRight(line, "\n")
			if line == "" {
				if len(lines) > 0 {
					return strings.Join(lines, "\n")
				}
				continue
			}
			lines = append(lines, line)
		}
	}

	if ev := readEvent(); !strings.Contains(ev, "event: hello") {
		t.Fatalf("first event: %q", ev)
	}
	e.bus.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: bus.JobEvent{JobID: 42}})
	ev := readEvent()
	if !strings.Contains(ev, "event: job.progress") || !strings.Contains(ev, `"job_id":42`) {
		t.Fatalf("published event: %q", ev)
	}

	// Header-less, token-less SSE is rejected.
	resp2, _ := http.Get(e.ts.URL + "/api/events")
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("unauthenticated sse: %d", resp2.StatusCode)
	}
}

func TestDestCandidatesEndpoint(t *testing.T) {
	e := newTestServer(t)

	if resp, _ := e.req(t, "GET", "/api/volumes/dest-candidates", "", nil); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}

	_, data := e.req(t, "GET", "/api/volumes/dest-candidates", e.s.token, nil)
	var got struct {
		Candidates []destCandidateDTO `json:"candidates"`
	}
	json.Unmarshal(data, &got)
	// The fake platform offers its dest dir under the magic GUID.
	if len(got.Candidates) != 1 || got.Candidates[0].VolumeGUID != "fake-dest" {
		t.Fatalf("candidates: %s", data)
	}

	// Nil lister must degrade to an empty list, not a crash.
	e.s.DestCandidates = nil
	resp, data := e.req(t, "GET", "/api/volumes/dest-candidates", e.s.token, nil)
	json.Unmarshal(data, &got)
	if resp.StatusCode != 200 || len(got.Candidates) != 0 {
		t.Fatalf("nil lister: %d %s", resp.StatusCode, data)
	}
}

func TestCancelNonexistentJob(t *testing.T) {
	e := newTestServer(t)
	resp, _ := e.req(t, "POST", "/api/jobs/999/cancel", e.s.token, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}
}
