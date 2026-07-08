//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"

	"github.com/getlantern/systray"

	"github.com/mateusgms/cardpit/core/internal/config"
	"github.com/mateusgms/cardpit/core/internal/platform/win"
	"github.com/mateusgms/cardpit/core/internal/store"
)

// The tray is a SEPARATE per-user process: a Windows service lives in
// Session 0 and cannot own a tray icon. It reads the API token from the
// shared SQLite settings (DPAPI LOCAL_MACHINE seals make that possible) and
// drives the worker exclusively through the HTTP API.
func trayCmd(args []string) error {
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "config file (same one the service uses)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	token, err := readAPIToken(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("lendo token da API (o serviço já rodou ao menos uma vez?): %w", err)
	}
	t := &tray{baseURL: baseURL(cfg.Listen), token: token}
	systray.Run(t.onReady, nil)
	return nil
}

func readAPIToken(dbPath string) (string, error) {
	db, err := store.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	sealed, ok, err := db.Settings.Get(context.Background(), store.SetAPIToken)
	if err != nil || !ok {
		return "", fmt.Errorf("api_token ausente (%v)", err)
	}
	plain, err := win.DPAPIBox{}.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func baseURL(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = "8532"
	}
	return "http://localhost:" + port
}

type tray struct {
	baseURL string
	token   string
}

func (t *tray) onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("cardpit")
	systray.SetTooltip("cardpit — ingestão de cartões")

	open := systray.AddMenuItem("Abrir painel", "Abre a interface web")
	pause := systray.AddMenuItemCheckbox("Pausar detecção", "Ignora novos cartões", false)
	systray.AddSeparator()
	quit := systray.AddMenuItem("Sair", "Fecha o ícone (o serviço continua)")

	// Reflect current pause state.
	go func() {
		for {
			paused, err := t.watcherPaused()
			if err == nil {
				if paused {
					pause.Check()
				} else {
					pause.Uncheck()
				}
			}
			time.Sleep(15 * time.Second)
		}
	}()

	go func() {
		for {
			select {
			case <-open.ClickedCh:
				exec.Command("cmd", "/c", "start", t.baseURL).Start()
			case <-pause.ClickedCh:
				target := "true"
				if pause.Checked() {
					target = "false"
				}
				if err := t.setPaused(target); err == nil {
					if target == "true" {
						pause.Check()
					} else {
						pause.Uncheck()
					}
				}
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func (t *tray) do(method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(method, t.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Do(req)
}

func (t *tray) watcherPaused() (bool, error) {
	resp, err := t.do("GET", "/api/status", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var st struct {
		WatcherPaused bool `json:"watcher_paused"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return false, err
	}
	return st.WatcherPaused, nil
}

func (t *tray) setPaused(v string) error {
	body, _ := json.Marshal(map[string]string{"watcher_paused": v})
	resp, err := t.do("PUT", "/api/settings", body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// trayIcon builds a 16×16 32-bpp ICO in memory (blue "card" square with a
// cut corner) — no asset files to ship.
func trayIcon() []byte {
	const n = 16
	// BGRA, top-down while drawing; ICO stores bottom-up.
	px := make([][4]byte, n*n)
	blue := [4]byte{0xFF, 0xA3, 0x4D, 0xFF} // BGRA: #4DA3FF
	dark := [4]byte{0xB0, 0x6E, 0x2A, 0xFF} // border
	blank := [4]byte{0, 0, 0, 0}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := blue
			border := x == 1 || x == n-2 || y == 1 || y == n-2
			outside := x == 0 || x == n-1 || y == 0 || y == n-1
			notch := x+y < 6 // cut corner, like an SD card
			switch {
			case outside || notch && x+y < 4:
				c = blank
			case notch:
				c = dark
			case border:
				c = dark
			}
			px[y*n+x] = c
		}
	}

	var img bytes.Buffer
	// BITMAPINFOHEADER: height doubled (XOR + AND masks).
	bih := struct {
		Size          uint32
		Width, Height int32
		Planes, BPP   uint16
		Compression   uint32
		SizeImage     uint32
		XPPM, YPPM    int32
		ClrUsed, ClrI uint32
	}{Size: 40, Width: n, Height: n * 2, Planes: 1, BPP: 32}
	binary.Write(&img, binary.LittleEndian, bih)
	for y := n - 1; y >= 0; y-- { // bottom-up
		for x := 0; x < n; x++ {
			img.Write(px[y*n+x][:])
		}
	}
	img.Write(make([]byte, n*n/8)) // AND mask: all opaque (alpha rules)

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))  // reserved
	binary.Write(&ico, binary.LittleEndian, uint16(1))  // type: icon
	binary.Write(&ico, binary.LittleEndian, uint16(1))  // count
	ico.WriteByte(n)                                    // width
	ico.WriteByte(n)                                    // height
	ico.WriteByte(0)                                    // colors
	ico.WriteByte(0)                                    // reserved
	binary.Write(&ico, binary.LittleEndian, uint16(1))  // planes
	binary.Write(&ico, binary.LittleEndian, uint16(32)) // bpp
	binary.Write(&ico, binary.LittleEndian, uint32(img.Len()))
	binary.Write(&ico, binary.LittleEndian, uint32(6+16)) // offset
	ico.Write(img.Bytes())
	return ico.Bytes()
}
