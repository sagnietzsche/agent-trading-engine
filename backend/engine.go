package main

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Collective-welfare tuning knobs.
//
// The exchange is not neutral: its stated objective is to move every agent
// toward an equal share of total wealth ("from each according to ability,
// to each according to needs"). When measured inequality exceeds
// GiniTarget, surplus agents are nudged into giving trades. Which inequality
// statistic is used is an instance-level choice — see WelfareMetric and the
// WELFARE_METRIC env var (or the TUI's session picker).
const (
	GiniTarget      = 0.20
	RoleThreshold   = 0.10
	GiftRate        = 0.05 // fraction of wealth gap offered per mandate
	MaxTape         = 400
	WelfareHistCap  = 180
	MaxPrice        = 1_000_000.0
	StartingCash    = 100_000.0
	TournamentRetW  = 1.0
	TournamentCoopW = 1.0
	ChatCap         = 200 // in-memory chat log (ephemeral, like the tape)
)

// WelfareMetric selects the collective-welfare statistic an exchange instance
// optimizes around. Each one is summarized as an inequality index in [0,1]
// (0 = perfectly equal shares, higher = more unequal), which is what the
// solidarity machinery compares against GiniTarget.
type WelfareMetric string

const (
	MetricGini     WelfareMetric = "gini"     // Gini coefficient (default)
	MetricAtkinson WelfareMetric = "atkinson" // Atkinson index, ε = 0.5
	MetricNash     WelfareMetric = "nash"     // Nash social welfare (geometric mean)
)

// parseWelfareMetric maps user input (env var, TUI picker, reset body) to a
// metric; anything unrecognized falls back to the default.
func parseWelfareMetric(s string) WelfareMetric {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "atkinson", "atk":
		return MetricAtkinson
	case "nash", "nsw":
		return MetricNash
	default:
		return MetricGini
	}
}

// welfareMetricFromEnv returns the metric configured for this process via the
// WELFARE_METRIC env var (default gini).
func welfareMetricFromEnv() WelfareMetric {
	return parseWelfareMetric(os.Getenv("WELFARE_METRIC"))
}

// normalized resolves the zero value to the default metric.
func (m WelfareMetric) normalized() WelfareMetric {
	if m == "" {
		return MetricGini
	}
	return m
}

// MarketRegime selects the microstructure an exchange instance runs under.
// It is instance-level configuration (MARKET_REGIME env var, or the optional
// "regime" field on POST /api/admin/reset), not persisted state.
//
//   - RegimeNeutral (default) is a conventional exchange: strict price-time
//     priority, no mandates, tournaments scored purely on return. Agents are
//     the only decision-makers; the venue expresses no preference about who
//     should end up with what.
//   - RegimeSolidarity turns the collective-welfare machinery back on: giving
//     mandates, need-priority matching for solidarity orders, and a
//     cooperation term in the tournament score.
type MarketRegime string

const (
	RegimeNeutral    MarketRegime = "neutral"
	RegimeSolidarity MarketRegime = "solidarity"
)

// parseMarketRegime maps user input (env var, reset body) to a regime;
// anything unrecognized falls back to the neutral default.
func parseMarketRegime(s string) MarketRegime {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "solidarity", "welfare", "cooperative", "coop":
		return RegimeSolidarity
	default:
		return RegimeNeutral
	}
}

// marketRegimeFromEnv returns the regime configured for this process via the
// MARKET_REGIME env var (default neutral).
func marketRegimeFromEnv() MarketRegime {
	return parseMarketRegime(os.Getenv("MARKET_REGIME"))
}

// normalized resolves the zero value to the default regime.
func (r MarketRegime) normalized() MarketRegime {
	if r == "" {
		return RegimeNeutral
	}
	return r
}

// liquidityBotName is the display name of the second system agent, whose job
// changes with the regime: passive depth on a neutral exchange, the
// redistribution desk under solidarity.
func liquidityBotName(r MarketRegime) string {
	if r.normalized() == RegimeSolidarity {
		return "solidarity_bot"
	}
	return "depth_bot"
}

var (
	MarketMakerID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	SolidarityID  = uuid.MustParse("00000000-0000-0000-0000-000000000002")
)

// (symbol, company name, base price)
var Listings = [][3]any{
	{"NOVA", "Nova Dynamics", 184.20},
	{"QNTM", "Quantum Foundry", 92.75},
	{"HELX", "Helix Biolabs", 341.10},
	{"DRCT", "Direct Commons", 47.55},
	{"ORBT", "Orbital Logistics", 128.40},
	{"ZEPH", "Zephyr Energy", 63.90},
}

// tournamentScore ranks a tournament entry. A neutral exchange scores pure
// risk-taking skill; the solidarity regime adds the cooperation term.
func (ex *Exchange) tournamentScore(returnPct, coopShare float64) float64 {
	if !ex.solidarityEnabled() {
		return TournamentRetW * returnPct
	}
	return TournamentRetW*returnPct + TournamentCoopW*coopShare
}

// Side ---------------------------------------------------------------------

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

func (s Side) Opposite() Side {
	if s == SideBuy {
		return SideSell
	}
	return SideBuy
}

type OrderKind string

const (
	KindLimit  OrderKind = "limit"
	KindMarket OrderKind = "market"
)

type Status string

const (
	StatusOpen            Status = "open"
	StatusFilled          Status = "filled"
	StatusPartiallyFilled Status = "partially_filled"
	StatusCancelled       Status = "cancelled"
)

// Fill ---------------------------------------------------------------------

type Fill struct {
	TradeID string  `json:"trade_id"`
	Price   float64 `json:"price"`
	Qty     uint32  `json:"qty"`
}

// RestingOrder -------------------------------------------------------------

type RestingOrder struct {
	id        uint64
	agentID   uuid.UUID
	side      Side
	price     float64
	remaining uint32
}

// key implements price-time priority (id doubles as time).
func (o RestingOrder) key() bookKey {
	return bookKeyFor(o.side, o.price, o.id)
}

type bookKey struct{ rank, id uint64 }

func bookKeyFor(side Side, price float64, id uint64) bookKey {
	ticks := uint64(math.Max(math.Round(price*100.0), 0))
	if side == SideBuy {
		// bids: higher price first, then older first
		return bookKey{math.MaxUint64 - ticks, id}
	}
	// asks: lower price first, then older first
	return bookKey{ticks, id}
}

// Book: price-time priority order book for one symbol.
type Book struct {
	orders []RestingOrder
}

func (b *Book) best(side Side) *float64 {
	var best *bookKey
	var price float64
	for _, o := range b.orders {
		if o.side != side {
			continue
		}
		k := o.key()
		if best == nil || k.rank < best.rank || (k.rank == best.rank && k.id < best.id) {
			kk := k
			best = &kk
			price = o.price
		}
	}
	if best == nil {
		return nil
	}
	return &price
}

func (b *Book) BestBid() *float64 { return b.best(SideBuy) }
func (b *Book) BestAsk() *float64 { return b.best(SideSell) }

func (b *Book) insert(order RestingOrder) {
	b.orders = append(b.orders, order)
}

func (b *Book) reduce(id uint64, qty uint32) {
	for i := range b.orders {
		if b.orders[i].id == id {
			if b.orders[i].remaining > qty {
				b.orders[i].remaining -= qty
			} else {
				b.orders[i].remaining = 0
			}
			break
		}
	}
	kept := b.orders[:0]
	for _, o := range b.orders {
		if o.remaining > 0 {
			kept = append(kept, o)
		}
	}
	b.orders = kept
}

func (b *Book) remove(id uint64) *RestingOrder {
	for i, o := range b.orders {
		if o.id == id {
			b.orders = append(b.orders[:i], b.orders[i+1:]...)
			return &o
		}
	}
	return nil
}

func (b *Book) removeAllOfAgent(agentID uuid.UUID) []RestingOrder {
	var removed []RestingOrder
	kept := b.orders[:0]
	for _, o := range b.orders {
		if o.agentID == agentID {
			removed = append(removed, o)
		} else {
			kept = append(kept, o)
		}
	}
	b.orders = kept
	return removed
}

// Level is a [price, qty] pair that serializes as a JSON array, matching the
// Rust (f64, u32) tuples.
type Level [2]float64

