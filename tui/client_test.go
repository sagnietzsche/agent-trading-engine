package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

func TestSessionResetsMetricThenStreamsFrames(t *testing.T) {
	var mu sync.Mutex
	resetCalled := false
	resetBody := map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/reset":
			mu.Lock()
			resetCalled = true
			_ = json.NewDecoder(r.Body).Decode(&resetBody)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"reset complete","metric":"nash"}`)
		case "/api/ws":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "bye")
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			first := true
			for i := 0; i < 5; i++ {
				metric := "nash"
				if first {
					metric = "gini"
					first = false
				}
				payload, _ := json.Marshal(Frame{
					Type:    "snapshot",
					Seq:     uint64(i + 1),
					Stocks:  []StockView{{Symbol: "NOVA", Fair: 184.2}},
					Welfare: Welfare{Metric: metric, Gini: 0.40, GiniTarget: 0.20},
				})
				if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sub := make(chan string, 4)
	announce := make(chan string, 4)
	var framesMu sync.Mutex
	frames := 0
	gotResetStatus := false
	s := newSession(srv.URL, "nash", func(msg tea.Msg) {
		switch msg := msg.(type) {
		case frameMsg:
			framesMu.Lock()
			frames++
			framesMu.Unlock()
			// Frame 1 carries the mismatching metric that triggers the reset;
			// everything after it must reflect the chosen metric.
			if msg.frame.Seq > 1 && msg.frame.Welfare.Metric != "nash" {
				t.Errorf("frame %d metric = %q, want nash", msg.frame.Seq, msg.frame.Welfare.Metric)
			}
		case statusMsg:
			if strings.Contains(msg.text, "reseeding") {
				gotResetStatus = true
			}
		}
	}, sub, announce)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.connectAndStream(ctx) // returns when the server handler finishes

	mu.Lock()
	defer mu.Unlock()
	if !resetCalled {
		t.Fatal("metric mismatch did not trigger a reset")
	}
	if resetBody["metric"] != "nash" {
		t.Fatalf("reset body metric = %q, want nash", resetBody["metric"])
	}
	framesMu.Lock()
	defer framesMu.Unlock()
	if frames != 5 {
		t.Fatalf("frames = %d, want 5", frames)
	}
	if !gotResetStatus {
		t.Fatal("user was never told the market was being reseeded")
	}
}
