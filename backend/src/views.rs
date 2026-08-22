//! Shared read-models used by both REST handlers and the WebSocket stream.

use serde::Serialize;
use uuid::Uuid;

use crate::engine::{
    BookView, Exchange, Mandate, OrderRecord, Role, StockView, Trade, TournamentView, Welfare,
    WelfareSnapshot,
};

pub fn role_of(deviation: f64) -> Role {
    if deviation > crate::engine::ROLE_THRESHOLD {
        Role::Contributor
    } else if deviation < -crate::engine::ROLE_THRESHOLD {
        Role::Beneficiary
    } else {
        Role::Neutral
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct AgentSummary {
    pub id: Uuid,
    pub name: String,
    pub is_bot: bool,
    pub cash: f64,
    pub equity: f64,
    pub deviation: f64,
    pub role: Role,
}

pub fn summaries(ex: &Exchange) -> Vec<AgentSummary> {
    let marks = ex.marks();
    let total: f64 = ex.agents.values().map(|a| a.equity(&marks)).sum();
    let mean = if ex.agents.is_empty() {
        0.0
    } else {
        total / ex.agents.len() as f64
    };
    let mut rows: Vec<AgentSummary> = ex
        .agents
        .values()
        .map(|a| {
            let equity = a.equity(&marks);
            let deviation = if mean > 0.0 { (equity - mean) / mean } else { 0.0 };
            AgentSummary {
                id: a.id,
                name: a.name.clone(),
                is_bot: a.is_bot,
                cash: a.cash,
                equity,
                deviation,
                role: role_of(deviation),
            }
        })
        .collect();
    rows.sort_by(|a, b| b.equity.partial_cmp(&a.equity).unwrap());
    rows
}

#[derive(Debug, Clone, Serialize)]
pub struct PositionView {
    pub symbol: String,
    pub qty: i64,
    pub reserved: u32,
    pub free: i64,
    pub mark: f64,
    pub value: f64,
}

#[derive(Debug, Clone, Serialize)]
pub struct AgentDetail {
    pub id: Uuid,
    pub name: String,
    pub is_bot: bool,
    pub cash: f64,
    pub reserved_cash: f64,
    pub free_cash: f64,
    pub equity: f64,
    pub role: Role,
    pub mandate: Mandate,
    pub positions: Vec<PositionView>,
    pub open_orders: Vec<OrderRecord>,
}

pub fn build_agent_detail(ex: &Exchange, id: Uuid) -> Option<AgentDetail> {
    let marks = ex.marks();
    let cache = ex.agents.get(&id)?.clone();

    let equity = cache.equity(&marks);
    let total: f64 = ex.agents.values().map(|x| x.equity(&marks)).sum();
    let mean = if ex.agents.is_empty() {
        0.0
    } else {
        total / ex.agents.len() as f64
    };
    let deviation = if mean > 0.0 { (equity - mean) / mean } else { 0.0 };

    let mandate = ex.mandates().into_iter().find(|m| m.agent_id == id)?;

    let positions: Vec<PositionView> = cache
        .positions
        .iter()
        .filter_map(|(sym, qty)| {
            let reserved = cache.reserved_shares.get(sym).copied().unwrap_or(0);
            if *qty == 0 && reserved == 0 {
                return None;
            }
            let mark = marks.get(sym).copied().unwrap_or(0.0);
            Some(PositionView {
                symbol: sym.clone(),
                qty: *qty,
                reserved,
                free: qty.saturating_sub(reserved as i64),
                mark,
                value: *qty as f64 * mark,
            })
        })
        .collect();

    use crate::engine::Status;
    let mut open_orders: Vec<OrderRecord> = ex
        .orders
        .values()
        .filter(|r| r.agent_id == id && matches!(r.status, Status::Open | Status::PartiallyFilled))
        .cloned()
        .collect();
    open_orders.sort_by_key(|r| r.id);

    Some(AgentDetail {
        id: cache.id,
        name: cache.name.clone(),
        is_bot: cache.is_bot,
        cash: cache.cash,
        reserved_cash: cache.reserved_cash,
        free_cash: cache.free_cash(),
        equity,
        role: role_of(deviation),
        mandate,
        positions,
        open_orders,
    })
}

// ---------------------------------------------------------------------------
// Live WebSocket frame
// ---------------------------------------------------------------------------

/// One push frame of the live feed. Core fields arrive every tick; `mandates`
/// and `history` are refreshed on extended frames; `desk` is present only when
/// the client subscribed with an `agent_id`.
#[derive(Debug, Clone, Serialize)]
pub struct LiveFrame {
    #[serde(rename = "type")]
    pub kind: &'static str,
    pub seq: u64,
    pub stocks: Vec<StockView>,
    pub book: Option<BookView>,
    pub tape: Vec<Trade>,
    pub agents: Vec<AgentSummary>,
    pub welfare: Welfare,
    pub tournament: Option<TournamentView>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mandates: Option<Vec<Mandate>>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub history: Vec<WelfareSnapshot>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub desk: Option<AgentDetail>,
}

pub fn build_frame(
    ex: &Exchange,
    symbol: &str,
    agent_id: Option<Uuid>,
    extended: bool,
    seq: u64,
) -> LiveFrame {
    let history: Vec<WelfareSnapshot> = ex.welfare_history.iter().cloned().collect();
    LiveFrame {
        kind: "snapshot",
        seq,
        stocks: ex.stock_views(),
        book: ex.book_view(symbol, 10),
        tape: ex.tape(40),
        agents: summaries(ex),
        welfare: ex.welfare(),
        tournament: ex.active_tournament_view(),
        mandates: if extended { Some(ex.mandates()) } else { None },
        history,
        desk: agent_id.and_then(|id| build_agent_detail(ex, id)),
    }
}
