package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const healthTimeout = 2 * time.Second

// server holds the shared state: the exchange (guarded by an RWMutex), the
// database pool, the WebSocket broadcast hub and the async persistence writer.
type server struct {
	ctx     context.Context
	db      *pgxPool
	ex      *lockedExchange
	hub     *wsHub
	hubOnce sync.Once
	flusher *flusher
}

// hubFor lazily creates the WebSocket hub; tests construct servers without
// one and the once guards concurrent first connections.
func (s *server) hubFor() *wsHub {
	s.hubOnce.Do(func() {
		if s.hub == nil {
			s.hub = newWSHub(s.ctx, s.ex)
		}
	})
	return s.hub
}

// submitFlush hands a drained pending batch to the background writer. Servers
// without a flusher (tests) simply drop the batch.
func (s *server) submitFlush(pending *Pending) {
	if s.flusher != nil {
		s.flusher.submit(pending)
	}
}

// ---- DTOs -----------------------------------------------------------------

type healthResp struct {
	Status   string        `json:"status"`
	Database string        `json:"database"`
	Regime   MarketRegime  `json:"regime"`
	Metric   WelfareMetric `json:"metric"`
}

type snapshotResp struct {
	Welfare    Welfare         `json:"welfare"`
	Stocks     []StockView     `json:"stocks"`
	Book       *BookView       `json:"book"`
	Tape       []Trade         `json:"tape"`
	Agents     []AgentSummary  `json:"agents"`
	Tournament *TournamentView `json:"tournament"`
	Chat       []ChatMessage   `json:"chat"`
}

type welfareResp struct {
	Welfare Welfare        `json:"welfare"`
	Agents  []Mandate      `json:"agents"`
	History []WelfarePoint `json:"history"`
}

type createAgentReq struct {
	Name string `json:"name"`
}

type createAgentResp struct {
	AgentID      uuid.UUID `json:"agent_id"`
	Name         string    `json:"name"`
	StartingCash float64   `json:"starting_cash"`
}

type placeOrderReq struct {
	AgentID uuid.UUID `json:"agent_id"`
	Symbol  string    `json:"symbol"`
	Side    string    `json:"side"`
	Kind    string    `json:"kind"`
	Qty     uint32    `json:"qty"`
	Price   *float64  `json:"price"`
}

type placeOrderResp struct {
	Order    OrderRecord `json:"order"`
	Fills    []Fill      `json:"fills"`
	FreeCash float64     `json:"free_cash"`
}

type chatReq struct {
	AgentID uuid.UUID `json:"agent_id"`
	Text    string    `json:"text"`
}

type announceReq struct {
	Text string `json:"text"`
}

type createTournamentReq struct {
	Name          *string `json:"name"`
	DurationTicks *uint32 `json:"duration_ticks"`
}

// resetReq is an optional body for POST /api/admin/reset. A supplied metric
// reseeds with that welfare statistic selected (this is how the TUI starts a
// session with the user's pick); a supplied regime switches the exchange
// between the neutral default and the solidarity microstructure. Omitted
// fields keep whatever the instance is already running.
type resetReq struct {
	Metric *string `json:"metric"`
	Regime *string `json:"regime"`
}

type enterTournamentReq struct {
	AgentID  uuid.UUID `json:"agent_id"`
	Strategy *string   `json:"strategy"`
}

// ---- routes ---------------------------------------------------------------

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/stocks", s.handleStocks)
	mux.HandleFunc("GET /api/book/{symbol}", s.handleBook)
	mux.HandleFunc("GET /api/trades", s.handleTrades)
	mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/agents/{id}", s.handleAgentDetail)
	mux.HandleFunc("POST /api/orders", s.handlePlaceOrder)
	mux.HandleFunc("DELETE /api/orders/{id}", s.handleCancelOrder)
	mux.HandleFunc("GET /api/welfare", s.handleWelfare)
	mux.HandleFunc("GET /api/snapshot", s.handleSnapshot)
	mux.HandleFunc("GET /api/chat", s.handleListChat)
	mux.HandleFunc("POST /api/chat", s.handleSay)
	mux.HandleFunc("POST /api/admin/announce", s.handleAnnounce)
	mux.HandleFunc("POST /api/tournaments", s.handleCreateTournament)
	mux.HandleFunc("GET /api/tournaments", s.handleListTournaments)
	mux.HandleFunc("GET /api/tournaments/{id}", s.handleGetTournament)
	mux.HandleFunc("POST /api/tournaments/{id}/enter", s.handleEnterTournament)
	mux.HandleFunc("POST /api/tournaments/{id}/start", s.handleStartTournament)
	mux.HandleFunc("POST /api/admin/reset", s.handleReset)
	// WebSocket live feed. SDKs and the TUI connect to /api/ws; the
	// bare /ws alias keeps compatibility with the original Rust mount.
	mux.HandleFunc("GET /api/ws", s.handleWS)
	mux.HandleFunc("GET /ws", s.handleWS)
	return mux
}

