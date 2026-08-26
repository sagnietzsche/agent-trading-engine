package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const healthTimeout = 2 * time.Second

// server holds the shared state: the exchange (guarded by a mutex) and the
// database pool.
type server struct {
	ctx context.Context
	db  *pgxPool
	ex  *lockedExchange
}

// ---- DTOs -----------------------------------------------------------------

type healthResp struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

type snapshotResp struct {
	Welfare    Welfare         `json:"welfare"`
	Stocks     []StockView     `json:"stocks"`
	Book       *BookView       `json:"book"`
	Tape       []Trade         `json:"tape"`
	Agents     []AgentSummary  `json:"agents"`
	Tournament *TournamentView `json:"tournament"`
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

type createTournamentReq struct {
	Name          *string `json:"name"`
	DurationTicks *uint32 `json:"duration_ticks"`
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
	mux.HandleFunc("POST /api/tournaments", s.handleCreateTournament)
	mux.HandleFunc("GET /api/tournaments", s.handleListTournaments)
	mux.HandleFunc("GET /api/tournaments/{id}", s.handleGetTournament)
	mux.HandleFunc("POST /api/tournaments/{id}/enter", s.handleEnterTournament)
	mux.HandleFunc("POST /api/tournaments/{id}/start", s.handleStartTournament)
	mux.HandleFunc("POST /api/admin/reset", s.handleReset)
	// WebSocket live feed. SDKs and the frontend connect to /api/ws; the
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
	status := "connected"
	if err := s.db.Ping(ctx); err != nil {
		status = "unavailable"
	}
	writeJSON(w, http.StatusOK, healthResp{Status: "ok", Database: status})
}

func (s *server) handleStocks(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.lock()
	views := ex.StockViews()
	s.ex.unlock()
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
	ex := s.ex.lock()
	view := ex.BookView(symbol, levels)
	s.ex.unlock()
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

	ex := s.ex.lock()
	tape := ex.Tape(limit)
	s.ex.unlock()

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

	if err := flush(s.ctx, s.db, &pending); err != nil {
		slog.Error("flush failed", "err", err)
	}
	writeJSON(w, http.StatusCreated, createAgentResp{
		AgentID:      id,
		Name:         name,
		StartingCash: StartingCash,
	})
}

func (s *server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.lock()
	rows := Summaries(ex)
	s.ex.unlock()
	writeJSON(w, http.StatusOK, rows)
}

func (s *server) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	ex := s.ex.lock()
	detail := BuildAgentDetail(ex, id)
	s.ex.unlock()
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

	if err := flush(s.ctx, s.db, &pending); err != nil {
		slog.Error("flush failed", "err", err)
	}

	if perr != nil {
		writeError(w, http.StatusBadRequest, placeErrorMessage(perr))
		return
	}

	ex = s.ex.lock()
	freeCash := 0.0
	if a, ok := ex.Agents[req.AgentID]; ok {
		freeCash = a.FreeCash()
	}
	s.ex.unlock()

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

	if err := flush(s.ctx, s.db, &pending); err != nil {
		slog.Error("flush failed", "err", err)
	}
	if cerr != nil {
		writeError(w, http.StatusBadRequest, cerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *server) handleWelfare(w http.ResponseWriter, r *http.Request) {
	ex := s.ex.lock()
	welf := ex.Welfare()
	mandates := ex.Mandates()
	s.ex.unlock()

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
	ex := s.ex.lock()
	resp := snapshotResp{
		Welfare:    ex.Welfare(),
		Stocks:     ex.StockViews(),
		Book:       ex.BookView(symbol, 10),
		Tape:       ex.Tape(40),
		Agents:     Summaries(ex),
		Tournament: ex.ActiveTournamentView(),
	}
	s.ex.unlock()
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
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
	pending := ex.DrainPending()
	s.ex.unlock()

	if err := flush(s.ctx, s.db, &pending); err != nil {
		slog.Error("flush after reset failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset complete"})
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
	ex := s.ex.lock()
	views := ex.TournamentViews()
	s.ex.unlock()
	writeJSON(w, http.StatusOK, views)
}

func (s *server) handleGetTournament(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tournament id")
		return
	}
	ex := s.ex.lock()
	view := ex.TournamentView(id)
	s.ex.unlock()
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
