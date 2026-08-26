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
	sendBuffer      = 16
)

// wsClient is one live-feed connection. Its subscription prefs are mutated by
// the reader goroutine and read by the broadcast loop; frames are handed to
// the writer goroutine over send. The broadcast loop never blocks on a slow
// client — it drops frames instead of stalling the whole feed.
type wsClient struct {
	mu      sync.Mutex
	symbol  string
	agentID *uuid.UUID
	send    chan []byte
}

func newWSClient(symbol string, agentID *uuid.UUID) *wsClient {
	if symbol == "" {
		symbol = "NOVA"
	}
	return &wsClient{
		symbol:  symbol,
		agentID: agentID,
		send:    make(chan []byte, sendBuffer),
	}
}

// wsHub fans one snapshot frame per tick out to every connected client. Frames
// are assembled once per subscribed symbol (plus a per-client desk when the
// client subscribed with an agent_id) and marshaled outside the engine lock,
// so the per-tick cost is O(symbols + desks), not O(clients).
type wsHub struct {
	ctx     context.Context
	ex      *lockedExchange
	mu      sync.Mutex
	clients map[*wsClient]struct{}
	started bool
}

func newWSHub(ctx context.Context, ex *lockedExchange) *wsHub {
	return &wsHub{
		ctx:     ctx,
		ex:      ex,
		clients: map[*wsClient]struct{}{},
	}
}

func (h *wsHub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	if !h.started {
		h.started = true
		go h.loop()
	}
	h.mu.Unlock()
}

func (h *wsHub) remove(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// loop is the single broadcaster: one frame per tick for everyone. It starts
// lazily on the first connection and stops when the server context ends.
func (h *wsHub) loop() {
	ticker := time.NewTicker(frameIntervalMs * time.Millisecond)
	defer ticker.Stop()
	var seq uint64
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
		}
		seq++
		h.broadcastTick(seq, seq%extendedEvery == 0)
	}
}

func (h *wsHub) broadcastTick(seq uint64, extended bool) {
	h.mu.Lock()
	if len(h.clients) == 0 {
		h.mu.Unlock()
		return
	}
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	// Group clients by their subscribed symbol; desk clients get a dedicated
	// frame so each pays for its own detail panel.
	nonDesk := map[string][]*wsClient{}
	type deskSub struct {
		c   *wsClient
		sym string
		ag  uuid.UUID
	}
	var desks []deskSub
	for _, c := range clients {
		c.mu.Lock()
		sym, ag := c.symbol, c.agentID
		c.mu.Unlock()
		if ag != nil {
			desks = append(desks, deskSub{c, sym, *ag})
		} else {
			nonDesk[sym] = append(nonDesk[sym], c)
		}
	}

	// Assemble under the engine's read lock; marshal after releasing it so
	// JSON encoding never blocks exchange writers.
	ex := h.ex.rlock()
	shared := make(map[string][]byte, len(nonDesk))
	for sym := range nonDesk {
		payload, err := json.Marshal(BuildBaseFrame(ex, sym, extended, seq))
		if err != nil {
			slog.Error("ws frame encode failed", "err", err)
			continue
		}
		shared[sym] = payload
	}
	deskPayloads := make(map[*wsClient][]byte, len(desks))
	for _, d := range desks {
		payload, err := json.Marshal(BuildFrame(ex, d.sym, &d.ag, extended, seq))
		if err != nil {
			slog.Error("ws frame encode failed", "err", err)
			continue
		}
		deskPayloads[d.c] = payload
	}
	h.ex.runlock()

	for sym, cs := range nonDesk {
		payload := shared[sym]
		for _, c := range cs {
			h.send(c, payload)
		}
	}
	for _, d := range desks {
		h.send(d.c, deskPayloads[d.c])
	}
}

func (h *wsHub) send(c *wsClient, payload []byte) {
	select {
	case c.send <- payload:
	default:
		// Slow consumer: drop this frame rather than stalling the feed or
		// growing memory without bound.
	}
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
	cl := newWSClient(r.URL.Query().Get("symbol"), agentID)
	s.hubFor().add(cl)

	ctx, cancel := context.WithCancel(r.Context())
	writeMu := &sync.Mutex{}
	write := func(msgType websocket.MessageType, payload []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, cxl := context.WithTimeout(ctx, 5*time.Second)
		defer cxl()
		if err := c.Write(wctx, msgType, payload); err != nil {
			cancel()
		}
	}

	// Writer goroutine: drain the broadcast queue. The reader below drives the
	// lifecycle — when it returns the client is gone, cancel() makes this
	// goroutine exit, and the hub forgets the client.
	go func() {
		defer s.hubFor().remove(cl)
		for {
			select {
			case <-ctx.Done():
				return
			case payload := <-cl.send:
				write(websocket.MessageText, payload)
			}
		}
	}()

	s.wsReader(ctx, c, cl, write, cancel)
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// wsReader handles client messages: subscribe updates prefs + acks, ping gets
// a pong. Protocol-level ping/pong control frames are handled inside
// coder/websocket automatically.
func (s *server) wsReader(ctx context.Context, c *websocket.Conn, cl *wsClient, write func(websocket.MessageType, []byte), cancel context.CancelFunc) {
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
			cl.mu.Lock()
			if msg.Symbol != nil {
				cl.symbol = *msg.Symbol
			}
			agentID := cl.agentID
			if msg.AgentID != nil {
				if id, err := uuid.Parse(*msg.AgentID); err == nil {
					agentID = &id
				} else {
					agentID = nil
				}
				cl.agentID = agentID
			}
			ackSymbol := cl.symbol
			cl.mu.Unlock()
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
