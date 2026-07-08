package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleSSE streams bus events as Server-Sent Events. Each event is
// `event: <topic>` with a JSON payload. A `hello` event opens the stream so
// clients know the pipe works, and a comment heartbeat every 15 s keeps
// proxies from idling the connection out.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming não suportado")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := s.bus.Subscribe(256)
	defer sub.Close()

	rc := http.NewResponseController(w)
	writeEvent := func(name string, payload any) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		// Bound each write so one dead client cannot pin the goroutine.
		rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !writeEvent("hello", map[string]any{"at": time.Now().Format(time.RFC3339)}) {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e := <-sub.C:
			if !writeEvent(string(e.Topic), e.Payload) {
				return
			}
		}
	}
}