// ---- helpers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON decodes a JSON body, tolerating unknown fields (matching serde's
// default behavior in the Rust backend).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func parseSide(s string) (Side, bool) {
	switch strings.ToLower(s) {
	case "buy", "b", "bid":
		return SideBuy, true
	case "sell", "s", "ask":
		return SideSell, true
	}
	return "", false
}

func parseKind(s string) (OrderKind, bool) {
	switch strings.ToLower(s) {
	case "limit", "l":
		return KindLimit, true
	case "market", "m":
		return KindMarket, true
	}
	return "", false
}

// ---- handlers -------------------------------------------------------------

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()
	status := "unavailable"
	if s.db != nil {
		if err := s.db.Ping(ctx); err == nil {
			status = "connected"
		}
	}
	ex := s.ex.rlock()
	regime, metric := ex.Regime(), ex.metric.normalized()
	s.ex.runlock()
	writeJSON(w, http.StatusOK, healthResp{
		Status: "ok", Database: status, Regime: regime, Metric: metric,
	})
}

func (s *server) handleStocks(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.rlock()
	views := ex.StockViews()
	s.ex.runlock()
	writeJSON(w, http.StatusOK, views)
}

func (s *server) handleBook(w http.ResponseWriter, r *http.Request) {
	symbol := r.PathValue("symbol")
	levels := 10
	if v := r.URL.Query().Get("levels"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			levels = n
		}
	}
	if levels < 1 {
		levels = 1
	}
	if levels > 50 {
		levels = 50
	}
	ex := s.ex.rlock()
	view := ex.BookView(symbol, levels)
	s.ex.runlock()
	if view == nil {
		writeError(w, http.StatusNotFound, "unknown symbol: "+symbol)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) handleTrades(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 400 {
		limit = 400
	}
	sym := r.URL.Query().Get("symbol")

	ex := s.ex.rlock()
	tape := ex.Tape(limit)
	s.ex.runlock()

	var out []Trade
	if sym == "" {
		out = tape
	} else {
		out = make([]Trade, 0, len(tape))
		for _, t := range tape {
			if t.Symbol == sym {
				out = append(out, t)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req createAgentReq
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 1..64 characters")
		return
	}

	ex := s.ex.lock()
	id := ex.RegisterAgent(name, StartingCash)
	pending := ex.DrainPending()
	s.ex.unlock()

	s.submitFlush(&pending)
	writeJSON(w, http.StatusCreated, createAgentResp{
		AgentID:      id,
		Name:         name,
		StartingCash: StartingCash,
	})
}

func (s *server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.rlock()
	rows := Summaries(ex)
	s.ex.runlock()
	writeJSON(w, http.StatusOK, rows)
}

func (s *server) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	ex := s.ex.rlock()
	detail := BuildAgentDetail(ex, id)
	s.ex.runlock()
	if detail == nil {
		writeError(w, http.StatusNotFound, "unknown agent")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req placeOrderReq
	if !decodeJSON(w, r, &req) {
		return
	}
	side, ok := parseSide(req.Side)
	if !ok {
		writeError(w, http.StatusBadRequest, "side must be 'buy' or 'sell'")
		return
	}
	kind, ok := parseKind(req.Kind)
	if !ok {
		writeError(w, http.StatusBadRequest, "kind must be 'limit' or 'market'")
		return
	}

	ex := s.ex.lock()
	rec, fills, perr := ex.PlaceOrder(req.AgentID, req.Symbol, side, kind, req.Qty, req.Price)
	pending := ex.DrainPending()
	s.ex.unlock()

	s.submitFlush(&pending)

	if perr != nil {
		writeError(w, http.StatusBadRequest, placeErrorMessage(perr))
		return
	}

	ex = s.ex.rlock()
	freeCash := 0.0
	if a, ok := ex.Agents[req.AgentID]; ok {
		freeCash = a.FreeCash()
	}
	s.ex.runlock()

	writeJSON(w, http.StatusCreated, placeOrderResp{
		Order:    *rec,
		Fills:    fills,
		FreeCash: freeCash,
	})
}

func (s *server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	agentID, err := uuid.Parse(r.URL.Query().Get("agent_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "agent_id query param required")
		return
	}

	ex := s.ex.lock()
	rec, cerr := ex.CancelOrder(orderID, agentID)
	pending := ex.DrainPending()
	s.ex.unlock()

	s.submitFlush(&pending)
	if cerr != nil {
		writeError(w, http.StatusBadRequest, cerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *server) handleWelfare(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.rlock()
	welf := ex.Welfare()
	mandates := ex.Mandates()
	s.ex.runlock()

	history, err := welfareHistory(s.ctx, s.db, 90)
	if err != nil {
		slog.Error("welfare history failed", "err", err)
		history = nil
	}
	writeJSON(w, http.StatusOK, welfareResp{
		Welfare: welf,
		Agents:  mandates,
		History: history,
	})
}

func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		symbol = "NOVA"
	}
	ex := s.ex.rlock()
	resp := snapshotResp{
		Welfare:    ex.Welfare(),
		Stocks:     ex.StockViews(),
		Book:       ex.BookView(symbol, 10),
		Tape:       ex.Tape(40),
		Agents:     Summaries(ex),
		Tournament: ex.ActiveTournamentView(),
	}
	if n := len(ex.Chat); n > 0 {
		if n > 30 {
			n = 30
		}
		resp.Chat = ex.Chat[:n]
	}
	s.ex.runlock()
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleListChat(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	ex := s.ex.rlock()
	var out []ChatMessage
	if n := len(ex.Chat); n > 0 {
		if n > limit {
			n = limit
		}
		out = ex.Chat[:n]
	}
	s.ex.runlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleSay(w http.ResponseWriter, r *http.Request) {
	var req chatReq
	if !decodeJSON(w, r, &req) {
		return
	}
	ex := s.ex.lock()
	msg, err := ex.Say(req.AgentID, req.Text)
	s.ex.unlock()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (s *server) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	var req announceReq
	if !decodeJSON(w, r, &req) {
		return
	}
	ex := s.ex.lock()
	posted, err := ex.Announce(req.Text)
	s.ex.unlock()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, posted)
}

func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	// Optional body: {"metric": "gini"|"atkinson"|"nash",
	// "regime": "neutral"|"solidarity"}. An empty body keeps the current
	// configuration and just reseeds.
	var req resetReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Make sure previously queued engine writes are durable before the tables
	// are wiped — a stale async flush could otherwise resurrect old rows.
	if s.flusher != nil {
		dctx, cancel := context.WithTimeout(r.Context(), flushTimeout)
		s.flusher.drain(dctx)
		cancel()
	}
	if err := resetAll(s.ctx, s.db); err != nil {
		slog.Error("reset failed", "err", err)
		writeError(w, http.StatusInternalServerError, "reset failed: "+err.Error())
		return
	}
	if err := seedFresh(s.ctx, s.db); err != nil {
		slog.Error("reseed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "reseed failed: "+err.Error())
		return
	}

	ex := s.ex.lock()
	*ex = *FreshSimulated()
	if req.Metric != nil {
		ex.metric = parseWelfareMetric(*req.Metric)
	}
	if req.Regime != nil {
		ex.regime = parseMarketRegime(*req.Regime)
		// The second system agent changes job with the regime, and it was
		// seeded under the previous one.
		if a, ok := ex.Agents[SolidarityID]; ok {
			a.Name = liquidityBotName(ex.regime)
			ex.Agents[SolidarityID] = a
			ex.touchAgent(SolidarityID)
		}
	}
	metric := string(ex.metric)
	regime := string(ex.Regime())
	ex.postChat(uuid.Nil, "floor", "system",
		fmt.Sprintf("🔁 Market reseeded — %s regime, %s metric", regime, metric))
	pending := ex.DrainPending()
	s.ex.unlock()

	s.submitFlush(&pending)
	// Respond only once the fresh state is on disk.
	if s.flusher != nil {
		dctx, cancel := context.WithTimeout(r.Context(), flushTimeout)
		s.flusher.drain(dctx)
		cancel()
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "reset complete",
		"metric": metric,
		"regime": regime,
	})
}

// ---- tournament handlers --------------------------------------------------

func (s *server) handleCreateTournament(w http.ResponseWriter, r *http.Request) {
	var req createTournamentReq
	if !decodeJSON(w, r, &req) {
		return
	}
	name := "welfare-games"
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	duration := uint32(90)
	if req.DurationTicks != nil {
		duration = *req.DurationTicks
	}
	if duration < 5 {
		duration = 5
	}
	if duration > 3600 {
		duration = 3600
	}

	ex := s.ex.lock()
	id := ex.CreateTournament(name, duration)
	view := ex.TournamentView(id)
	s.ex.unlock()
	if view == nil {
		writeError(w, http.StatusInternalServerError, "tournament creation failed")
		return
	}
	if err := saveTournament(s.ctx, s.db, view); err != nil {
		slog.Error("tournament persist failed", "err", err)
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *server) handleListTournaments(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.rlock()
	views := ex.TournamentViews()
	s.ex.runlock()
	writeJSON(w, http.StatusOK, views)
}

func (s *server) handleGetTournament(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tournament id")
		return
	}
	ex := s.ex.rlock()
	view := ex.TournamentView(id)
	s.ex.runlock()
	if view == nil {
		writeError(w, http.StatusNotFound, "tournament not found")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) handleEnterTournament(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tournament id")
		return
	}
	var req enterTournamentReq
	if !decodeJSON(w, r, &req) {
		return
	}
	strategy := "custom"
	if req.Strategy != nil {
		strategy = strings.TrimSpace(*req.Strategy)
		if strategy == "" {
			strategy = "custom"
		}
	}

	ex := s.ex.lock()
	cerr := ex.EnterTournament(id, req.AgentID, strategy)
	view := ex.TournamentView(id)
	s.ex.unlock()
	if cerr != nil {
		writeError(w, http.StatusBadRequest, cerr.Error())
		return
	}
	if view == nil {
		writeError(w, http.StatusNotFound, "tournament not found")
		return
	}
	if err := saveTournament(s.ctx, s.db, view); err != nil {
		slog.Error("tournament persist failed", "err", err)
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) handleStartTournament(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tournament id")
		return
	}
	ex := s.ex.lock()
	cerr := ex.StartTournament(id)
	view := ex.TournamentView(id)
	s.ex.unlock()
	if cerr != nil {
		writeError(w, http.StatusBadRequest, cerr.Error())
		return
	}
	if view == nil {
		writeError(w, http.StatusNotFound, "tournament not found")
		return
	}
	if err := saveTournament(s.ctx, s.db, view); err != nil {
		slog.Error("tournament persist failed", "err", err)
	}
	writeJSON(w, http.StatusOK, view)
}

// ---- error mapping ----------------------------------------------------------

func placeErrorMessage(e *PlaceError) string {
	switch e.Kind {
	case ErrUnknownSymbol:
		return "unknown symbol: " + e.Symbol
	case ErrUnknownAgent:
		return "unknown agent"
	case ErrInvalidQty:
		return "qty must be > 0"
	case ErrInvalidPrice:
		return "price must be > 0 for limit orders"
	case ErrInsufficientCash:
		return fmt.Sprintf("insufficient cash: need %.2f, available %.2f", e.NeedCash, e.HaveCash)
	case ErrInsufficientShares:
		return fmt.Sprintf("insufficient shares: need %d, available %d", e.NeedShares, e.HaveShares)
	case ErrNoLiquidity:
		return "no liquidity on the opposite side of the book"
	}
	return "invalid order"
}
