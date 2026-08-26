package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	frameIntervalMs = 1000
	extendedEvery   = 3
)

// prefs holds per-connection subscription preferences, mutated by the
// receiver goroutine and read by the sender goroutine.
type prefs struct {
	mu      sync.Mutex
	symbol  string
	agentID *uuid.UUID
}

func newPrefs(symbol string, agentID *uuid.UUID) *prefs {
	if symbol == "" {
		symbol = "NOVA"
	}
	return &prefs{symbol: symbol, agentID: agentID}
}

// wsClientMsg mirrors the client->server protocol messages.
type wsClientMsg struct {
	Type    string  `json:"type"`
	Symbol  *string `json:"symbol"`
	AgentID *string `json:"agent_id"`
}

func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	agentID := (*uuid.UUID)(nil)
	if v := r.URL.Query().Get("agent_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			agentID = &id
		}
	}
	p := newPrefs(r.URL.Query().Get("symbol"), agentID)

	ctx, cancel := context.WithCancel(r.Context())
	writeMu := &sync.Mutex{}
	write := func(msgType websocket.MessageType, payload []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		ctx, cxl := context.WithTimeout(ctx, 5*time.Second)
		defer cxl()
		if err := c.Write(ctx, msgType, payload); err != nil {
			cancel()
		}
	}

	go s.wsSender(ctx, c, p, write, cancel)
	s.wsReceiver(ctx, c, p, write, cancel)
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// wsSender pushes a full snapshot frame every second.
func (s *server) wsSender(ctx context.Context, c *websocket.Conn, p *prefs, write func(websocket.MessageType, []byte), cancel context.CancelFunc) {
	ticker := time.NewTicker(frameIntervalMs * time.Millisecond)
	defer ticker.Stop()
	var seq uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		symbol := p.symbol
		agentID := p.agentID
		p.mu.Unlock()

		seq++
		extended := seq%extendedEvery == 0

		ex := s.ex.lock()
		frame := BuildFrame(ex, symbol, agentID, extended, seq)
		s.ex.unlock()

		payload, err := json.Marshal(frame)
		if err != nil {
			slog.Error("ws frame encode failed", "err", err)
			continue
		}
		write(websocket.MessageText, payload)
	}
}

// wsReceiver handles client messages: subscribe updates prefs + acks, ping
// gets a pong. Protocol-level ping/pong control frames are handled inside
// coder/websocket automatically.
func (s *server) wsReceiver(ctx context.Context, c *websocket.Conn, p *prefs, write func(websocket.MessageType, []byte), cancel context.CancelFunc) {
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var msg wsClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // unknown or malformed messages are ignored
		}
		switch msg.Type {
		case "subscribe":
			p.mu.Lock()
			if msg.Symbol != nil {
				p.symbol = *msg.Symbol
			}
			agentID := p.agentID
			if msg.AgentID != nil {
				if id, err := uuid.Parse(*msg.AgentID); err == nil {
					agentID = &id
				} else {
					agentID = nil
				}
				p.agentID = agentID
			}
			ackSymbol := p.symbol
			p.mu.Unlock()
			ack, _ := json.Marshal(map[string]any{
				"type":     "subscribed",
				"symbol":   ackSymbol,
				"agent_id": agentID,
			})
			write(websocket.MessageText, ack)
		case "ping":
			write(websocket.MessageText, []byte(`{"type":"pong"}`))
		}
	}
}
