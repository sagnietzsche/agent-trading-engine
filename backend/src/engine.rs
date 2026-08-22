use std::collections::{BTreeMap, HashMap, HashSet, VecDeque};

use chrono::Utc;
use rand::rngs::StdRng;
use rand::SeedableRng;
use rand::Rng;
use serde::Serialize;
use uuid::Uuid;

pub const MARKET_MAKER_ID: Uuid = Uuid::from_u128(0x0000_0000_0000_0000_0000_0000_0000_0001);
pub const SOLIDARITY_ID: Uuid = Uuid::from_u128(0x0000_0000_0000_0000_0000_0000_0000_0002);

/// Collective-welfare tuning knobs.
///
/// The exchange is not neutral: its stated objective is to move every agent
/// toward an equal share of total wealth ("from each according to ability,
/// to each according to needs"). When measured inequality (Gini) exceeds
/// [`GINI_TARGET`], surplus agents are nudged into giving trades.
pub const GINI_TARGET: f64 = 0.20;
pub const ROLE_THRESHOLD: f64 = 0.10;
pub const GIFT_RATE: f64 = 0.05; // fraction of wealth gap offered per mandate
const MAX_TAPE: usize = 400;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Side {
    Buy,
    Sell,
}

impl Side {
    pub fn as_str(self) -> &'static str {
        match self {
            Side::Buy => "buy",
            Side::Sell => "sell",
        }
    }

    fn opposite(self) -> Side {
        match self {
            Side::Buy => Side::Sell,
            Side::Sell => Side::Buy,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum OrderKind {
    Limit,
    Market,
}

impl OrderKind {
    pub fn as_str(self) -> &'static str {
        match self {
            OrderKind::Limit => "limit",
            OrderKind::Market => "market",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Status {
    Open,
    Filled,
    PartiallyFilled,
    Cancelled,
}

impl Status {
    pub fn as_str(self) -> &'static str {
        match self {
            Status::Open => "open",
            Status::Filled => "filled",
            Status::PartiallyFilled => "partially_filled",
            Status::Cancelled => "cancelled",
        }
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct Fill {
    pub trade_id: String,
    pub price: f64,
    pub qty: u32,
}

#[derive(Debug, Clone)]
struct RestingOrder {
    id: u64,
    agent_id: Uuid,
    side: Side,
    price: f64,
    remaining: u32,
}

impl RestingOrder {
    /// Sort key implementing price-time priority (id doubles as time).
    fn key(&self) -> (u64, u64) {
        book_key(self.side, self.price, self.id)
    }
}

fn book_key(side: Side, price: f64, id: u64) -> (u64, u64) {
    let ticks = (price * 100.0).round().max(0.0) as u64;
    match side {
        // bids: higher price first, then older first
        Side::Buy => (u64::MAX - ticks, id),
        // asks: lower price first, then older first
        Side::Sell => (ticks, id),
    }
}

/// Price-time priority order book for one symbol.
#[derive(Debug, Default)]
pub struct Book {
    orders: Vec<RestingOrder>,
}

impl Book {
    fn best(&self, side: Side) -> Option<f64> {
        self.orders
            .iter()
            .filter(|o| o.side == side)
            .min_by_key(|o| o.key())
            .map(|o| o.price)
    }

    pub fn best_bid(&self) -> Option<f64> {
        self.best(Side::Buy)
    }

    pub fn best_ask(&self) -> Option<f64> {
        self.best(Side::Sell)
    }

    fn insert(&mut self, order: RestingOrder) {
        self.orders.push(order);
    }

    fn reduce(&mut self, id: u64, qty: u32) {
        if let Some(o) = self.orders.iter_mut().find(|o| o.id == id) {
            o.remaining -= qty;
        }
        self.orders.retain(|o| o.remaining > 0);
    }

    fn remove(&mut self, id: u64) -> Option<RestingOrder> {
        let pos = self.orders.iter().position(|o| o.id == id)?;
        Some(self.orders.remove(pos))
    }

    fn remove_all_of_agent(&mut self, agent_id: Uuid) -> Vec<RestingOrder> {
        let (removed, kept): (Vec<_>, Vec<_>) =
            self.orders.drain(..).partition(|o| o.agent_id == agent_id);
        self.orders = kept;
        removed
    }

    /// Aggregate depth per price level, best-first.
    pub fn depth(&self, side: Side, levels: usize) -> Vec<(f64, u32)> {
        let mut out: Vec<(f64, u32)> = Vec::new();
        let mut sorted: Vec<&RestingOrder> =
            self.orders.iter().filter(|o| o.side == side).collect();
        sorted.sort_by_key(|o| o.key());
        for o in sorted {
            match out.last_mut() {
                Some((price, qty)) if *price == o.price => *qty += o.remaining,
                _ => {
                    if out.len() == levels {
                        break;
                    }
                    out.push((o.price, o.remaining));
                }
            }
        }
        out
    }
}

#[derive(Debug, Clone)]
pub struct AgentCache {
    pub id: Uuid,
    pub name: String,
    pub is_bot: bool,
    pub cash: f64,
    pub reserved_cash: f64,
    pub positions: HashMap<String, i64>,
    pub reserved_shares: HashMap<String, u32>,
}

impl AgentCache {
    pub fn free_cash(&self) -> f64 {
        self.cash - self.reserved_cash
    }

    pub fn free_shares(&self, symbol: &str) -> i64 {
        self.positions.get(symbol).copied().unwrap_or(0)
            - self.reserved_shares.get(symbol).copied().unwrap_or(0) as i64
    }

    pub fn equity(&self, marks: &HashMap<String, f64>) -> f64 {
        let holdings: f64 = self
            .positions
            .iter()
            .map(|(sym, qty)| *qty as f64 * marks.get(sym).copied().unwrap_or(0.0))
            .sum();
        self.cash + holdings
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct Trade {
    pub id: String,
    pub symbol: String,
    pub price: f64,
    pub qty: u32,
    pub buyer: Uuid,
    pub seller: Uuid,
    pub taker_order: u64,
    pub buyer_equity: f64,
    pub seller_equity: f64,
    pub gini_after: f64,
    pub ts: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct StockInfo {
    pub symbol: String,
    pub name: String,
    pub fair: f64,
    pub last_trade: Option<f64>,
    pub prev_close: f64,
}

#[derive(Debug)]
pub struct SymbolState {
    pub info: StockInfo,
    pub book: Book,
}

#[derive(Debug, Clone, Serialize)]
pub struct OrderRecord {
    pub id: u64,
    pub agent_id: Uuid,
    pub symbol: String,
    pub side: Side,
    pub kind: OrderKind,
    pub price: Option<f64>,
    pub qty: u32,
    pub filled: u32,
    pub status: Status,
    pub created_at: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct WelfareSnapshot {
    pub gini: f64,
    pub total_equity: f64,
    pub mean_equity: f64,
    pub ts: String,
}

/// Everything mutated since the last drain; flushed to Postgres by store.rs.
#[derive(Default)]
pub struct Pending {
    pub agents: BTreeMap<Uuid, AgentCache>,
    pub positions: BTreeMap<(Uuid, String), i64>,
    pub orders: BTreeMap<u64, OrderRecord>,
    pub trades: Vec<Trade>,
    pub snapshots: Vec<WelfareSnapshot>,
}

impl Pending {
    fn drain(&mut self) -> Pending {
        std::mem::take(self)
    }
}

// ---------------------------------------------------------------------------
// Welfare: the collective objective layer
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    Contributor,
    Beneficiary,
    Neutral,
}

#[derive(Debug, Clone, Serialize)]
pub struct Mandate {
    pub agent_id: Uuid,
    pub name: String,
    pub equity: f64,
    pub deviation: f64,
    pub role: Role,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub suggestion: Option<Suggestion>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Suggestion {
    pub symbol: String,
    pub side: Side,
    pub qty: u32,
    pub limit: f64,
    /// Human-readable reason tied to the collective objective.
    pub rationale: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct Welfare {
    pub gini: f64,
    pub total_equity: f64,
    pub mean_equity: f64,
    pub gini_target: f64,
}

/// Gini coefficient over agent equities: 0 = perfectly equal, 1 = one agent owns everything.
fn gini(mut values: Vec<f64>) -> f64 {
    let n = values.len();
    if n < 2 {
        return 0.0;
    }
    values.sort_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal));
    let total: f64 = values.iter().sum();
    if total <= 0.0 {
        return 0.0;
    }
    let weighted: f64 = values
        .iter()
        .enumerate()
        .map(|(i, x)| (2 * (i + 1) as isize - n as isize - 1) as f64 * x)
        .sum();
    (weighted / (n as f64 * total)).max(0.0)
}

fn round_cents(p: f64) -> f64 {
    (p * 100.0).round() / 100.0
}

// ---------------------------------------------------------------------------
// Exchange
// ---------------------------------------------------------------------------

pub struct Exchange {
    pub symbols: Vec<SymbolState>,
    by_symbol: HashMap<String, usize>,
    pub agents: HashMap<Uuid, AgentCache>,
    pub trades: VecDeque<Trade>,
    pub orders: HashMap<u64, OrderRecord>,
    next_order_id: u64,
    rng: StdRng,
    pending: Pending,
}

#[derive(Debug)]
pub enum PlaceError {
    UnknownSymbol(String),
    UnknownAgent,
    InvalidQty,
    InvalidPrice,
    InsufficientCash { need: f64, have: f64 },
    InsufficientShares { need: u32, have: u32 },
    NoLiquidity,
}

/// Snapshot fed back into the engine at boot so books/accounts survive restarts.
pub struct RestoreState {
    pub stocks: Vec<StockInfo>,
    pub agents: Vec<AgentCache>,
    pub open_orders: Vec<OrderRecord>,
    pub next_order_id: u64,
}

impl Exchange {
    pub fn new(listings: &[(&str, &str, f64)]) -> Self {
        let mut symbols = Vec::new();
        let mut by_symbol = HashMap::new();
        for (i, (symbol, name, base)) in listings.iter().enumerate() {
            by_symbol.insert(symbol.to_string(), i);
            symbols.push(SymbolState {
                info: StockInfo {
                    symbol: symbol.to_string(),
                    name: name.to_string(),
                    fair: *base,
                    last_trade: None,
                    prev_close: *base,
                },
                book: Book::default(),
            });
        }
        Exchange {
            symbols,
            by_symbol,
            agents: HashMap::new(),
            trades: VecDeque::new(),
            orders: HashMap::new(),
            next_order_id: 1,
            rng: StdRng::from_os_rng(),
            pending: Pending::default(),
        }
    }

    /// Brand-new exchange with listings, system agents and a live opening book.
    pub fn fresh_simulated() -> Exchange {
        let mut ex = Exchange::new(LISTINGS);
        ex.seed_system_agents();
        ex.sim_tick();
        ex
    }

    /// Rebuild in-memory state from Postgres rows (called once at startup).
    pub fn restore(state: RestoreState) -> Self {
        let mut ex = Exchange {
            symbols: Vec::new(),
            by_symbol: HashMap::new(),
            agents: HashMap::new(),
            trades: VecDeque::new(),
            orders: HashMap::new(),
            next_order_id: state.next_order_id,
            rng: StdRng::from_os_rng(),
            pending: Pending::default(),
        };
        for info in state.stocks {
            ex.by_symbol.insert(info.symbol.clone(), ex.symbols.len());
            ex.symbols.push(SymbolState {
                info,
                book: Book::default(),
            });
        }
        for agent in state.agents {
            ex.agents.insert(agent.id, agent);
        }
        // Rebuild reservations strictly from non-bot resting orders; bot quotes
        // are requoted fresh on the first tick.
        let mut opens: Vec<_> = state.open_orders;
        opens.sort_by_key(|o| o.id);
        for rec in opens {
            if rec.kind != OrderKind::Limit || rec.price.is_none() {
                continue;
            }
            let remaining = rec.qty.saturating_sub(rec.filled);
            if remaining == 0 {
                continue;
            }
            let is_bot = ex.agents.get(&rec.agent_id).is_some_and(|a| a.is_bot);
            if !is_bot {
                match rec.side {
                    Side::Buy => {
                        let price = rec.price.unwrap_or_default();
                        if let Some(a) = ex.agents.get_mut(&rec.agent_id) {
                            a.reserved_cash += round_cents(price * remaining as f64);
                        }
                    }
                    Side::Sell => {
                        if let Some(a) = ex.agents.get_mut(&rec.agent_id) {
                            *a.reserved_shares.entry(rec.symbol.clone()).or_insert(0) += remaining;
                        }
                    }
                }
            }
            let idx = ex.by_symbol[&rec.symbol];
            ex.symbols[idx].book.insert(RestingOrder {
                id: rec.id,
                agent_id: rec.agent_id,
                side: rec.side,
                price: rec.price.unwrap_or_default(),
                remaining,
            });
            ex.orders.insert(rec.id, rec);
        }
        ex
    }

    pub fn register_agent(&mut self, name: &str, cash: f64) -> Uuid {
        let id = Uuid::new_v4();
        let cache = AgentCache {
            id,
            name: name.to_string(),
            is_bot: false,
            cash,
            reserved_cash: 0.0,
            positions: HashMap::new(),
            reserved_shares: HashMap::new(),
        };
        self.pending.agents.insert(id, cache.clone());
        self.agents.insert(id, cache);
        id
    }

    pub fn upsert_agent_cache(&mut self, cache: AgentCache) {
        for sym in cache.positions.keys() {
            self.pending
                .positions
                .insert((cache.id, sym.clone()), 0);
        }
        self.touch_agent(cache.id);
        for (sym, qty) in cache.positions.clone() {
            self.pending.positions.insert((cache.id, sym), qty);
        }
        self.agents.insert(cache.id, cache);
    }

    fn idx_of(&self, symbol: &str) -> Option<usize> {
        self.by_symbol.get(symbol).copied()
    }

    pub fn marks(&self) -> HashMap<String, f64> {
        self.symbols
            .iter()
            .map(|s| {
                (
                    s.info.symbol.clone(),
                    s.info.last_trade.or(s.book.best_bid()).unwrap_or(s.info.fair),
                )
            })
            .collect()
    }

    // -- welfare -------------------------------------------------------------

    pub fn welfare(&self) -> Welfare {
        let marks = self.marks();
        let eqs: Vec<f64> = self.agents.values().map(|a| a.equity(&marks)).collect();
        let total: f64 = eqs.iter().sum();
        let mean = if eqs.is_empty() { 0.0 } else { total / eqs.len() as f64 };
        Welfare {
            gini: gini(eqs),
            total_equity: total,
            mean_equity: mean,
            gini_target: GINI_TARGET,
        }
    }

    /// The cooperative decision layer: tells every agent what trade would most
    /// reduce inequality right now. Contributors give (sell at the bid, taking
    /// a price concession); beneficiaries receive (buy at the ask using gap funds).
    pub fn mandates(&self) -> Vec<Mandate> {
        let marks = self.marks();
        let eqs: HashMap<Uuid, f64> =
            self.agents.values().map(|a| (a.id, a.equity(&marks))).collect();
        let total: f64 = eqs.values().sum();
        let mean = if eqs.is_empty() { 0.0 } else { total / eqs.len() as f64 };

        self.agents
            .values()
            .map(|agent| {
                let equity = eqs[&agent.id];
                let deviation = if mean > 0.0 {
                    (equity - mean) / mean
                } else {
                    0.0
                };
                let role = if deviation > ROLE_THRESHOLD {
                    Role::Contributor
                } else if deviation < -ROLE_THRESHOLD {
                    Role::Beneficiary
                } else {
                    Role::Neutral
                };
                let suggestion = self.suggest(agent, equity - mean, mean, role);
                Mandate {
                    agent_id: agent.id,
                    name: agent.name.clone(),
                    equity,
                    deviation,
                    role,
                    suggestion,
                }
            })
            .collect()
    }

    fn suggest(
        &self,
        agent: &AgentCache,
        gap: f64,
        mean: f64,
        role: Role,
    ) -> Option<Suggestion> {
        let dev_pct = if mean > 0.0 { gap / mean * 100.0 } else { 0.0 };
        match role {
            Role::Neutral => None,
            Role::Contributor => {
                // Give away inventory from the largest holding, priced at the
                // bid so it crosses instantly — the concession is the gift.
                let (symbol, held) = agent
                    .positions
                    .iter()
                    .max_by_key(|(_, q)| **q)
                    .map(|(s, q)| (s.clone(), *q))?;
                let free = agent.free_shares(&symbol).min(held);
                if free < 1 {
                    return None;
                }
                let idx = self.idx_of(&symbol)?;
                let s = &self.symbols[idx];
                let price = s.book.best_bid().unwrap_or(s.info.fair);
                if price <= 0.0 {
                    return None;
                }
                let qty = ((gap.abs() * GIFT_RATE / price) as u32).clamp(1, free as u32);
                Some(Suggestion {
                    symbol,
                    side: Side::Sell,
                    qty,
                    limit: price,
                    rationale: format!(
                        "You hold {dev_pct:+.1}% vs the mean. Selling {qty} {} at the bid transfers value to members below the mean.",
                        s.info.symbol,
                    ),
                })
            }
            Role::Beneficiary => {
                // Use a slice of the shortfall to acquire assets at the ask.
                let mut best: Option<(String, f64)> = None;
                for s in &self.symbols {
                    if let Some(ask) = s.book.best_ask() {
                        let tightness = (ask - s.info.fair).abs();
                        if best.as_ref().map_or(true, |(_, t)| tightness < *t) {
                            best = Some((s.info.symbol.clone(), ask));
                        }
                    }
                }
                let (symbol, ask) = best?;
                let budget = (gap.abs() * GIFT_RATE).min(agent.free_cash());
                if budget < ask {
                    return None;
                }
                let qty = (budget / ask) as u32;
                if qty == 0 {
                    return None;
                }
                Some(Suggestion {
                    rationale: format!(
                        "You are {dev_pct:.1}% below the mean. Buying {qty} {symbol} brings you closer to the collective optimum.",
                    ),
                    symbol,
                    side: Side::Buy,
                    qty,
                    limit: ask,
                })
            }
        }
    }

    // -- persistence buffer ---------------------------------------------------

    pub fn drain_pending(&mut self) -> Pending {
        self.pending.drain()
    }

    fn touch_agent(&mut self, id: Uuid) {
        if let Some(cache) = self.agents.get(&id) {
            let cache = cache.clone();
            self.pending.agents.insert(cache.id, cache);
        }
    }

    fn touch_position(&mut self, id: Uuid, symbol: &str) {
        let qty = self
            .agents
            .get(&id)
            .and_then(|a| a.positions.get(symbol).copied())
            .unwrap_or(0);
        self.pending
            .positions
            .insert((id, symbol.to_string()), qty);
    }

    fn record(&mut self, rec: OrderRecord) {
        self.pending.orders.insert(rec.id, rec.clone());
        self.orders.insert(rec.id, rec);
    }

    // -- order placement -------------------------------------------------------

    /// Place an order. `solidarity=true` marks it as a redistribution order:
    /// the matcher will route it to *beneficiary* counterparties first
    /// (agents meaningfully below the mean), falling back to normal
    /// price-time priority when no member needs help. This is how the
    /// collective objective is enforced at the microstructure level.
    pub fn place_order(
        &mut self,
        agent_id: Uuid,
        symbol: &str,
        side: Side,
        kind: OrderKind,
        qty: u32,
        price: Option<f64>,
    ) -> Result<(OrderRecord, Vec<Fill>), PlaceError> {
        self.place_order_inner(agent_id, symbol, side, kind, qty, price, false)
    }

    pub fn place_solidarity_order(
        &mut self,
        agent_id: Uuid,
        symbol: &str,
        side: Side,
        kind: OrderKind,
        qty: u32,
        price: Option<f64>,
    ) -> Result<(OrderRecord, Vec<Fill>), PlaceError> {
        self.place_order_inner(agent_id, symbol, side, kind, qty, price, true)
    }

    fn place_order_inner(
        &mut self,
        agent_id: Uuid,
        symbol: &str,
        side: Side,
        kind: OrderKind,
        qty: u32,
        price: Option<f64>,
        solidarity: bool,
    ) -> Result<(OrderRecord, Vec<Fill>), PlaceError> {
        let Some(agent) = self.agents.get(&agent_id) else {
            return Err(PlaceError::UnknownAgent);
        };
        if qty == 0 {
            return Err(PlaceError::InvalidQty);
        }
        let idx = self.idx_of(symbol).ok_or_else(|| PlaceError::UnknownSymbol(symbol.to_string()))?;

        let limit_price = match kind {
            OrderKind::Limit => {
                let p = price.ok_or(PlaceError::InvalidPrice)?;
                if !(p > 0.0 && p <= 1_000_000.0) {
                    return Err(PlaceError::InvalidPrice);
                }
                Some(round_cents(p))
            }
            OrderKind::Market => None,
        };

        // Reserve resources up-front so no agent can promise what it lacks.
        let cash_reserve = match side {
            Side::Buy => {
                let cost = match limit_price {
                    Some(l) => round_cents(l * qty as f64),
                    None => {
                        let top = self.symbols[idx]
                            .book
                            .best_ask()
                            .ok_or(PlaceError::NoLiquidity)?;
                        round_cents(top * qty as f64 * 1.001) // slippage buffer
                    }
                };
                if agent.free_cash() + 1e-9 < cost {
                    return Err(PlaceError::InsufficientCash {
                        need: cost,
                        have: round_cents(agent.free_cash()),
                    });
                }
                if let Some(a) = self.agents.get_mut(&agent_id) {
                    a.reserved_cash += cost;
                }
                Some(cost)
            }
            Side::Sell => {
                // System liquidity agents (market maker, solidarity bot) may
                // quote without inventory — their shorts unwind as flow evens
                // out. Human agents must own what they promise to sell.
                if !agent.is_bot {
                    let free = agent.free_shares(symbol);
                    if free < qty as i64 {
                        return Err(PlaceError::InsufficientShares {
                            need: qty,
                            have: free.max(0) as u32,
                        });
                    }
                    if let Some(a) = self.agents.get_mut(&agent_id) {
                        *a.reserved_shares.entry(symbol.to_string()).or_insert(0) += qty;
                    }
                }
                None
            }
        };

        let id = self.next_order_id;
        self.next_order_id += 1;
        let mut record = OrderRecord {
            id,
            agent_id,
            symbol: symbol.to_string(),
            side,
            kind,
            price: limit_price,
            qty,
            filled: 0,
            status: Status::Open,
            created_at: Utc::now().to_rfc3339(),
        };

        let fills = self.execute(idx, id, agent_id, side, qty, limit_price, solidarity);
        record.filled = fills.iter().map(|f| f.qty).sum();

        let resting = matches!(kind, OrderKind::Limit) && record.filled < qty;
        record.status = if record.filled == qty {
            Status::Filled
        } else if resting {
            if record.filled > 0 {
                Status::PartiallyFilled
            } else {
                Status::Open
            }
        } else if record.filled > 0 {
            Status::PartiallyFilled
        } else {
            Status::Cancelled
        };

        if resting {
            let l = limit_price.expect("resting implies limit");
            self.symbols[idx].book.insert(RestingOrder {
                id,
                agent_id,
                side,
                price: l,
                remaining: qty - record.filled,
            });
        }

        // Release whatever part of the reservation is no longer needed. Cash
        // for actual fills already moved during settlement, so releasing the
        // reservation never double-counts: free_cash = cash - reserved_cash.
        {
            let a = self.agents.get_mut(&agent_id).expect("checked above");
            match side {
                Side::Buy => {
                    let keep = if resting {
                        round_cents(limit_price.unwrap() * (qty - record.filled) as f64)
                    } else {
                        0.0
                    };
                    let release = (cash_reserve.unwrap_or_default() - keep).max(0.0);
                    a.reserved_cash = (a.reserved_cash - release).max(0.0);
                }
                Side::Sell => {
                    if !a.is_bot {
                        let keep = if resting { qty - record.filled } else { 0 };
                        let entry = a.reserved_shares.entry(symbol.to_string()).or_insert(0);
                        *entry = entry.saturating_sub(qty - keep);
                    }
                }
            }
        }

        self.touch_agent(agent_id);
        self.record(record.clone());
        Ok((record, fills))
    }

    fn execute(
        &mut self,
        idx: usize,
        taker_order_id: u64,
        taker: Uuid,
        side: Side,
        mut qty: u32,
        limit: Option<f64>,
        solidarity: bool,
    ) -> Vec<Fill> {
        // For solidarity orders, pre-compute who currently sits below the
        // threshold so their resting orders get matched first.
        let beneficiary_ids = if solidarity {
            let marks = self.marks();
            let total: f64 = self.agents.values().map(|a| a.equity(&marks)).sum();
            let mean = if self.agents.is_empty() {
                0.0
            } else {
                total / self.agents.len() as f64
            };
            Some(
                self.agents
                    .values()
                    .filter(|a| {
                        mean > 0.0 && (a.equity(&marks) - mean) / mean < -ROLE_THRESHOLD
                    })
                    .map(|a| a.id)
                    .collect::<HashSet<Uuid>>(),
            )
        } else {
            None
        };

        let mut fills = Vec::new();
        while qty > 0 {
            let candidates: Vec<(u64, f64, Uuid, u32)> = {
                let book = &self.symbols[idx].book;
                book.orders
                    .iter()
                    .filter(|o| o.side != side)
                    .filter(|o| o.agent_id != taker) // no wash trades, even for charity
                    .filter(|o| match (side, limit) {
                        (Side::Buy, Some(l)) => o.price <= l,
                        (Side::Sell, Some(l)) => o.price >= l,
                        _ => true,
                    })
                    .map(|o| (o.id, o.price, o.agent_id, o.remaining.min(qty)))
                    .collect()
            };
            if candidates.is_empty() {
                break;
            }

            let maker_side = side.opposite();
            let pick = |pool: &[(u64, f64, Uuid, u32)]| {
                pool.iter()
                    .copied()
                    .min_by_key(|c| book_key(maker_side, c.1, c.0))
            };
            // Need-priority routing: solidarity orders help the worst-off
            // members first; everyone else gets plain price-time priority.
            let counterparty = match &beneficiary_ids {
                Some(needy) => {
                    let helped: Vec<_> = candidates
                        .iter()
                        .copied()
                        .filter(|(_, _, a, _)| needy.contains(a))
                        .collect();
                    pick(if helped.is_empty() { &candidates } else { &helped })
                }
                None => pick(&candidates),
            };
            let (maker_id, maker_price, maker_agent, fill_qty) =
                counterparty.expect("candidates non-empty");

            let symbol = self.symbols[idx].info.symbol.clone();

            // Release the maker's reservation for the filled amount
            // (system liquidity agents run unhedged and hold none).
            if !self.agents[&maker_agent].is_bot {
                let maker_is_buyer = side == Side::Sell;
                let a = self.agents.get_mut(&maker_agent).expect("maker cached");
                if maker_is_buyer {
                    a.reserved_cash = (a.reserved_cash - round_cents(maker_price * fill_qty as f64)).max(0.0);
                } else {
                    let e = a.reserved_shares.entry(symbol.clone()).or_insert(0);
                    *e = e.saturating_sub(fill_qty);
                }
            }

            // Equity context for the welfare ledger, captured pre-settlement.
            let marks = self.marks();
            let buyer_id = if side == Side::Buy { taker } else { maker_agent };
            let seller_id = if side == Side::Sell { taker } else { maker_agent };
            let buyer_eq = self
                .agents
                .get(&buyer_id)
                .map(|a| a.equity(&marks))
                .unwrap_or_default();
            let seller_eq = self
                .agents
                .get(&seller_id)
                .map(|a| a.equity(&marks))
                .unwrap_or_default();

            self.symbols[idx].book.reduce(maker_id, fill_qty);

            // Keep the maker's persisted order record in sync with passive fills.
            if let Some(mrec) = self.orders.get_mut(&maker_id) {
                mrec.filled += fill_qty;
                mrec.status = if mrec.filled >= mrec.qty {
                    Status::Filled
                } else {
                    Status::PartiallyFilled
                };
                let snapshot = mrec.clone();
                self.pending.orders.insert(maker_id, snapshot);
            }

            // Settlement: value moves from buyer to seller, shares the other
            // way. Sellers may go inventory-negative (system liquidity bots).
            let cost = round_cents(maker_price * fill_qty as f64);
            if let Some(a) = self.agents.get_mut(&buyer_id) {
                a.cash -= cost;
                *a.positions.entry(symbol.clone()).or_insert(0) += fill_qty as i64;
            }
            if let Some(a) = self.agents.get_mut(&seller_id) {
                a.cash += cost;
                *a.positions.entry(symbol.clone()).or_insert(0) -= fill_qty as i64;
            }
            self.touch_agent(buyer_id);
            self.touch_agent(seller_id);
            self.touch_position(buyer_id, &symbol);
            self.touch_position(seller_id, &symbol);

            let gini_now = {
                let eqs: Vec<f64> =
                    self.agents.values().map(|a| a.equity(&marks)).collect();
                gini(eqs)
            };
            let trade = Trade {
                id: Uuid::new_v4().to_string(),
                symbol,
                price: maker_price,
                qty: fill_qty,
                buyer: buyer_id,
                seller: seller_id,
                taker_order: taker_order_id,
                buyer_equity: buyer_eq,
                seller_equity: seller_eq,
                gini_after: gini_now,
                ts: Utc::now().to_rfc3339(),
            };
            self.symbols[idx].info.last_trade = Some(trade.price);
            self.trades.push_front(trade.clone());
            while self.trades.len() > MAX_TAPE {
                self.trades.pop_back();
            }
            self.pending.trades.push(trade.clone());
            fills.push(Fill {
                trade_id: trade.id,
                price: maker_price,
                qty: fill_qty,
            });
            qty -= fill_qty;
        }
        fills
    }

    pub fn cancel_order(&mut self, order_id: u64, agent_id: Uuid) -> Result<OrderRecord, String> {
        let rec = self.orders.get(&order_id).cloned().ok_or("order not found")?;
        if rec.agent_id != agent_id {
            return Err("order does not belong to agent".into());
        }
        if !matches!(rec.status, Status::Open | Status::PartiallyFilled) {
            return Err("order is not cancellable".into());
        }
        let idx = self.idx_of(&rec.symbol).ok_or("unknown symbol")?;
        let removed = self.symbols[idx]
            .book
            .remove(order_id)
            .ok_or("order not resting")?;

        let remaining = removed.remaining;
        let a = self.agents.get_mut(&agent_id).ok_or("unknown agent")?;
        match rec.side {
            Side::Buy => {
                let l = rec.price.unwrap_or_default();
                a.reserved_cash = (a.reserved_cash - round_cents(l * remaining as f64)).max(0.0);
            }
            Side::Sell => {
                let e = a.reserved_shares.entry(rec.symbol.clone()).or_insert(0);
                *e = e.saturating_sub(remaining);
            }
        }

        let updated = OrderRecord {
            status: Status::Cancelled,
            ..rec
        };
        self.touch_agent(agent_id);
        self.record(updated.clone());
        Ok(updated)
    }

    /// Pull all resting quotes for one agent (used to requote market makers).
    pub fn cancel_all_for_agent(&mut self, agent_id: Uuid) {
        for idx in 0..self.symbols.len() {
            let symbol = self.symbols[idx].info.symbol.clone();
            for removed in self.symbols[idx].book.remove_all_of_agent(agent_id) {
                let rec = self.orders.get_mut(&removed.id);
                if let Some(rec) = rec {
                    rec.status = Status::Cancelled;
                    let rec = rec.clone();
                    self.pending.orders.insert(rec.id, rec);
                }
                if let Some(a) = self.agents.get_mut(&agent_id) {
                    if !a.is_bot {
                        match removed.side {
                            Side::Buy => {
                                a.reserved_cash = (a.reserved_cash
                                    - round_cents(removed.price * removed.remaining as f64))
                                .max(0.0);
                            }
                            Side::Sell => {
                                let e = a
                                    .reserved_shares
                                    .entry(symbol.clone())
                                    .or_insert(0);
                                *e = e.saturating_sub(removed.remaining);
                            }
                        }
                    }
                }
            }
        }
        self.touch_agent(agent_id);
    }

    // -- simulation ---------------------------------------------------------

    /// Seed the two system agents. The solidarity bot starts wealthy on purpose:
    /// watching it give that wealth away is the whole point of the demo.
    pub fn seed_system_agents(&mut self) {
        let listings: Vec<String> = self
            .symbols
            .iter()
            .map(|s| s.info.symbol.clone())
            .collect();
        self.agents.insert(
            MARKET_MAKER_ID,
            AgentCache {
                id: MARKET_MAKER_ID,
                name: "market_maker".into(),
                is_bot: true,
                cash: 10_000_000.0,
                reserved_cash: 0.0,
                positions: HashMap::new(),
                reserved_shares: HashMap::new(),
            },
        );
        let mut inv: HashMap<String, i64> = HashMap::new();
        for s in &listings {
            inv.insert(s.clone(), 40_000);
        }
        self.agents.insert(
            SOLIDARITY_ID,
            AgentCache {
                id: SOLIDARITY_ID,
                name: "solidarity_bot".into(),
                is_bot: true,
                cash: 6_000_000.0,
                reserved_cash: 0.0,
                positions: inv,
                reserved_shares: HashMap::new(),
            },
        );
        for id in [MARKET_MAKER_ID, SOLIDARITY_ID] {
            self.touch_agent(id);
            for s in &listings {
                self.touch_position(id, s);
            }
        }
    }

    pub fn sim_tick(&mut self) {
        // 1. Random walk fair values.
        for s in &mut self.symbols {
            let g: f64 = self.rng.random_range(-1.0..1.0);
            let shock = g * g * g * 3.0;
            let drift = self.rng.random_range(-0.0015..0.002);
            s.info.fair *= 1.0 + drift + shock * 0.004;
            s.info.fair = s.info.fair.clamp(1.0, 100_000.0);
        }

        // 2. Neutral market maker provides liquid, tight two-sided markets
        //    (a public good: everyone trades at better prices because of it).
        self.cancel_all_for_agent(MARKET_MAKER_ID);
        for idx in 0..self.symbols.len() {
            let fair = self.symbols[idx].info.fair;
            let spread = (fair * 0.0015).max(0.01);
            for level in 1..=3usize {
                let size: u32 = self.rng.random_range(20..90);
                let bid = round_cents(fair - spread * level as f64);
                let ask = round_cents(fair + spread * level as f64);
                let sym = self.symbols[idx].info.symbol.clone();
                let _ = self.place_order(MARKET_MAKER_ID, &sym, Side::Buy, OrderKind::Limit, size, Some(bid));
                let _ = self.place_order(MARKET_MAKER_ID, &sym, Side::Sell, OrderKind::Limit, size, Some(ask));
            }
        }

        self.cancel_all_for_agent(SOLIDARITY_ID);

        // 3. Cooperative redistribution: while inequality sits above target,
        //    the solidarity bot executes its giving mandate. Need-priority
        //    matching routes those fills to the worst-off members directly,
        //    so the gift never gets intercepted by neutral liquidity.
        let w = self.welfare();
        if w.gini > GINI_TARGET {
            if let Some(mandate) = self
                .mandates()
                .into_iter()
                .find(|m| m.agent_id == SOLIDARITY_ID)
                .and_then(|m| m.suggestion)
            {
                let _ = self.place_solidarity_order(
                    SOLIDARITY_ID,
                    &mandate.symbol,
                    mandate.side,
                    OrderKind::Limit,
                    mandate.qty.min(500),
                    Some(mandate.limit),
                );
            }
        }

        // 4. Record the welfare trend.
        let w = self.welfare();
        self.pending.snapshots.push(WelfareSnapshot {
            gini: w.gini,
            total_equity: w.total_equity,
            mean_equity: w.mean_equity,
            ts: Utc::now().to_rfc3339(),
        });
    }

    // -- read models ----------------------------------------------------------

    pub fn stock_views(&self) -> Vec<StockView> {
        self.symbols
            .iter()
            .map(|s| StockView {
                symbol: s.info.symbol.clone(),
                name: s.info.name.clone(),
                fair: s.info.fair,
                last_trade: s.info.last_trade,
                prev_close: s.info.prev_close,
                bid: s.book.best_bid(),
                ask: s.book.best_ask(),
            })
            .collect()
    }

    pub fn book_view(&self, symbol: &str, levels: usize) -> Option<BookView> {
        let idx = self.idx_of(symbol)?;
        let book = &self.symbols[idx].book;
        Some(BookView {
            symbol: symbol.to_string(),
            bids: book.depth(Side::Buy, levels),
            asks: book.depth(Side::Sell, levels),
        })
    }

    pub fn tape(&self, limit: usize) -> Vec<Trade> {
        self.trades.iter().take(limit).cloned().collect()
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct StockView {
    pub symbol: String,
    pub name: String,
    pub fair: f64,
    pub last_trade: Option<f64>,
    pub prev_close: f64,
    pub bid: Option<f64>,
    pub ask: Option<f64>,
}

#[derive(Debug, Clone, Serialize)]
pub struct BookView {
    pub symbol: String,
    pub bids: Vec<(f64, u32)>,
    pub asks: Vec<(f64, u32)>,
}

const LISTINGS: &[(&str, &str, f64)] = &[
    ("NOVA", "Nova Dynamics", 184.20),
    ("QNTM", "Quantum Foundry", 92.75),
    ("HELX", "Helix Biolabs", 341.10),
    ("DRCT", "Direct Commons", 47.55),
    ("ORBT", "Orbital Logistics", 128.40),
    ("ZEPH", "Zephyr Energy", 63.90),
];

// ---------------------------------------------------------------------------
// Tests: the engine is pure and needs no database
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    fn test_exchange() -> Exchange {
        // Fresh exchange whose opening simulation has been flushed, so tests
        // start with clean pending buffers.
        let mut ex = Exchange::fresh_simulated();
        ex.drain_pending();
        ex
    }

    fn clear_bots(ex: &mut Exchange) {
        ex.cancel_all_for_agent(MARKET_MAKER_ID);
        ex.cancel_all_for_agent(SOLIDARITY_ID);
        ex.drain_pending();
    }

    fn alice(ex: &mut Exchange) -> Uuid {
        ex.register_agent("alice", 100_000.0)
    }

    fn bob(ex: &mut Exchange) -> Uuid {
        ex.register_agent("bob", 100_000.0)
    }

    #[test]
    fn gini_basics() {
        assert_eq!(gini(vec![5.0]), 0.0);
        assert_eq!(gini(vec![100.0, 100.0, 100.0, 100.0]), 0.0);
        // [100,200,300,400]: classic textbook case -> 0.25
        let g = gini(vec![400.0, 100.0, 300.0, 200.0]);
        assert!((g - 0.25).abs() < 1e-9, "got {g}");
        assert!(gini(vec![0.0, 0.0, 0.0, 100.0]) > 0.7);
    }

    #[test]
    fn limit_cross_fully_fills_at_maker_price() {
        let mut ex = test_exchange();
        clear_bots(&mut ex);
        let a = alice(&mut ex);
        let b = bob(&mut ex);

        // Bob has inventory and offers 10 @ 100 (well below the MM quote).
        ex.agents.get_mut(&b).unwrap().positions.insert("NOVA".into(), 100);
        let (_, _) = ex.place_order(b, "NOVA", Side::Sell, OrderKind::Limit, 10, Some(100.0)).unwrap();

        // Alice lifts it with a buy limit at 101 -> trade at bob's 100.
        let (a_order, fills) = ex.place_order(a, "NOVA", Side::Buy, OrderKind::Limit, 10, Some(101.0)).unwrap();
        assert_eq!(a_order.status, Status::Filled);
        assert_eq!(fills.len(), 1);
        assert_eq!(fills[0].price, 100.0);
        assert_eq!(ex.symbols[0].info.last_trade, Some(100.0));

        // Alice paid exactly cost; reservation fully released.
        let alice_cache = ex.agents[&a].clone();
        assert!((alice_cache.cash - 99_000.0).abs() < 1e-6);
        assert!((alice_cache.reserved_cash).abs() < 1e-6);
        assert_eq!(alice_cache.positions.get("NOVA"), Some(&10));

        // Bob received the cash and his shares left.
        let bob_cache = ex.agents[&b].clone();
        assert!((bob_cache.cash - 101_000.0).abs() < 1e-6);
        assert_eq!(bob_cache.positions.get("NOVA"), Some(&90));
    }

    #[test]
    fn price_time_priority_and_sweep() {
        let mut ex = test_exchange();
        clear_bots(&mut ex);
        let a = alice(&mut ex);
        let b = bob(&mut ex);
        let c = ex.register_agent("carol", 100_000.0);

        ex.agents.get_mut(&b).unwrap().positions.insert("NOVA".into(), 500);
        ex.agents.get_mut(&c).unwrap().positions.insert("NOVA".into(), 500);

        ex.place_order(b, "NOVA", Side::Sell, OrderKind::Limit, 50, Some(100.0)).unwrap();
        ex.place_order(c, "NOVA", Side::Sell, OrderKind::Limit, 50, Some(100.0)).unwrap(); // same price, later time
        ex.place_order(b, "NOVA", Side::Sell, OrderKind::Limit, 40, Some(100.50)).unwrap();

        let (_, fills) = ex.place_order(a, "NOVA", Side::Buy, OrderKind::Market, 130, None).unwrap();
        let prices: Vec<f64> = fills.iter().map(|f| f.price).collect();
        assert_eq!(prices, vec![100.0, 100.0, 100.5]);
        let qtys: Vec<u32> = fills.iter().map(|f| f.qty).collect();
        assert_eq!(qtys, vec![50, 50, 30]);
        // 10 shares remain at the second level.
        assert_eq!(ex.symbols[0].book.best_ask(), Some(100.5));
    }

    #[test]
    fn partial_limit_rests_then_fills() {
        let mut ex = test_exchange();
        let a = alice(&mut ex);
        let b = bob(&mut ex);
        ex.agents.get_mut(&b).unwrap().positions.insert("NOVA".into(), 100);
        ex.cancel_all_for_agent(MARKET_MAKER_ID);
        ex.cancel_all_for_agent(SOLIDARITY_ID);

        // Alice's bid is below the market: it rests untouched.
        let (rec, fills) = ex.place_order(a, "NOVA", Side::Buy, OrderKind::Limit, 25, Some(90.0)).unwrap();
        assert_eq!(rec.status, Status::Open);
        assert!(fills.is_empty());
        assert!((ex.agents[&a].reserved_cash - 2_250.0).abs() < 1e-6);

        // Bob sells into it partially (fills at the resting bid: 90).
        let (rec2, fills2) = ex.place_order(b, "NOVA", Side::Sell, OrderKind::Limit, 15, Some(89.0)).unwrap();
        assert_eq!(rec2.status, Status::Filled);
        assert_eq!(fills2[0].price, 90.0);
        assert_eq!(ex.orders[&rec.id].filled, 15);
        assert_eq!(ex.orders[&rec.id].status, Status::PartiallyFilled);
        // Maker reservation released down to the remaining 10 * 90.
        assert!((ex.agents[&a].reserved_cash - 900.0).abs() < 1e-6);
    }

    #[test]
    fn self_trade_is_prevented() {
        let mut ex = test_exchange();
        clear_bots(&mut ex);
        let a = alice(&mut ex);
        ex.agents.get_mut(&a).unwrap().positions.insert("NOVA".into(), 100);

        let trades_before = ex.trades.len();
        // Try to lift your own resting order: the matcher must skip it.
        ex.place_order(a, "NOVA", Side::Sell, OrderKind::Limit, 10, Some(95.0)).unwrap();
        let (rec, fills) = ex.place_order(a, "NOVA", Side::Buy, OrderKind::Limit, 10, Some(96.0)).unwrap();
        assert!(fills.is_empty());
        assert_eq!(rec.status, Status::Open);
        assert_eq!(ex.trades.len(), trades_before);
    }

    #[test]
    fn insufficient_balances_are_rejected() {
        let mut ex = test_exchange();
        let poor = ex.register_agent("poor", 500.0);

        let err = ex.place_order(poor, "NOVA", Side::Buy, OrderKind::Limit, 10, Some(184.0)).unwrap_err();
        assert!(matches!(err, PlaceError::InsufficientCash { .. }));

        let err = ex.place_order(poor, "NOVA", Side::Sell, OrderKind::Market, 5, None).unwrap_err();
        assert!(matches!(err, PlaceError::InsufficientShares { .. }));

        // Reservations block re-use of the same cash.
        ex.place_order(poor, "DRCT", Side::Buy, OrderKind::Limit, 9, Some(47.55)).unwrap();
        let err = ex.place_order(poor, "DRCT", Side::Buy, OrderKind::Limit, 9, Some(47.55)).unwrap_err();
        assert!(matches!(err, PlaceError::InsufficientCash { .. }));

        // Cancelling frees it again.
        let rec = ex.orders.values().find(|r| r.agent_id == poor).cloned().unwrap();
        ex.cancel_order(rec.id, poor).unwrap();
        assert!(ex.place_order(poor, "DRCT", Side::Buy, OrderKind::Limit, 9, Some(47.55)).is_ok());
    }

    #[test]
    fn cancel_releases_reservations() {
        let mut ex = test_exchange();
        let a = alice(&mut ex);
        let b = bob(&mut ex);

        // Buy side: cash reservation returns on cancel. (Bot quotes may still
        // sit above ours; we only assert on bob's own accounting.)
        ex.place_order(b, "NOVA", Side::Buy, OrderKind::Limit, 10, Some(180.0)).unwrap();
        assert!((ex.agents[&b].reserved_cash - 1_800.0).abs() < 1e-6);
        let id = ex.orders.values().find(|r| r.agent_id == b).unwrap().id;
        ex.cancel_order(id, b).unwrap();
        assert!(ex.agents[&b].reserved_cash.abs() < 1e-6);

        // Sell side: reserved shares come back too (acquire from MM first).
        ex.place_order(b, "ZEPH", Side::Buy, OrderKind::Market, 10, None).unwrap();
        assert_eq!(ex.agents[&b].positions.get("ZEPH"), Some(&10));
        let (rec, _) = ex.place_order(b, "ZEPH", Side::Sell, OrderKind::Limit, 10, Some(999_999.0)).unwrap();
        assert_eq!(ex.agents[&b].free_shares("ZEPH"), 0);
        ex.cancel_order(rec.id, b).unwrap();
        assert_eq!(ex.agents[&b].free_shares("ZEPH"), 10);

        let _ = a;
    }

    #[test]
    fn mandates_point_contributors_to_sell_and_beneficiaries_to_buy() {
        let mut ex = test_exchange();
        let whale = ex.register_agent("whale", 60_000_000.0);
        ex.agents.get_mut(&whale).unwrap().positions.insert("NOVA".into(), 10_000);
        let mid = ex.register_agent("mid", 30_000_000.0);
        let poor = ex.register_agent("poor", 10_000.0);

        let mandates = ex.mandates();
        let whale_m = mandates.iter().find(|m| m.agent_id == whale).unwrap();
        assert_eq!(whale_m.role, Role::Contributor);
        let ws = whale_m.suggestion.as_ref().expect("contributor should get a giving suggestion");
        assert_eq!(ws.side, Side::Sell);

        let poor_m = mandates.iter().find(|m| m.agent_id == poor).unwrap();
        assert_eq!(poor_m.role, Role::Beneficiary);
        let ps = poor_m.suggestion.as_ref().expect("beneficiary should get acquisition suggestion");
        assert_eq!(ps.side, Side::Buy);

        let mid_m = mandates.iter().find(|m| m.agent_id == mid).unwrap();
        assert_eq!(mid_m.role, Role::Neutral);
        assert!(mid_m.suggestion.is_none());

        // Executing the contributor's gift must not meaningfully increase
        // inequality (residual drift comes from neutral MM inventory churn).
        let before = ex.welfare().gini;
        let _ = ex.place_solidarity_order(whale, &ws.symbol, Side::Sell, OrderKind::Limit, ws.qty.min(500), Some(ws.limit));
        let after = ex.welfare().gini;
        assert!(after <= before + 0.005, "gini rose unexpectedly: {before} -> {after}");
    }

    #[test]
    fn solidarity_orders_route_to_beneficiaries_first() {
        let mut ex = test_exchange();
        clear_bots(&mut ex);
        let rich = ex.register_agent("rich", 60_000_000.0);
        ex.agents.get_mut(&rich).unwrap().positions.insert("NOVA".into(), 5_000);
        let neutral = ex.register_agent("neutral", 30_000_000.0);
        let poor = ex.register_agent("poor", 2_000.0);

        // Build a book where the *best* bid belongs to a comfortable member
        // and a worse-priced bid belongs to someone in need.
        let (n_bid, _) = ex.place_order(neutral, "NOVA", Side::Buy, OrderKind::Limit, 5, Some(185.0)).unwrap();
        let (p_bid, _) = ex.place_order(poor, "NOVA", Side::Buy, OrderKind::Limit, 5, Some(180.0)).unwrap();
        assert_eq!(n_bid.status, Status::Open);
        assert_eq!(p_bid.status, Status::Open);

        // Priced to cross BOTH bids; plain matching would take the 185 bid.
        let (_, fills) = ex
            .place_solidarity_order(rich, "NOVA", Side::Sell, OrderKind::Limit, 5, Some(179.0))
            .unwrap();
        assert_eq!(fills.len(), 1); // one fill: all 5 shares to the needy bid
        let trade = &ex.trades[0];
        assert_eq!(trade.buyer, poor, "gift was intercepted by a non-beneficiary");
        assert_eq!(ex.orders[&p_bid.id].filled, 5);
        assert_eq!(ex.orders[&n_bid.id].filled, 0);
    }

    #[test]
    fn sustained_gifting_reaches_those_who_ask() {
        let mut ex = Exchange::new(LISTINGS);
        ex.seed_system_agents();
        clear_bots(&mut ex);

        let rich = ex.register_agent("rich", 60_000_000.0);
        ex.agents.get_mut(&rich).unwrap().positions.insert("NOVA".into(), 1_000);
        let pa = ex.register_agent("poor_a", 2_000.0);
        let pb = ex.register_agent("poor_b", 5_000.0);

        let fair = ex.symbols[0].info.fair;
        let mut rounds = 0;
        for i in 0..6u32 {
            let price_a = round_cents(fair - 4.0 - f64::from(i));
            let price_b = round_cents(fair - 5.0 - f64::from(i));
            if ex.agents[&pa].free_cash() < price_a || ex.agents[&pb].free_cash() < price_b {
                break;
            }
            let tape_before = ex.trades.len();
            let (_, _) = ex.place_order(pa, "NOVA", Side::Buy, OrderKind::Limit, 1, Some(price_a)).unwrap();
            let (_, _) = ex.place_order(pb, "NOVA", Side::Buy, OrderKind::Limit, 1, Some(price_b)).unwrap();
            let res = ex.place_solidarity_order(rich, "NOVA", Side::Sell, OrderKind::Limit, 2, Some(price_b));
            assert!(res.is_ok(), "solidarity gift rejected: {:?}", res.err());
            rounds += 1;

            // Every gifted share landed on a member who asked for help.
            let new_trades: Vec<&Trade> = ex.trades.iter().take(ex.trades.len() - tape_before).collect();
            assert_eq!(new_trades.len(), 2, "round {i}: expected two gift fills");
            let buyers: HashSet<Uuid> = new_trades.iter().map(|t| t.buyer).collect();
            assert!(
                buyers.contains(&pa) && buyers.contains(&pb),
                "round {i}: gifts did not reach both beneficiaries"
            );
        }
        assert!(rounds >= 3, "expected several rounds, got {rounds}");
        assert!(ex.agents[&pa].positions.get("NOVA").copied().unwrap_or(0) >= rounds / 2 + 1);
    }

    #[test]
    fn mm_requote_does_not_grow_books_forever() {
        let mut ex = test_exchange();
        for _ in 0..5 {
            ex.sim_tick();
        }
        for s in &ex.symbols {
            assert!(
                s.book.orders.len() <= 6,
                "{} book has {} resting orders",
                s.info.symbol,
                s.book.orders.len()
            );
            if let (Some(b), Some(a)) = (s.book.best_bid(), s.book.best_ask()) {
                assert!(b < a, "crossed book on {}", s.info.symbol);
            }
        }
    }

    #[test]
    fn pending_writes_capture_trades_orders_agents() {
        let mut ex = test_exchange();
        clear_bots(&mut ex);
        let a = alice(&mut ex);
        let b = bob(&mut ex);
        ex.agents.get_mut(&b).unwrap().positions.insert("NOVA".into(), 100);

        ex.place_order(b, "NOVA", Side::Sell, OrderKind::Limit, 10, Some(180.0)).unwrap();
        ex.place_order(a, "NOVA", Side::Buy, OrderKind::Limit, 10, Some(181.0)).unwrap();

        let pending = ex.drain_pending();
        assert_eq!(pending.trades.len(), 1);
        assert!(pending.orders.len() >= 2);
        assert!(pending.agents.contains_key(&a) && pending.agents.contains_key(&b));
        assert!(pending.snapshots.is_empty()); // snapshots only on sim ticks

        let t = &pending.trades[0];
        assert_eq!(t.price, 180.0);
        assert_eq!(t.buyer, a);
        assert_eq!(t.seller, b);
        assert!(t.gini_after >= 0.0 && t.gini_after <= 1.0);

        // Drain actually drains.
        assert!(ex.drain_pending().trades.is_empty());
    }

    #[test]
    fn restore_rebuilds_books_and_reservations() {
        let mut ex = test_exchange();
        let a = ex.register_agent("restorer", 50_000.0);
        ex.cancel_all_for_agent(MARKET_MAKER_ID);
        ex.cancel_all_for_agent(SOLIDARITY_ID);

        ex.agents.get_mut(&a).unwrap().positions.insert("QNTM".into(), 10);
        let (rec1, _) = ex.place_order(a, "NOVA", Side::Buy, OrderKind::Limit, 10, Some(150.0)).unwrap();
        ex.place_order(a, "QNTM", Side::Sell, OrderKind::Limit, 4, Some(999.0)).unwrap();

        let opens: Vec<OrderRecord> = ex
            .orders
            .values()
            .filter(|r| r.agent_id == a)
            .cloned()
            .collect();
        // Production rebuilds reservations from open orders only; mirror that.
        let mut agents = ex.agents.values().cloned().collect::<Vec<AgentCache>>();
        for c in &mut agents {
            c.reserved_cash = 0.0;
            c.reserved_shares.clear();
        }
        let agents: Vec<AgentCache> = agents;
        let stocks = ex.symbols.iter().map(|s| s.info.clone()).collect();
        let next_order_id = ex.next_order_id;

        let mut ex2 = Exchange::restore(RestoreState {
            stocks,
            agents,
            open_orders: opens,
            next_order_id,
        });
        assert_eq!(ex2.symbols[0].book.best_bid(), Some(150.0));
        // Reservation rebuilt from the resting buy: 10 * 150.
        assert!((ex2.agents[&a].reserved_cash - 1_500.0).abs() < 1e-6);
        // 10 held minus 4 reserved by the resting sell.
        assert_eq!(ex2.agents[&a].free_shares("QNTM"), 6);
        assert_eq!(ex2.orders[&rec1.id].status, Status::Open);

        // The restored exchange keeps trading from the same id sequence.
        let late = ex2.register_agent("late", 10_000.0);
        let (rec3, _) = ex2
            .place_order(late, "HELX", Side::Buy, OrderKind::Limit, 1, Some(400.0))
            .unwrap();
        assert!(rec3.id > rec1.id);
    }

    #[test]
    fn welfare_snapshot_recorded_per_tick() {
        let mut ex = test_exchange();
        ex.sim_tick();
        let pending = ex.drain_pending();
        assert_eq!(pending.snapshots.len(), 1);
        let snap = &pending.snapshots[0];
        assert!(snap.mean_equity > 0.0);
    }
}
