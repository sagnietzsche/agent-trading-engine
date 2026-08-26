package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

// Frame types mirror the backend's live WebSocket payload (/api/ws). Only the
// fields the dashboard renders are modeled.
type Welfare struct {
	Gini        float64 `json:"gini"`
	Metric      string  `json:"metric"`
	MetricValue float64 `json:"metric_value"`
	TotalEquity float64 `json:"total_equity"`
	MeanEquity  float64 `json:"mean_equity"`
	GiniTarget  float64 `json:"gini_target"`
}

type StockView struct {
	Symbol string   `json:"symbol"`
	Name   string   `json:"name"`
	Fair   float64  `json:"fair"`
	Last   *float64 `json:"last_trade"`
	Prev   float64  `json:"prev_close"`
	Bid    *float64 `json:"bid"`
	Ask    *float64 `json:"ask"`
}

type Level [2]float64

type BookView struct {
	Symbol string  `json:"symbol"`
	Bids   []Level `json:"bids"`
	Asks   []Level `json:"asks"`
}

type Trade struct {
	ID        string  `json:"id"`
	Symbol    string  `json:"symbol"`
	Price     float64 `json:"price"`
	Qty       uint32  `json:"qty"`
	Buyer     string  `json:"buyer"`
	Seller    string  `json:"seller"`
	BuyerEq   float64 `json:"buyer_equity"`
	SellerEq  float64 `json:"seller_equity"`
	GiniAfter float64 `json:"gini_after"`
	TS        string  `json:"ts"`
}

type AgentView struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	IsBot  bool    `json:"is_bot"`
	Cash   float64 `json:"cash"`
	Equity float64 `json:"equity"`
	Dev    float64 `json:"deviation"`
	Role   string  `json:"role"`
}

type WelfarePoint struct {
	Gini        float64 `json:"gini"`
	Metric      string  `json:"metric"`
	MetricValue float64 `json:"metric_value"`
	TotalEquity float64 `json:"total_equity"`
	MeanEquity  float64 `json:"mean_equity"`
	TS          string  `json:"ts"`
}

type Frame struct {
	Type    string         `json:"type"`
	Seq     uint64         `json:"seq"`
	Stocks  []StockView    `json:"stocks"`
	Book    *BookView      `json:"book"`
	Tape    []Trade        `json:"tape"`
	Agents  []AgentView    `json:"agents"`
	Welfare Welfare        `json:"welfare"`
	History []WelfarePoint `json:"history"`
}

// wsBase converts an http(s) base URL into the equivalent ws(s) base.
func wsBase(base string) string {
	u := strings.TrimSuffix(base, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	default:
		return "ws://" + u
	}
}

// session owns one live session against the backend: it dials the WebSocket
// feed, makes sure the server runs with the chosen welfare metric (reseeding
// the market through POST /api/admin/reset when it doesn't), forwards every
// snapshot frame to the bubbletea program, and reconnects on failure.
type session struct {
	base   string
	metric string
	send   func(tea.Msg)
	subCh  chan string
}

func newSession(base, metric string, send func(tea.Msg), subCh chan string) *session {
	return &session{base: base, metric: metric, send: send, subCh: subCh}
}

// run is the session's top-level loop: connect, stream, and on error wait a
// moment and reconnect until ctx is cancelled.
func (s *session) run(ctx context.Context) {
	s.send(statusMsg{text: "connecting…"})
	for {
		err := s.connectAndStream(ctx)
		if ctx.Err() != nil {
			return
		}
		s.send(statusMsg{text: fmt.Sprintf("reconnecting after error: %v", err)})
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *session) connectAndStream(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, wsBase(s.base)+"/api/ws?symbol=NOVA", nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	s.send(statusMsg{text: "connected — starting session"})

	// Symbol switches from the keyboard flow through here.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sym := <-s.subCh:
				payload, _ := json.Marshal(map[string]any{"type": "subscribe", "symbol": sym})
				wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				_ = conn.Write(wctx, websocket.MessageText, payload)
				cancel()
			}
		}
	}()

	resetOnce := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var f Frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		if f.Type != "snapshot" {
			continue
		}
		if !resetOnce && f.Welfare.Metric != "" && f.Welfare.Metric != s.metric {
			resetOnce = true
			s.send(statusMsg{text: fmt.Sprintf(
				"server runs %q — reseeding market with %q…", f.Welfare.Metric, s.metric)})
			if err := s.reset(ctx); err != nil {
				s.send(statusMsg{text: fmt.Sprintf("reset failed: %v", err)})
			}
		}
		s.send(frameMsg{frame: f})
	}
}

// reset reseeds the market with the chosen metric via POST /api/admin/reset.
func (s *session) reset(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"metric": s.metric})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/api/admin/reset", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("reset: %s", e.Error)
	}
	return nil
}