// Depth aggregates size per price level, best-first.
func (b *Book) depth(side Side, levels int) []Level {
	sorted := make([]RestingOrder, 0, len(b.orders))
	for _, o := range b.orders {
		if o.side == side {
			sorted = append(sorted, o)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		ki, kj := sorted[i].key(), sorted[j].key()
		if ki.rank != kj.rank {
			return ki.rank < kj.rank
		}
		return ki.id < kj.id
	})
	out := make([]Level, 0, levels)
	for _, o := range sorted {
		if n := len(out); n > 0 && out[n-1][0] == o.price {
			out[n-1][1] += float64(o.remaining)
			continue
		}
		if len(out) == levels {
			break
		}
		out = append(out, Level{o.price, float64(o.remaining)})
	}
	return out
}

// AgentCache ---------------------------------------------------------------

type AgentCache struct {
	ID             uuid.UUID
	Name           string
	IsBot          bool
	Cash           float64
	ReservedCash   float64
	Positions      map[string]int64
	ReservedShares map[string]uint32
}

func (a *AgentCache) FreeCash() float64 { return a.Cash - a.ReservedCash }

func (a *AgentCache) FreeShares(symbol string) int64 {
	return a.Positions[symbol] - int64(a.ReservedShares[symbol])
}

func (a *AgentCache) Equity(marks map[string]float64) float64 {
	holdings := 0.0
	for sym, qty := range a.Positions {
		holdings += float64(qty) * marks[sym]
	}
	return a.Cash + holdings
}

// Trade --------------------------------------------------------------------

type Trade struct {
	ID           string    `json:"id"`
	Symbol       string    `json:"symbol"`
	Price        float64   `json:"price"`
	Qty          uint32    `json:"qty"`
	Buyer        uuid.UUID `json:"buyer"`
	Seller       uuid.UUID `json:"seller"`
	TakerOrder   uint64    `json:"taker_order"`
	BuyerEquity  float64   `json:"buyer_equity"`
	SellerEquity float64   `json:"seller_equity"`
	GiniAfter    float64   `json:"gini_after"`
	TS           string    `json:"ts"`
}

type StockInfo struct {
	Symbol    string
	Name      string
	Fair      float64
	LastTrade *float64
	PrevClose float64
}

type SymbolState struct {
	Info StockInfo
	Book Book
}

type OrderRecord struct {
	ID        uint64    `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Symbol    string    `json:"symbol"`
	Side      Side      `json:"side"`
	Kind      OrderKind `json:"kind"`
	Price     *float64  `json:"price"`
	Qty       uint32    `json:"qty"`
	Filled    uint32    `json:"filled"`
	Status    Status    `json:"status"`
	CreatedAt string    `json:"created_at"`
}

// ChatMessage is one line in the floor chatroom: system agents write when
// they act on instructions (mandates, requotes, tournaments), any agent can
// post via POST /api/chat, and the TUI monitors the stream. Ephemeral and
// in-memory — like the tape, chat is a session artifact, not part of the
// durable ledger.
type ChatMessage struct {
	ID     string    `json:"id"`
	Author uuid.UUID `json:"author"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"` // system | mandate | market | chat
	Text   string    `json:"text"`
	TS     string    `json:"ts"`
}

type WelfareSnapshot struct {
	Gini        float64       `json:"gini"`
	Metric      WelfareMetric `json:"metric"`
	MetricValue float64       `json:"metric_value"`
	TotalEquity float64       `json:"total_equity"`
	MeanEquity  float64       `json:"mean_equity"`
	TS          string        `json:"ts"`
}

// Pending ------------------------------------------------------------------

type PosKey struct {
	AgentID uuid.UUID
	Symbol  string
}

// Everything mutated since the last drain; flushed to Postgres by store.go.
type Pending struct {
	Agents               map[uuid.UUID]AgentCache
	Positions            map[PosKey]int64
	Orders               map[uint64]OrderRecord
	Trades               []Trade
	Snapshots            []WelfareSnapshot
	TournamentsFinalized []TournamentView
}

func newPending() Pending {
	return Pending{
		Agents:    map[uuid.UUID]AgentCache{},
		Positions: map[PosKey]int64{},
		Orders:    map[uint64]OrderRecord{},
	}
}

func (p *Pending) isEmpty() bool {
	return len(p.Agents) == 0 && len(p.Positions) == 0 && len(p.Orders) == 0 &&
		len(p.Trades) == 0 && len(p.Snapshots) == 0 && len(p.TournamentsFinalized) == 0
}

// Welfare ------------------------------------------------------------------

type Role string

const (
	RoleContributor Role = "contributor"
	RoleBeneficiary Role = "beneficiary"
	RoleNeutral     Role = "neutral"
)

type Suggestion struct {
	Symbol    string  `json:"symbol"`
	Side      Side    `json:"side"`
	Qty       uint32  `json:"qty"`
	Limit     float64 `json:"limit"`
	Rationale string  `json:"rationale"`
}

type Mandate struct {
	AgentID    uuid.UUID   `json:"agent_id"`
	Name       string      `json:"name"`
	Equity     float64     `json:"equity"`
	Deviation  float64     `json:"deviation"`
	Role       Role        `json:"role"`
	Suggestion *Suggestion `json:"suggestion,omitempty"`
}

// Welfare is the inequality read-out of the exchange. It is computed under
// every regime — a neutral venue still reports how concentrated wealth is,
// it just does nothing about it — so Regime tells consumers whether the
// mandate machinery behind these numbers is actually live.
type Welfare struct {
	Gini        float64       `json:"gini"`
	Metric      WelfareMetric `json:"metric"`
	MetricValue float64       `json:"metric_value"`
	TotalEquity float64       `json:"total_equity"`
	MeanEquity  float64       `json:"mean_equity"`
	GiniTarget  float64       `json:"gini_target"`
	Regime      MarketRegime  `json:"regime"`
}

// Gini coefficient over agent equities: 0 = perfectly equal, 1 = one agent
// owns everything.
func gini(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0.0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	total := 0.0
	for _, x := range sorted {
		total += x
	}
	if total <= 0.0 {
		return 0.0
	}
	weighted := 0.0
	for i, x := range sorted {
		weighted += float64(2*(i+1)-n-1) * x
	}
	return math.Max(weighted/(float64(n)*total), 0.0)
}

// atkinsonEpsilon is the inequality-aversion parameter of the Atkinson index.
// Smaller ε weights the poorest members of the distribution more heavily.
const atkinsonEpsilon = 0.5

// atkinsonIndex computes A_ε = 1 − [(1/n)Σᵢ (xᵢ/μ)^(1−ε)]^(1/(1−ε)) for ε ≠ 1.
// Like Gini it lives in [0,1] with 0 = perfect equality, but the Atkinson
// index is more sensitive to the bottom of the distribution: giving the
// poorest agent a dollar reduces it more than the same dollar to a rich one.
func atkinsonIndex(values []float64, epsilon float64) float64 {
	n := len(values)
	if n < 2 {
		return 0.0
	}
	total := 0.0
	for _, x := range values {
		total += x
	}
	if total <= 0.0 {
		return 0.0
	}
	mean := total / float64(n)
	sum := 0.0
	for _, x := range values {
		// Clamp negatives to zero: a non-positive member pushes the index
		// toward 1 (max inequality) instead of producing NaN.
		v := math.Max(x, 0.0)
		sum += math.Pow(v/mean, 1.0-epsilon)
	}
	return 1.0 - math.Pow(sum/float64(n), 1.0/(1.0-epsilon))
}

// nashSocialWelfare returns the geometric mean of agent equities — the Nash
// social welfare, i.e. the per-capita product maximized by an egalitarian
// planner. A single non-positive member collapses the product to 0.
func nashSocialWelfare(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0.0
	}
	logSum := 0.0
	for _, x := range values {
		if x <= 0.0 {
			return 0.0
		}
		logSum += math.Log(x)
	}
	return math.Exp(logSum / float64(n))
}

// nashDeficit normalizes Nash social welfare into the inequality index the
// engine drives on: 1 − GM/mean ∈ [0,1), with 0 = every member holds the
// mean share (the AM–GM inequality guarantees GM ≤ mean).
func nashDeficit(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0.0
	}
	total := 0.0
	for _, x := range values {
		total += x
	}
	if total <= 0.0 {
		return 0.0
	}
	gm := nashSocialWelfare(values)
	if gm <= 0.0 {
		return 1.0
	}
	return 1.0 - gm/(total/float64(n))
}

// inequality summarizes a distribution with the instance's chosen metric,
// always expressed as an index in [0,1] where 0 = perfectly equal. This is
// the number stored in Welfare.Gini (and trades' gini_after) regardless of
// which metric produced it.
func inequality(values []float64, m WelfareMetric) float64 {
	switch m.normalized() {
	case MetricAtkinson:
		return atkinsonIndex(values, atkinsonEpsilon)
	case MetricNash:
		return nashDeficit(values)
	default:
		return gini(values)
	}
}

