package main

import (
	"sort"

	"github.com/google/uuid"
)

func roleOf(deviation float64) Role {
	if deviation > RoleThreshold {
		return RoleContributor
	} else if deviation < -RoleThreshold {
		return RoleBeneficiary
	}
	return RoleNeutral
}

type AgentSummary struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	IsBot     bool      `json:"is_bot"`
	Cash      float64   `json:"cash"`
	Equity    float64   `json:"equity"`
	Deviation float64   `json:"deviation"`
	Role      Role      `json:"role"`
}

// Summaries builds the leaderboard sorted by equity, with roles.
func Summaries(ex *Exchange) []AgentSummary {
	marks := ex.Marks()
	total := 0.0
	for _, a := range ex.Agents {
		total += a.Equity(marks)
	}
	mean := 0.0
	if len(ex.Agents) > 0 {
		mean = total / float64(len(ex.Agents))
	}
	rows := make([]AgentSummary, 0, len(ex.Agents))
	for _, a := range ex.Agents {
		equity := a.Equity(marks)
		deviation := 0.0
		if mean > 0.0 {
			deviation = (equity - mean) / mean
		}
		rows = append(rows, AgentSummary{
			ID:        a.ID,
			Name:      a.Name,
			IsBot:     a.IsBot,
			Cash:      a.Cash,
			Equity:    equity,
			Deviation: deviation,
			Role:      roleOf(deviation),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Equity > rows[j].Equity })
	return rows
}

type PositionView struct {
	Symbol   string  `json:"symbol"`
	Qty      int64   `json:"qty"`
	Reserved uint32  `json:"reserved"`
	Free     int64   `json:"free"`
	Mark     float64 `json:"mark"`
	Value    float64 `json:"value"`
}

type AgentDetail struct {
	ID           uuid.UUID      `json:"id"`
	Name         string         `json:"name"`
	IsBot        bool           `json:"is_bot"`
	Cash         float64        `json:"cash"`
	ReservedCash float64        `json:"reserved_cash"`
	FreeCash     float64        `json:"free_cash"`
	Equity       float64        `json:"equity"`
	Role         Role           `json:"role"`
	Mandate      Mandate        `json:"mandate"`
	Positions    []PositionView `json:"positions"`
	OpenOrders   []OrderRecord  `json:"open_orders"`
}

func BuildAgentDetail(ex *Exchange, id uuid.UUID) *AgentDetail {
	marks := ex.Marks()
	cache, ok := ex.Agents[id]
	if !ok {
		return nil
	}

	equity := cache.Equity(marks)
	total := 0.0
	for _, a := range ex.Agents {
		total += a.Equity(marks)
	}
	mean := 0.0
	if len(ex.Agents) > 0 {
		mean = total / float64(len(ex.Agents))
	}
	deviation := 0.0
	if mean > 0.0 {
		deviation = (equity - mean) / mean
	}

	var mandate *Mandate
	for _, m := range ex.Mandates() {
		if m.AgentID == id {
			mm := m
			mandate = &mm
			break
		}
	}
	if mandate == nil {
		return nil
	}

	var positions []PositionView
	for sym, qty := range cache.Positions {
		reserved := cache.ReservedShares[sym]
		if qty == 0 && reserved == 0 {
			continue
		}
		mark := marks[sym]
		free := qty - int64(reserved)
		positions = append(positions, PositionView{
			Symbol:   sym,
			Qty:      qty,
			Reserved: reserved,
			Free:     free,
			Mark:     mark,
			Value:    float64(qty) * mark,
		})
	}

	var openOrders []OrderRecord
	for _, r := range ex.Orders {
		if r.AgentID == id && (r.Status == StatusOpen || r.Status == StatusPartiallyFilled) {
			openOrders = append(openOrders, r)
		}
	}
	sort.Slice(openOrders, func(i, j int) bool { return openOrders[i].ID < openOrders[j].ID })

	return &AgentDetail{
		ID:           cache.ID,
		Name:         cache.Name,
		IsBot:        cache.IsBot,
		Cash:         cache.Cash,
		ReservedCash: cache.ReservedCash,
		FreeCash:     cache.FreeCash(),
		Equity:       equity,
		Role:         roleOf(deviation),
		Mandate:      *mandate,
		Positions:    positions,
		OpenOrders:   openOrders,
	}
}

// ---------------------------------------------------------------------------
// Live WebSocket frame
// ---------------------------------------------------------------------------

// LiveFrame is one push frame of the live feed. Core fields arrive every tick;
// Mandates and History are refreshed on extended frames; Desk is present only
// when the client subscribed with an agent_id.
type LiveFrame struct {
	Type       string            `json:"type"`
	Seq        uint64            `json:"seq"`
	Stocks     []StockView       `json:"stocks"`
	Book       *BookView         `json:"book"`
	Tape       []Trade           `json:"tape"`
	Agents     []AgentSummary    `json:"agents"`
	Welfare    Welfare           `json:"welfare"`
	Tournament *TournamentView   `json:"tournament"`
	Mandates   []Mandate         `json:"mandates,omitempty"`
	History    []WelfareSnapshot `json:"history,omitempty"`
	Desk       *AgentDetail      `json:"desk,omitempty"`
}

func BuildFrame(ex *Exchange, symbol string, agentID *uuid.UUID, extended bool, seq uint64) LiveFrame {
	frame := LiveFrame{
		Type:       "snapshot",
		Seq:        seq,
		Stocks:     ex.StockViews(),
		Book:       ex.BookView(symbol, 10),
		Tape:       ex.Tape(40),
		Agents:     Summaries(ex),
		Welfare:    ex.Welfare(),
		Tournament: ex.ActiveTournamentView(),
		History:    ex.WelfareHistory,
	}
	if extended {
		frame.Mandates = ex.Mandates()
	}
	if agentID != nil {
		frame.Desk = BuildAgentDetail(ex, *agentID)
	}
	return frame
}