// metricValue returns the headline value of the chosen metric itself: the
// Atkinson index / Gini coefficient for those metrics, or the raw Nash
// social welfare (geometric mean of equities) for Nash.
func metricValue(values []float64, m WelfareMetric, ineq float64) float64 {
	if m.normalized() == MetricNash {
		return nashSocialWelfare(values)
	}
	return ineq
}

func roundCents(p float64) float64 {
	return math.Round(p*100.0) / 100.0
}

// Tournaments --------------------------------------------------------------

type TournamentStatus string

const (
	TStatusOpen     TournamentStatus = "open"
	TStatusRunning  TournamentStatus = "running"
	TStatusFinished TournamentStatus = "finished"
)

type TournamentEntry struct {
	AgentID         uuid.UUID
	Strategy        string
	StartEquity     float64
	TotalVolume     uint64
	ProsocialVolume uint64
}

type Tournament struct {
	ID            uuid.UUID
	Name          string
	Status        TournamentStatus
	DurationTicks uint32
	TicksLeft     uint32
	GiniStart     float64
	GiniFinal     *float64
	Entries       map[uuid.UUID]TournamentEntry
	CreatedAt     string
	StartedAt     *string
	FinishedAt    *string
}

type TournamentEntryView struct {
	AgentID         uuid.UUID `json:"agent_id"`
	Strategy        string    `json:"strategy"`
	StartEquity     float64   `json:"start_equity"`
	EquityNow       float64   `json:"equity_now"`
	ReturnPct       float64   `json:"return_pct"`
	TotalVolume     uint64    `json:"total_volume"`
	ProsocialVolume uint64    `json:"prosocial_volume"`
	CoopShare       float64   `json:"coop_share"`
	Score           float64   `json:"score"`
}

type TournamentView struct {
	ID            uuid.UUID             `json:"id"`
	Name          string                `json:"name"`
	Status        TournamentStatus      `json:"status"`
	DurationTicks uint32                `json:"duration_ticks"`
	TicksLeft     uint32                `json:"ticks_left"`
	GiniStart     float64               `json:"gini_start"`
	GiniFinal     *float64              `json:"gini_final"`
	CreatedAt     string                `json:"created_at"`
	StartedAt     *string               `json:"started_at"`
	FinishedAt    *string               `json:"finished_at"`
	Entries       []TournamentEntryView `json:"entries"`
}

// PlaceError ---------------------------------------------------------------

type PlaceErrorKind int

const (
	ErrUnknownSymbol PlaceErrorKind = iota
	ErrUnknownAgent
	ErrInvalidQty
	ErrInvalidPrice
	ErrInsufficientCash
	ErrInsufficientShares
	ErrNoLiquidity
)

type PlaceError struct {
	Kind       PlaceErrorKind
	Symbol     string
	NeedCash   float64
	HaveCash   float64
	NeedShares uint32
	HaveShares uint32
}

func (e *PlaceError) Error() string { return "place order failed" }

// RestoreState: snapshot fed back into the engine at boot so books/accounts
// survive restarts.
type RestoreState struct {
	Stocks      []StockInfo
	Agents      []AgentCache
	OpenOrders  []OrderRecord
	Tournaments []Tournament
	Metric      WelfareMetric
	Regime      MarketRegime
	NextOrderID uint64
}

// Exchange -----------------------------------------------------------------

type Exchange struct {
	Symbols        []SymbolState
	bySymbol       map[string]int
	Agents         map[uuid.UUID]AgentCache
	Trades         []Trade // newest first (front-pushed, capped)
	Orders         map[uint64]OrderRecord
	Chat           []ChatMessage // newest first (front-pushed, capped)
	nextOrderID    uint64
	rng            *rand.Rand
	pending        Pending
	Tournaments    []Tournament
	WelfareHistory []WelfareSnapshot
	metric         WelfareMetric
	regime         MarketRegime
}

// solidarityEnabled reports whether the collective-welfare machinery is live
// on this instance. Everything welfare-flavoured — mandates, need-priority
// matching, the cooperation term in tournament scores, the redistribution
// bot — hangs off this one predicate.
func (ex *Exchange) solidarityEnabled() bool {
	return ex.regime.normalized() == RegimeSolidarity
}

// Regime reports the microstructure this instance runs under.
func (ex *Exchange) Regime() MarketRegime { return ex.regime.normalized() }

func newRNG() *rand.Rand {
	var seed [16]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		// crypto/rand failure is effectively unrecoverable; fall back to a
		// time-based seed rather than panicking the server.
		ts := uint64(time.Now().UnixNano())
		return rand.New(rand.NewPCG(ts, ts^0x9e3779b97f4a7c15))
	}
	s1 := binary.LittleEndian.Uint64(seed[0:8])
	s2 := binary.LittleEndian.Uint64(seed[8:16])
	return rand.New(rand.NewPCG(s1, s2))
}

// ExchangeOption tweaks a freshly built exchange (e.g. WithMetric).
type ExchangeOption func(*Exchange)

// WithMetric selects the welfare metric this exchange instance optimizes
// around. Without it an instance defaults to MetricGini.
func WithMetric(m WelfareMetric) ExchangeOption {
	return func(ex *Exchange) { ex.metric = m.normalized() }
}

// WithRegime selects the microstructure this exchange runs under. Without it
// an instance defaults to RegimeNeutral — a conventional exchange.
func WithRegime(r MarketRegime) ExchangeOption {
	return func(ex *Exchange) { ex.regime = r.normalized() }
}

func NewExchange(listings [][3]any, opts ...ExchangeOption) *Exchange {
	ex := &Exchange{
		Symbols:     make([]SymbolState, 0, len(listings)),
		bySymbol:    map[string]int{},
		Agents:      map[uuid.UUID]AgentCache{},
		Orders:      map[uint64]OrderRecord{},
		nextOrderID: 1,
		rng:         newRNG(),
		pending:     newPending(),
		metric:      MetricGini,
		regime:      RegimeNeutral,
	}
	for _, opt := range opts {
		opt(ex)
	}
	for _, l := range listings {
		symbol := l[0].(string)
		name := l[1].(string)
		base := l[2].(float64)
		ex.bySymbol[symbol] = len(ex.Symbols)
		ex.Symbols = append(ex.Symbols, SymbolState{
			Info: StockInfo{
				Symbol:    symbol,
				Name:      name,
				Fair:      base,
				LastTrade: nil,
				PrevClose: base,
			},
			Book: Book{},
		})
	}
	return ex
}

// freshSimulated returns a brand-new exchange under an explicit configuration:
// listings, system agents, one opening tick so the books are live, and the
// opening bell on the floor.
func freshSimulated(metric WelfareMetric, regime MarketRegime) *Exchange {
	ex := NewExchange(Listings, WithMetric(metric), WithRegime(regime))
	ex.SeedSystemAgents()
	ex.SimTick()
	ex.postChat(uuid.Nil, "floor", "system", fmt.Sprintf(
		"🔔 Opening bell — %s regime, %s metric. %d listings, books are live.",
		ex.Regime(), ex.metric.normalized(), len(ex.Symbols)))
	return ex
}

// FreshSimulated is freshSimulated() configured from the process environment:
// the WELFARE_METRIC and MARKET_REGIME env vars (defaults: gini, neutral).
func FreshSimulated() *Exchange {
	return freshSimulated(welfareMetricFromEnv(), marketRegimeFromEnv())
}

// Restore rebuilds in-memory state from Postgres rows (called once at startup).
func Restore(state RestoreState) *Exchange {
	ex := &Exchange{
		Symbols:     []SymbolState{},
		bySymbol:    map[string]int{},
		Agents:      map[uuid.UUID]AgentCache{},
		Orders:      map[uint64]OrderRecord{},
		nextOrderID: state.NextOrderID,
		rng:         newRNG(),
		pending:     newPending(),
		metric:      state.Metric.normalized(),
		regime:      state.Regime.normalized(),
	}
	for _, info := range state.Stocks {
		ex.bySymbol[info.Symbol] = len(ex.Symbols)
		ex.Symbols = append(ex.Symbols, SymbolState{Info: info, Book: Book{}})
	}
	for _, agent := range state.Agents {
		ex.Agents[agent.ID] = agent
	}
	ex.Tournaments = state.Tournaments
	// Rebuild reservations strictly from non-bot resting orders; bot quotes
	// are requoted fresh on the first tick.
	opens := append([]OrderRecord(nil), state.OpenOrders...)
	sort.Slice(opens, func(i, j int) bool { return opens[i].ID < opens[j].ID })
	for _, rec := range opens {
		if rec.Kind != KindLimit || rec.Price == nil {
			continue
		}
		remaining := rec.Qty - rec.Filled
		if remaining == 0 {
			continue
		}
		isBot := false
		if a, ok := ex.Agents[rec.AgentID]; ok {
			isBot = a.IsBot
		}
		if !isBot {
			switch rec.Side {
			case SideBuy:
				if a, ok := ex.Agents[rec.AgentID]; ok {
					a.ReservedCash += roundCents(*rec.Price * float64(remaining))
					ex.Agents[rec.AgentID] = a
				}
			case SideSell:
				if a, ok := ex.Agents[rec.AgentID]; ok {
					a.ReservedShares[rec.Symbol] += remaining
					ex.Agents[rec.AgentID] = a
				}
			}
		}
		idx := ex.bySymbol[rec.Symbol]
		ex.Symbols[idx].Book.insert(RestingOrder{
			id:        rec.ID,
			agentID:   rec.AgentID,
			side:      rec.Side,
			price:     *rec.Price,
			remaining: remaining,
		})
		ex.Orders[rec.ID] = rec
	}
	return ex
}

func (ex *Exchange) RegisterAgent(name string, cash float64) uuid.UUID {
	id := uuid.New()
	cache := AgentCache{
		ID:             id,
		Name:           name,
		IsBot:          false,
		Cash:           cash,
		ReservedCash:   0.0,
		Positions:      map[string]int64{},
		ReservedShares: map[string]uint32{},
	}
	ex.pending.Agents[id] = cache
	ex.Agents[id] = cache
	ex.postChat(id, name, "system", "🎉 "+name+" joined the floor")
	return id
}

// ---- chatroom ------------------------------------------------------------

// postChat appends a message to the floor chat (newest first, capped). Callers
// hold the engine lock; chat is ephemeral and never enters the Pending buffer.
func (ex *Exchange) postChat(author uuid.UUID, name, kind, text string) {
	msg := ChatMessage{
		ID:     uuid.New().String(),
		Author: author,
		Name:   name,
		Kind:   kind,
		Text:   text,
		TS:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	ex.Chat = append([]ChatMessage{msg}, ex.Chat...)
	if len(ex.Chat) > ChatCap {
		ex.Chat = ex.Chat[:ChatCap]
	}
}

// Say lets any registered agent post a chat message (e.g. an SDK bot reporting
// what it did after being instructed).
func (ex *Exchange) Say(agentID uuid.UUID, text string) (*ChatMessage, error) {
	a, ok := ex.Agents[agentID]
	if !ok {
		return nil, fmt.Errorf("unknown agent")
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 280 {
		return nil, fmt.Errorf("message must be 1..280 characters")
	}
	ex.postChat(agentID, a.Name, "chat", text)
	return &ex.Chat[0], nil
}

// Announce broadcasts an instruction to the floor (system message) and lets
// the system agents answer it — the "tell the bots something, watch them
// write" loop. Returns the messages just posted, newest first.
func (ex *Exchange) Announce(text string) ([]ChatMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 280 {
		return nil, fmt.Errorf("announcement must be 1..280 characters")
	}
	var posted []ChatMessage
	ex.postChat(uuid.Nil, "floor", "system", "📢 "+text)
	posted = append(posted, ex.Chat[0])
	for _, reply := range ex.botReplies(text) {
		ex.postChat(reply.agent, reply.name, "chat", reply.text)
		posted = append(posted, ex.Chat[0])
	}
	return posted, nil
}

// botReplies is a tiny scripted instruction->reply table for the two system
// agents. It keeps the chatroom alive: the user says something, the bots
// answer with their role in mind.
type botReply struct {
	agent uuid.UUID
	name  string
	text  string
}

func (ex *Exchange) botReplies(text string) []botReply {
	low := strings.ToLower(text)
	bot := liquidityBotName(ex.regime)
	var out []botReply
	switch {
	case strings.Contains(low, "give"), strings.Contains(low, "share"),
		strings.Contains(low, "help"), strings.Contains(low, "redistribut"),
		strings.Contains(low, "solidarity"):
		if ex.solidarityEnabled() {
			out = append(out, botReply{SolidarityID, bot, "✊ On it — routing a gift to the worst-off members now."})
		} else {
			out = append(out, botReply{SolidarityID, bot, "This venue is neutral — I only rest size. What you do with it is your call."})
		}
	case strings.Contains(low, "volatil"), strings.Contains(low, "panic"),
		strings.Contains(low, "crash"), strings.Contains(low, "spread"),
		strings.Contains(low, "halt"):
		out = append(out, botReply{MarketMakerID, "market_maker", "⚠ Widening the book and trimming size — protecting the spread."})
	case strings.Contains(low, "hello"), strings.Contains(low, "hi"),
		strings.Contains(low, "greeting"), strings.Contains(low, "hey"):
		if ex.solidarityEnabled() {
			out = append(out, botReply{SolidarityID, bot, "Greetings, comrade. I hold surplus to distribute whenever inequality calls."})
		} else {
			out = append(out, botReply{SolidarityID, bot, "Hello. Depth is resting five ticks out on both sides if you need it."})
		}
	case strings.Contains(low, "tournament"), strings.Contains(low, "compete"), strings.Contains(low, "winner"):
		out = append(out, botReply{MarketMakerID, "market_maker", "I don't compete — I quote both sides of every book. Good luck to the entrants."})
	default:
		out = append(out, botReply{MarketMakerID, "market_maker", "Noted. Staying two-sided — call me if you need liquidity."})
	}
	return out
}

func (ex *Exchange) UpsertAgentCache(cache AgentCache) {
	for sym := range cache.Positions {
		ex.pending.Positions[PosKey{cache.ID, sym}] = 0
	}
	ex.touchAgent(cache.ID)
	for sym, qty := range cache.Positions {
		ex.pending.Positions[PosKey{cache.ID, sym}] = qty
	}
	ex.Agents[cache.ID] = cache
}

func (ex *Exchange) idxOf(symbol string) (int, bool) {
	i, ok := ex.bySymbol[symbol]
	return i, ok
}

// Marks: last trade price, falling back to best bid, then fair value.
func (ex *Exchange) Marks() map[string]float64 {
	marks := make(map[string]float64, len(ex.Symbols))
	for i := range ex.Symbols {
		s := &ex.Symbols[i]
		mark := s.Info.Fair
		if b := s.Book.BestBid(); b != nil {
			mark = *b
		}
		if s.Info.LastTrade != nil {
			mark = *s.Info.LastTrade
		}
		marks[s.Info.Symbol] = mark
	}
	return marks
}

// Welfare ------------------------------------------------------------------

func (ex *Exchange) Welfare() Welfare {
	marks := ex.Marks()
	eqs := make([]float64, 0, len(ex.Agents))
	total := 0.0
	for _, a := range ex.Agents {
		e := a.Equity(marks)
		eqs = append(eqs, e)
		total += e
	}
	mean := 0.0
	if len(eqs) > 0 {
		mean = total / float64(len(eqs))
	}
	ineq := inequality(eqs, ex.metric)
	return Welfare{
		Gini:        ineq,
		Metric:      ex.metric.normalized(),
		Regime:      ex.regime.normalized(),
		MetricValue: metricValue(eqs, ex.metric, ineq),
		TotalEquity: total,
		MeanEquity:  mean,
		GiniTarget:  GiniTarget,
	}
}

// Mandates is the solidarity regime's advisory layer: it tells every agent
// what trade would most reduce inequality right now. A neutral exchange has
// no opinion about that, so it issues none — agents decide on their own.
func (ex *Exchange) Mandates() []Mandate {
	if !ex.solidarityEnabled() {
		return nil
	}
	marks := ex.Marks()
	eqs := map[uuid.UUID]float64{}
	total := 0.0
	for _, a := range ex.Agents {
		e := a.Equity(marks)
		eqs[a.ID] = e
		total += e
	}
	mean := 0.0
	if len(eqs) > 0 {
		mean = total / float64(len(eqs))
	}

	out := make([]Mandate, 0, len(ex.Agents))
	for _, agent := range ex.Agents {
		equity := eqs[agent.ID]
		deviation := 0.0
		if mean > 0.0 {
			deviation = (equity - mean) / mean
		}
		role := RoleNeutral
		if deviation > RoleThreshold {
			role = RoleContributor
		} else if deviation < -RoleThreshold {
			role = RoleBeneficiary
		}
		out = append(out, Mandate{
			AgentID:    agent.ID,
			Name:       agent.Name,
			Equity:     equity,
			Deviation:  deviation,
			Role:       role,
			Suggestion: ex.suggest(&agent, equity-mean, mean, role),
		})
	}
	return out
}

func (ex *Exchange) suggest(agent *AgentCache, gap, mean float64, role Role) *Suggestion {
	devPct := 0.0
	if mean > 0.0 {
		devPct = gap / mean * 100.0
	}
	switch role {
	case RoleNeutral:
		return nil
	case RoleContributor:
		// Give away inventory from the largest holding, priced at the bid so
		// it crosses instantly — the concession is the gift.
		var symbol string
		var held int64
		found := false
		for s, q := range agent.Positions {
			if !found || q > held {
				symbol, held, found = s, q, true
			}
		}
		if !found {
			return nil
		}
		free := agent.FreeShares(symbol)
		if free > held {
			free = held // min(free, held), matching the Rust free_shares().min(held)
		}
		if free < 1 {
			return nil
		}
		idx, ok := ex.idxOf(symbol)
		if !ok {
			return nil
		}
		s := &ex.Symbols[idx]
		price := s.Info.Fair
		if b := s.Book.BestBid(); b != nil {
			price = *b
		}
		if price <= 0.0 {
			return nil
		}
		qty := uint32(math.Abs(gap)*GiftRate/price) + 0 // floor, then clamp
		if qty < 1 {
			qty = 1
		}
		if qty > uint32(free) {
			qty = uint32(free)
		}
		return &Suggestion{
			Symbol: symbol,
			Side:   SideSell,
			Qty:    qty,
			Limit:  price,
			Rationale: fmt.Sprintf(
				"You hold %+.1f%% vs the mean. Selling %d %s at the bid transfers value to members below the mean.",
				devPct, qty, s.Info.Symbol,
			),
		}
	case RoleBeneficiary:
		// Use a slice of the shortfall to acquire assets at the ask.
		var bestSym string
		var bestAsk float64
		bestTight := 0.0
		found := false
		for i := range ex.Symbols {
			s := &ex.Symbols[i]
			if ask := s.Book.BestAsk(); ask != nil {
				tightness := math.Abs(*ask - s.Info.Fair)
				if !found || tightness < bestTight {
					bestSym, bestAsk, found = s.Info.Symbol, *ask, true
					bestTight = tightness
				}
			}
		}
		if !found {
			return nil
		}
		budget := math.Abs(gap) * GiftRate
		if fc := agent.FreeCash(); fc < budget {
			budget = fc
		}
		if budget < bestAsk {
			return nil
		}
		qty := uint32(budget / bestAsk)
		if qty == 0 {
			return nil
		}
		return &Suggestion{
			Symbol: bestSym,
			Side:   SideBuy,
			Qty:    qty,
			Limit:  bestAsk,
			Rationale: fmt.Sprintf(
				"You are %.1f%% below the mean. Buying %d %s brings you closer to the collective optimum.",
				devPct, qty, bestSym,
			),
		}
	}
	return nil
}

// Tournaments --------------------------------------------------------------

func (ex *Exchange) CreateTournament(name string, durationTicks uint32) uuid.UUID {
	id := uuid.New()
	ex.Tournaments = append(ex.Tournaments, Tournament{
		ID:            id,
		Name:          name,
		Status:        TStatusOpen,
		DurationTicks: durationTicks,
		TicksLeft:     durationTicks,
		GiniStart:     0.0,
		GiniFinal:     nil,
		Entries:       map[uuid.UUID]TournamentEntry{},
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		StartedAt:     nil,
		FinishedAt:    nil,
	})
	return id
}

func (ex *Exchange) EnterTournament(tournamentID, agentID uuid.UUID, strategy string) error {
	if _, ok := ex.Agents[agentID]; !ok {
		return fmt.Errorf("unknown agent")
	}
	var t *Tournament
	for i := range ex.Tournaments {
		if ex.Tournaments[i].ID == tournamentID {
			t = &ex.Tournaments[i]
			break
		}
	}
	if t == nil {
		return fmt.Errorf("tournament not found")
	}
	if t.Status != TStatusOpen {
		return fmt.Errorf("tournament is %s", t.Status)
	}
	if _, ok := t.Entries[agentID]; ok {
		return fmt.Errorf("agent already entered")
	}
	startEquity := 0.0
	if a, ok := ex.Agents[agentID]; ok {
		startEquity = a.Equity(ex.Marks())
	}
	t.Entries[agentID] = TournamentEntry{
		AgentID:     agentID,
		Strategy:    strategy,
		StartEquity: startEquity,
	}
	return nil
}

func (ex *Exchange) StartTournament(tournamentID uuid.UUID) error {
	var t *Tournament
	for i := range ex.Tournaments {
		if ex.Tournaments[i].ID == tournamentID {
			t = &ex.Tournaments[i]
			break
		}
	}
	if t == nil {
		return fmt.Errorf("tournament not found")
	}
	if t.Status != TStatusOpen {
		return fmt.Errorf("tournament is %s", t.Status)
	}
	t.Status = TStatusRunning
	t.TicksLeft = t.DurationTicks
	now := time.Now().UTC().Format(time.RFC3339Nano)
	t.StartedAt = &now

	// Everyone's baseline is captured at the gun, not at signup.
	marks := ex.Marks()
	t.GiniStart = ex.Welfare().Gini
	for id, e := range t.Entries {
		if a, ok := ex.Agents[id]; ok {
			e.StartEquity = a.Equity(marks)
			t.Entries[id] = e
		}
	}
	ex.postChat(uuid.Nil, "floor", "system",
		fmt.Sprintf("🏁 Tournament '%s' started — %d entrant(s) locked in", t.Name, len(t.Entries)))
	return nil
}

func (ex *Exchange) TournamentView(tournamentID uuid.UUID) *TournamentView {
	var t *Tournament
	for i := range ex.Tournaments {
		if ex.Tournaments[i].ID == tournamentID {
			t = &ex.Tournaments[i]
			break
		}
	}
	if t == nil {
		return nil
	}
	marks := ex.Marks()
	entries := make([]TournamentEntryView, 0, len(t.Entries))
	for _, e := range t.Entries {
		equityNow := e.StartEquity
		if a, ok := ex.Agents[e.AgentID]; ok {
			equityNow = a.Equity(marks)
		}
		returnPct := 0.0
		if e.StartEquity > 0.0 {
			returnPct = equityNow/e.StartEquity - 1.0
		}
		coopShare := 0.0
		if e.TotalVolume > 0 {
			coopShare = float64(e.ProsocialVolume) / float64(e.TotalVolume)
		}
		entries = append(entries, TournamentEntryView{
			AgentID:         e.AgentID,
			Strategy:        e.Strategy,
			StartEquity:     e.StartEquity,
			EquityNow:       equityNow,
			ReturnPct:       returnPct,
			TotalVolume:     e.TotalVolume,
			ProsocialVolume: e.ProsocialVolume,
			CoopShare:       coopShare,
			Score:           ex.tournamentScore(returnPct, coopShare),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})
	return &TournamentView{
		ID:            t.ID,
		Name:          t.Name,
		Status:        t.Status,
		DurationTicks: t.DurationTicks,
		TicksLeft:     t.TicksLeft,
		GiniStart:     t.GiniStart,
		GiniFinal:     t.GiniFinal,
		CreatedAt:     t.CreatedAt,
		StartedAt:     t.StartedAt,
		FinishedAt:    t.FinishedAt,
		Entries:       entries,
	}
}

func (ex *Exchange) TournamentViews() []TournamentView {
	views := make([]TournamentView, 0, len(ex.Tournaments))
	for _, t := range ex.Tournaments {
		if v := ex.TournamentView(t.ID); v != nil {
			views = append(views, *v)
		}
	}
	// Most relevant first: running, open, then newest finished.
	sort.SliceStable(views, func(i, j int) bool {
		return statusRank(views[i].Status) < statusRank(views[j].Status)
	})
	return views
}

func statusRank(s TournamentStatus) int {
	switch s {
	case TStatusRunning:
		return 0
	case TStatusOpen:
		return 1
	default:
		return 2
	}
}

// ActiveTournamentView returns the tournament currently worth showing on the
// wire (running first).
func (ex *Exchange) ActiveTournamentView() *TournamentView {
	views := ex.TournamentViews()
	if len(views) == 0 {
		return nil
	}
	return &views[0]
}

// Pending ------------------------------------------------------------------

func (ex *Exchange) DrainPending() Pending {
	p := ex.pending
	ex.pending = newPending()
	return p
}

func (ex *Exchange) touchAgent(id uuid.UUID) {
	if cache, ok := ex.Agents[id]; ok {
		ex.pending.Agents[id] = cache
	}
}

func (ex *Exchange) touchPosition(id uuid.UUID, symbol string) {
	qty := int64(0)
	if a, ok := ex.Agents[id]; ok {
		qty = a.Positions[symbol]
	}
	ex.pending.Positions[PosKey{id, symbol}] = qty
}

func (ex *Exchange) record(rec OrderRecord) {
	ex.pending.Orders[rec.ID] = rec
	ex.Orders[rec.ID] = rec
}

// Order placement -----------------------------------------------------------

// PlaceOrder places an order with plain price-time priority.
func (ex *Exchange) PlaceOrder(agentID uuid.UUID, symbol string, side Side, kind OrderKind, qty uint32, price *float64) (*OrderRecord, []Fill, *PlaceError) {
	return ex.placeOrderInner(agentID, symbol, side, kind, qty, price, false)
}

// PlaceSolidarityOrder places an order marked for redistribution. Under the
// solidarity regime the matcher routes it to beneficiary counterparties
// first; on a neutral exchange the flag is inert and the order gets plain
// price-time priority like everyone else's.
func (ex *Exchange) PlaceSolidarityOrder(agentID uuid.UUID, symbol string, side Side, kind OrderKind, qty uint32, price *float64) (*OrderRecord, []Fill, *PlaceError) {
	return ex.placeOrderInner(agentID, symbol, side, kind, qty, price, true)
}

func (ex *Exchange) placeOrderInner(agentID uuid.UUID, symbol string, side Side, kind OrderKind, qty uint32, price *float64, solidarity bool) (*OrderRecord, []Fill, *PlaceError) {
	agent, ok := ex.Agents[agentID]
	if !ok {
		return nil, nil, &PlaceError{Kind: ErrUnknownAgent}
	}
	if qty == 0 {
		return nil, nil, &PlaceError{Kind: ErrInvalidQty}
	}
	idx, ok := ex.idxOf(symbol)
	if !ok {
		return nil, nil, &PlaceError{Kind: ErrUnknownSymbol, Symbol: symbol}
	}

	var limitPrice *float64
	switch kind {
	case KindLimit:
		if price == nil || !(*price > 0.0 && *price <= MaxPrice) {
			return nil, nil, &PlaceError{Kind: ErrInvalidPrice}
		}
		p := roundCents(*price)
		limitPrice = &p
	case KindMarket:
		limitPrice = nil
	}

	// Reserve resources up-front so no agent can promise what it lacks.
	var cashReserve *float64
	switch side {
	case SideBuy:
		var cost float64
		if limitPrice != nil {
			cost = roundCents(*limitPrice * float64(qty))
		} else {
			ask := ex.Symbols[idx].Book.BestAsk()
			if ask == nil {
				return nil, nil, &PlaceError{Kind: ErrNoLiquidity}
			}
			cost = roundCents(*ask * float64(qty) * 1.001) // slippage buffer
		}
		if agent.FreeCash()+1e-9 < cost {
			return nil, nil, &PlaceError{
				Kind:     ErrInsufficientCash,
				NeedCash: cost,
				HaveCash: roundCents(agent.FreeCash()),
			}
		}
		a := ex.Agents[agentID]
		a.ReservedCash += cost
		ex.Agents[agentID] = a
		c := cost
		cashReserve = &c
	case SideSell:
		// System liquidity agents (market maker, solidarity bot) may quote
		// without inventory. Human agents must own what they promise to sell.
		if !agent.IsBot {
			free := agent.FreeShares(symbol)
			if free < int64(qty) {
				have := uint32(0)
				if free > 0 {
					have = uint32(free)
				}
				return nil, nil, &PlaceError{
					Kind:       ErrInsufficientShares,
					NeedShares: qty,
					HaveShares: have,
				}
			}
			a := ex.Agents[agentID]
			a.ReservedShares[symbol] += qty
			ex.Agents[agentID] = a
		}
	}

	id := ex.nextOrderID
	ex.nextOrderID++
	record := OrderRecord{
		ID:        id,
		AgentID:   agentID,
		Symbol:    symbol,
		Side:      side,
		Kind:      kind,
		Price:     limitPrice,
		Qty:       qty,
		Filled:    0,
		Status:    StatusOpen,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	fills := ex.execute(idx, id, agentID, side, qty, limitPrice, solidarity)
	record.Filled = 0
	for _, f := range fills {
		record.Filled += f.Qty
	}

	resting := kind == KindLimit && record.Filled < qty
	switch {
	case record.Filled == qty:
		record.Status = StatusFilled
	case resting:
		if record.Filled > 0 {
			record.Status = StatusPartiallyFilled
		} else {
			record.Status = StatusOpen
		}
	case record.Filled > 0:
		record.Status = StatusPartiallyFilled
	default:
		record.Status = StatusCancelled
	}

	if resting {
		l := *limitPrice // resting implies limit
		ex.Symbols[idx].Book.insert(RestingOrder{
			id:        id,
			agentID:   agentID,
			side:      side,
			price:     l,
			remaining: qty - record.Filled,
		})
	}

	// Release whatever part of the reservation is no longer needed.
	a := ex.Agents[agentID]
	switch side {
	case SideBuy:
		keep := 0.0
		if resting {
			keep = roundCents(*limitPrice * float64(qty-record.Filled))
		}
		release := math.Max(*cashReserve-keep, 0.0)
		a.ReservedCash = math.Max(a.ReservedCash-release, 0.0)
	case SideSell:
		if !a.IsBot {
			keep := uint32(0)
			if resting {
				keep = qty - record.Filled
			}
			res := a.ReservedShares[symbol]
			sub := qty - keep
			if res > sub {
				a.ReservedShares[symbol] = res - sub
			} else {
				a.ReservedShares[symbol] = 0
			}
		}
	}
	ex.Agents[agentID] = a

	ex.touchAgent(agentID)
	ex.record(record)
	return &record, fills, nil
}

func (ex *Exchange) execute(idx int, takerOrderID uint64, taker uuid.UUID, side Side, qty uint32, limit *float64, solidarity bool) []Fill {
	// For solidarity orders, pre-compute who currently sits below the
	// threshold so their resting orders get matched first.
	var beneficiaryIDs map[uuid.UUID]struct{}
	if solidarity && ex.solidarityEnabled() {
		marks := ex.Marks()
		total := 0.0
		for _, a := range ex.Agents {
			total += a.Equity(marks)
		}
		mean := 0.0
		if len(ex.Agents) > 0 {
			mean = total / float64(len(ex.Agents))
		}
		beneficiaryIDs = map[uuid.UUID]struct{}{}
		for _, a := range ex.Agents {
			if mean > 0.0 && (a.Equity(marks)-mean)/mean < -RoleThreshold {
				beneficiaryIDs[a.ID] = struct{}{}
			}
		}
	}

	type candidate struct {
		id      uint64
		price   float64
		agentID uuid.UUID
		qty     uint32
	}

	var fills []Fill
	for qty > 0 {
		var candidates []candidate
		book := &ex.Symbols[idx].Book
		for _, o := range book.orders {
			if o.side == side || o.agentID == taker {
				continue // same side, or no wash trades even for charity
			}
			if limit != nil {
				if side == SideBuy && o.price > *limit {
					continue
				}
				if side == SideSell && o.price < *limit {
					continue
				}
			}
			fillQty := o.remaining
			if fillQty > qty {
				fillQty = qty
			}
			candidates = append(candidates, candidate{o.id, o.price, o.agentID, fillQty})
		}
		if len(candidates) == 0 {
			break
		}

		makerSide := side.Opposite()
		pick := func(pool []candidate) *candidate {
			var best *candidate
			for i := range pool {
				c := &pool[i]
				if best == nil || lessByBookKey(makerSide, c.price, c.id, best.price, best.id) {
					best = c
				}
			}
			return best
		}
		// Need-priority routing: solidarity orders help the worst-off members
		// first; everyone else gets plain price-time priority.
		var counterparty *candidate
		if beneficiaryIDs != nil {
			helped := make([]candidate, 0, len(candidates))
			for _, c := range candidates {
				if _, needy := beneficiaryIDs[c.agentID]; needy {
					helped = append(helped, c)
				}
			}
			if len(helped) > 0 {
				counterparty = pick(helped)
			} else {
				counterparty = pick(candidates)
			}
		} else {
			counterparty = pick(candidates)
		}
		if counterparty == nil {
			break
		}

		symbol := ex.Symbols[idx].Info.Symbol
		fillQty := counterparty.qty

		// Release the maker's reservation for the filled amount (system
		// liquidity agents run unhedged and hold none).
		if a, ok := ex.Agents[counterparty.agentID]; ok && !a.IsBot {
			makerIsBuyer := side == SideSell
			if makerIsBuyer {
				a.ReservedCash = math.Max(a.ReservedCash-roundCents(counterparty.price*float64(fillQty)), 0.0)
			} else {
				res := a.ReservedShares[symbol]
				if res > fillQty {
					a.ReservedShares[symbol] = res - fillQty
				} else {
					a.ReservedShares[symbol] = 0
				}
			}
			ex.Agents[counterparty.agentID] = a
		}

		// Equity context for the welfare ledger, captured pre-settlement.
		marks := ex.Marks()
		buyerID, sellerID := taker, counterparty.agentID
		if side == SideSell {
			buyerID, sellerID = counterparty.agentID, taker
		}
		buyerEq := 0.0
		if a, ok := ex.Agents[buyerID]; ok {
			buyerEq = a.Equity(marks)
		}
		sellerEq := 0.0
		if a, ok := ex.Agents[sellerID]; ok {
			sellerEq = a.Equity(marks)
		}

		ex.Symbols[idx].Book.reduce(counterparty.id, fillQty)

		// Keep the maker's persisted order record in sync with passive fills.
		if mrec, ok := ex.Orders[counterparty.id]; ok {
			mrec.Filled += fillQty
			if mrec.Filled >= mrec.Qty {
				mrec.Status = StatusFilled
			} else {
				mrec.Status = StatusPartiallyFilled
			}
			ex.Orders[counterparty.id] = mrec
			ex.pending.Orders[counterparty.id] = mrec
		}

		// Settlement: value moves from buyer to seller, shares the other way.
		cost := roundCents(counterparty.price * float64(fillQty))
		if a, ok := ex.Agents[buyerID]; ok {
			a.Cash -= cost
			a.Positions[symbol] += int64(fillQty)
			ex.Agents[buyerID] = a
		}
		if a, ok := ex.Agents[sellerID]; ok {
			a.Cash += cost
			a.Positions[symbol] -= int64(fillQty)
			ex.Agents[sellerID] = a
		}
		ex.touchAgent(buyerID)
		ex.touchAgent(sellerID)
		ex.touchPosition(buyerID, symbol)
		ex.touchPosition(sellerID, symbol)

		// Tournament attribution: a fill is prosocial for whichever
		// counterparty is wealthier.
		richerIsBuyer := buyerEq > sellerEq
		richerIsSeller := sellerEq > buyerEq
		for i := range ex.Tournaments {
			t := &ex.Tournaments[i]
			if t.Status != TStatusRunning {
				continue
			}
			qty64 := uint64(fillQty)
			for _, party := range []uuid.UUID{buyerID, sellerID} {
				if e, ok := t.Entries[party]; ok {
					e.TotalVolume += qty64
					if (party == buyerID && richerIsBuyer) || (party == sellerID && richerIsSeller) {
						e.ProsocialVolume += qty64
					}
					t.Entries[party] = e
				}
			}
		}

		giniNow := func() float64 {
			eqs := make([]float64, 0, len(ex.Agents))
			for _, a := range ex.Agents {
				eqs = append(eqs, a.Equity(marks))
			}
			return inequality(eqs, ex.metric)
		}()

		trade := Trade{
			ID:           uuid.New().String(),
			Symbol:       symbol,
			Price:        counterparty.price,
			Qty:          fillQty,
			Buyer:        buyerID,
			Seller:       sellerID,
			TakerOrder:   takerOrderID,
			BuyerEquity:  buyerEq,
			SellerEquity: sellerEq,
			GiniAfter:    giniNow,
			TS:           time.Now().UTC().Format(time.RFC3339Nano),
		}
		ex.Symbols[idx].Info.LastTrade = &trade.Price
		ex.Trades = append([]Trade{trade}, ex.Trades...)
		if len(ex.Trades) > MaxTape {
			ex.Trades = ex.Trades[:MaxTape]
		}
		ex.pending.Trades = append(ex.pending.Trades, trade)
		fills = append(fills, Fill{TradeID: trade.ID, Price: counterparty.price, Qty: fillQty})
		qty -= fillQty
	}
	return fills
}

func lessByBookKey(side Side, p1 float64, id1 uint64, p2 float64, id2 uint64) bool {
	k1, k2 := bookKeyFor(side, p1, id1), bookKeyFor(side, p2, id2)
	if k1.rank != k2.rank {
		return k1.rank < k2.rank
	}
	return k1.id < k2.id
}

func (ex *Exchange) CancelOrder(orderID uint64, agentID uuid.UUID) (*OrderRecord, error) {
	rec, ok := ex.Orders[orderID]
	if !ok {
		return nil, fmt.Errorf("order not found")
	}
	if rec.AgentID != agentID {
		return nil, fmt.Errorf("order does not belong to agent")
	}
	if rec.Status != StatusOpen && rec.Status != StatusPartiallyFilled {
		return nil, fmt.Errorf("order is not cancellable")
	}
	idx, ok := ex.idxOf(rec.Symbol)
	if !ok {
		return nil, fmt.Errorf("unknown symbol")
	}
	removed := ex.Symbols[idx].Book.remove(orderID)
	if removed == nil {
		return nil, fmt.Errorf("order not resting")
	}

	remaining := removed.remaining
	a := ex.Agents[agentID]
	switch rec.Side {
	case SideBuy:
		l := 0.0
		if rec.Price != nil {
			l = *rec.Price
		}
		a.ReservedCash = math.Max(a.ReservedCash-roundCents(l*float64(remaining)), 0.0)
	case SideSell:
		res := a.ReservedShares[rec.Symbol]
		if res > remaining {
			a.ReservedShares[rec.Symbol] = res - remaining
		} else {
			a.ReservedShares[rec.Symbol] = 0
		}
	}
	ex.Agents[agentID] = a

	rec.Status = StatusCancelled
	ex.touchAgent(agentID)
	ex.record(rec)
	return &rec, nil
}

// CancelAllForAgent pulls all resting quotes for one agent (used to requote
// market makers).
func (ex *Exchange) CancelAllForAgent(agentID uuid.UUID) {
	for idx := range ex.Symbols {
		symbol := ex.Symbols[idx].Info.Symbol
		for _, removed := range ex.Symbols[idx].Book.removeAllOfAgent(agentID) {
			if rec, ok := ex.Orders[removed.id]; ok {
				rec.Status = StatusCancelled
				ex.Orders[removed.id] = rec
				ex.pending.Orders[removed.id] = rec
			}
			if a, ok := ex.Agents[agentID]; ok && !a.IsBot {
				switch removed.side {
				case SideBuy:
					a.ReservedCash = math.Max(a.ReservedCash-roundCents(removed.price*float64(removed.remaining)), 0.0)
				case SideSell:
					res := a.ReservedShares[symbol]
					if res > removed.remaining {
						a.ReservedShares[symbol] = res - removed.remaining
					} else {
						a.ReservedShares[symbol] = 0
					}
				}
				ex.Agents[agentID] = a
			}
		}
	}
	ex.touchAgent(agentID)
}

// Simulation ---------------------------------------------------------------

// SeedSystemAgents seeds the two system agents. The solidarity bot starts
// wealthy on purpose: watching it give that wealth away is the point.
func (ex *Exchange) SeedSystemAgents() {
	ex.Agents[MarketMakerID] = AgentCache{
		ID:             MarketMakerID,
		Name:           "market_maker",
		IsBot:          true,
		Cash:           10_000_000.0,
		ReservedCash:   0.0,
		Positions:      map[string]int64{},
		ReservedShares: map[string]uint32{},
	}
	inv := map[string]int64{}
	for _, s := range ex.Symbols {
		inv[s.Info.Symbol] = 40_000
	}
	ex.Agents[SolidarityID] = AgentCache{
		ID:             SolidarityID,
		Name:           liquidityBotName(ex.regime),
		IsBot:          true,
		Cash:           6_000_000.0,
		ReservedCash:   0.0,
		Positions:      inv,
		ReservedShares: map[string]uint32{},
	}
	for _, id := range []uuid.UUID{MarketMakerID, SolidarityID} {
		ex.touchAgent(id)
		for _, s := range ex.Symbols {
			ex.touchPosition(id, s.Info.Symbol)
		}
	}
}

// quoteDepthTick is the neutral regime's use for the second system agent: a
// patient size provider resting wide of the market maker. It never crosses
// the spread, so it adds depth for agents to trade against without steering
// price — the venue stays a passive counterparty of last resort.
func (ex *Exchange) quoteDepthTick() {
	for idx := range ex.Symbols {
		fair := ex.Symbols[idx].Info.Fair
		spread := math.Max(fair*0.0015, 0.01)
		sym := ex.Symbols[idx].Info.Symbol
		for level := 5; level <= 9; level += 2 {
			size := uint32(ex.rng.IntN(300) + 200) // 200..499
			bid := roundCents(fair - spread*float64(level))
			ask := roundCents(fair + spread*float64(level))
			ex.PlaceOrder(SolidarityID, sym, SideBuy, KindLimit, size, &bid)
			ex.PlaceOrder(SolidarityID, sym, SideSell, KindLimit, size, &ask)
		}
	}
}

// redistributeTick is the solidarity regime's use for the same agent: while
// inequality sits above target it executes its own giving mandate as a
// solidarity order, which the matcher routes to the worst-off first.
func (ex *Exchange) redistributeTick(w Welfare) {
	if w.Gini <= GiniTarget {
		return
	}
	for _, m := range ex.Mandates() {
		if m.AgentID != SolidarityID || m.Suggestion == nil {
			continue
		}
		qty := m.Suggestion.Qty
		if qty > 500 {
			qty = 500
		}
		_, fills, perr := ex.PlaceSolidarityOrder(
			SolidarityID,
			m.Suggestion.Symbol,
			m.Suggestion.Side,
			KindLimit,
			qty,
			&m.Suggestion.Limit,
		)
		if perr == nil && len(fills) > 0 {
			sold := 0
			for _, f := range fills {
				sold += int(f.Qty)
			}
			ex.postChat(SolidarityID, liquidityBotName(ex.regime), "mandate",
				fmt.Sprintf("✊ Giving %d %s to the bids of the worst-off — %d shares, mandate fulfilled.", sold, m.Suggestion.Symbol, sold))
		}
		return
	}
}

func (ex *Exchange) SimTick() {
	// 1. Random walk fair values.
	prevFair := make([]float64, len(ex.Symbols))
	for i := range ex.Symbols {
		prevFair[i] = ex.Symbols[i].Info.Fair
		g := ex.rng.Float64()*2 - 1 // uniform in [-1, 1)
		shock := g * g * g * 3.0
		drift := -0.0015 + ex.rng.Float64()*0.0035 // uniform in [-0.0015, 0.002)
		fair := ex.Symbols[i].Info.Fair * (1.0 + drift + shock*0.004)
		if fair < 1.0 {
			fair = 1.0
		} else if fair > 100_000.0 {
			fair = 100_000.0
		}
		ex.Symbols[i].Info.Fair = fair
	}

	// 2. Neutral market maker provides liquid, tight two-sided markets.
	ex.CancelAllForAgent(MarketMakerID)
	for idx := range ex.Symbols {
		fair := ex.Symbols[idx].Info.Fair
		spread := math.Max(fair*0.0015, 0.01)
		for level := 1; level <= 3; level++ {
			size := uint32(ex.rng.IntN(70) + 20) // 20..89
			bid := roundCents(fair - spread*float64(level))
			ask := roundCents(fair + spread*float64(level))
			sym := ex.Symbols[idx].Info.Symbol
			ex.PlaceOrder(MarketMakerID, sym, SideBuy, KindLimit, size, &bid)
			ex.PlaceOrder(MarketMakerID, sym, SideSell, KindLimit, size, &ask)
		}
	}

	ex.CancelAllForAgent(SolidarityID)

	// The market maker comments when a tick moves the market meaningfully.
	{
		var worstSym string
		worstPct := 0.0
		for i := range ex.Symbols {
			if prevFair[i] <= 0.0 {
				continue
			}
			pct := math.Abs(ex.Symbols[i].Info.Fair/prevFair[i] - 1.0)
			if pct > worstPct {
				worstPct, worstSym = pct, ex.Symbols[i].Info.Symbol
			}
		}
		if worstPct > 0.015 {
			sign := "rose"
			if ex.Symbols[ex.bySymbol[worstSym]].Info.Fair < prevFair[ex.bySymbol[worstSym]] {
				sign = "fell"
			}
			ex.postChat(MarketMakerID, "market_maker", "market",
				fmt.Sprintf("⚠ %s %s %+.2f%% this tick — widening the book", worstSym, sign, worstPct*100))
		}
	}

	// 3. The second system agent's job depends on the regime.
	w := ex.Welfare()
	if ex.solidarityEnabled() {
		ex.redistributeTick(w)
	} else {
		ex.quoteDepthTick()
	}

	// 4. Advance tournaments; finalize any that ran out of ticks.
	var finishedIDs []uuid.UUID
	for i := range ex.Tournaments {
		t := &ex.Tournaments[i]
		if t.Status == TStatusRunning {
			if t.TicksLeft > 0 {
				t.TicksLeft--
			}
			if t.TicksLeft == 0 {
				finishedIDs = append(finishedIDs, t.ID)
			}
		}
	}
	if len(finishedIDs) > 0 {
		giniFinal := ex.Welfare().Gini
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for i := range ex.Tournaments {
			t := &ex.Tournaments[i]
			for _, id := range finishedIDs {
				if t.ID == id {
					t.Status = TStatusFinished
					t.GiniFinal = &giniFinal
					t.FinishedAt = &now
				}
			}
		}
		for _, id := range finishedIDs {
			if v := ex.TournamentView(id); v != nil {
				logTournamentResult(v)
				ex.pending.TournamentsFinalized = append(ex.pending.TournamentsFinalized, *v)
				ex.postChat(uuid.Nil, "floor", "system",
					fmt.Sprintf("🏁 Tournament '%s' finished", v.Name))
			}
		}
	}

	// 5. Record the welfare trend.
	w = ex.Welfare()
	snap := WelfareSnapshot{
		Gini:        w.Gini,
		Metric:      w.Metric,
		MetricValue: w.MetricValue,
		TotalEquity: w.TotalEquity,
		MeanEquity:  w.MeanEquity,
		TS:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	ex.pending.Snapshots = append(ex.pending.Snapshots, snap)
	ex.WelfareHistory = append(ex.WelfareHistory, snap)
	if len(ex.WelfareHistory) > WelfareHistCap {
		ex.WelfareHistory = ex.WelfareHistory[len(ex.WelfareHistory)-WelfareHistCap:]
	}
}

func logTournamentResult(v *TournamentView) {
	if len(v.Entries) == 0 {
		logInfo("tournament '%s' finished with no entries", v.Name)
		return
	}
	w := v.Entries[0]
	logInfo(
		"tournament '%s' finished — winner %s (%s) score %.4f (ret %+.4f, coop %.2f)",
		v.Name, w.AgentID, w.Strategy, w.Score, w.ReturnPct, w.CoopShare,
	)
}

// Read models --------------------------------------------------------------

type StockView struct {
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name"`
	Fair      float64  `json:"fair"`
	LastTrade *float64 `json:"last_trade"`
	PrevClose float64  `json:"prev_close"`
	Bid       *float64 `json:"bid"`
	Ask       *float64 `json:"ask"`
}

type BookView struct {
	Symbol string  `json:"symbol"`
	Bids   []Level `json:"bids"`
	Asks   []Level `json:"asks"`
}

func (ex *Exchange) StockViews() []StockView {
	views := make([]StockView, 0, len(ex.Symbols))
	for i := range ex.Symbols {
		s := &ex.Symbols[i]
		views = append(views, StockView{
			Symbol:    s.Info.Symbol,
			Name:      s.Info.Name,
			Fair:      s.Info.Fair,
			LastTrade: s.Info.LastTrade,
			PrevClose: s.Info.PrevClose,
			Bid:       s.Book.BestBid(),
			Ask:       s.Book.BestAsk(),
		})
	}
	return views
}

func (ex *Exchange) BookView(symbol string, levels int) *BookView {
	idx, ok := ex.idxOf(symbol)
	if !ok {
		return nil
	}
	book := &ex.Symbols[idx].Book
	return &BookView{
		Symbol: symbol,
		Bids:   book.depth(SideBuy, levels),
		Asks:   book.depth(SideSell, levels),
	}
}

func (ex *Exchange) Tape(limit int) []Trade {
	if limit > len(ex.Trades) {
		limit = len(ex.Trades)
	}
	// Always a non-nil slice: an empty tape is `[]` on the wire, not `null`.
	// A quiet market is normal on a neutral venue, where nothing crosses the
	// spread until an agent decides to.
	out := make([]Trade, 0, limit)
	return append(out, ex.Trades[:limit]...)
}
