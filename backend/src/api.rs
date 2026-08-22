use actix_web::{web, HttpResponse};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::engine::{
    BookView, Exchange, Mandate, OrderKind, OrderRecord, PlaceError, Role, Side, Status, Trade,
    Welfare,
};
use crate::store::{self, WelfarePoint};
use crate::AppState;

pub fn routes(cfg: &mut web::ServiceConfig) {
    cfg.service(
        web::scope("/api")
            .route("/health", web::get().to(health))
            .route("/stocks", web::get().to(stocks))
            .route("/book/{symbol}", web::get().to(book))
            .route("/trades", web::get().to(trades))
            .route("/agents", web::post().to(create_agent))
            .route("/agents", web::get().to(list_agents))
            .route("/agents/{id}", web::get().to(agent_detail))
            .route("/orders", web::post().to(place_order))
            .route("/orders/{id}", web::delete().to(cancel_order))
            .route("/welfare", web::get().to(welfare))
            .route("/snapshot", web::get().to(snapshot))
            .route("/admin/reset", web::post().to(reset)),
    );
}

// ---- DTOs -----------------------------------------------------------------

#[derive(Serialize)]
struct Health {
    status: &'static str,
    database: &'static str,
}

#[derive(Serialize)]
struct AgentSummary {
    id: Uuid,
    name: String,
    is_bot: bool,
    cash: f64,
    equity: f64,
    deviation: f64,
    role: Role,
}

#[derive(Serialize)]
struct PositionView {
    symbol: String,
    qty: i64,
    reserved: u32,
    free: i64,
    mark: f64,
    value: f64,
}

#[derive(Serialize)]
struct AgentDetail {
    id: Uuid,
    name: String,
    is_bot: bool,
    cash: f64,
    reserved_cash: f64,
    free_cash: f64,
    equity: f64,
    role: Role,
    mandate: Mandate,
    positions: Vec<PositionView>,
    open_orders: Vec<OrderRecord>,
}

#[derive(Deserialize)]
struct CreateAgentReq {
    name: String,
}

#[derive(Serialize)]
struct CreateAgentResp {
    agent_id: Uuid,
    name: String,
    starting_cash: f64,
}

#[derive(Deserialize)]
struct PlaceOrderReq {
    agent_id: Uuid,
    symbol: String,
    side: String,
    #[serde(default = "default_kind")]
    kind: String,
    qty: u32,
    #[serde(default)]
    price: Option<f64>,
}

fn default_kind() -> String {
    "limit".into()
}

#[derive(Serialize)]
struct FillView {
    trade_id: String,
    price: f64,
    qty: u32,
}

#[derive(Serialize)]
struct PlaceOrderResp {
    order: OrderRecord,
    fills: Vec<FillView>,
    free_cash: f64,
}

#[derive(Serialize)]
struct SnapshotResp {
    welfare: Welfare,
    stocks: Vec<crate::engine::StockView>,
    book: Option<BookView>,
    tape: Vec<Trade>,
    agents: Vec<AgentSummary>,
}

#[derive(Serialize)]
struct WelfareResp {
    welfare: Welfare,
    agents: Vec<Mandate>,
    history: Vec<WelfarePoint>,
}

// ---- helpers ----------------------------------------------------------------

fn place_error(e: PlaceError) -> HttpResponse {
    let msg = match e {
        PlaceError::UnknownSymbol(s) => format!("unknown symbol: {s}"),
        PlaceError::UnknownAgent => "unknown agent".into(),
        PlaceError::InvalidQty => "qty must be > 0".into(),
        PlaceError::InvalidPrice => "price must be > 0 for limit orders".into(),
        PlaceError::InsufficientCash { need, have } => {
            format!("insufficient cash: need {need:.2}, available {have:.2}")
        }
        PlaceError::InsufficientShares { need, have } => {
            format!("insufficient shares: need {need}, available {have}")
        }
        PlaceError::NoLiquidity => "no liquidity on the opposite side of the book".into(),
    };
    HttpResponse::BadRequest().json(serde_json::json!({ "error": msg }))
}

fn role_of(deviation: f64) -> Role {
    if deviation > crate::engine::ROLE_THRESHOLD {
        Role::Contributor
    } else if deviation < -crate::engine::ROLE_THRESHOLD {
        Role::Beneficiary
    } else {
        Role::Neutral
    }
}

fn summaries(ex: &Exchange) -> Vec<AgentSummary> {
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

fn parse_side(s: &str) -> Option<Side> {
    match s.to_ascii_lowercase().as_str() {
        "buy" | "b" | "bid" => Some(Side::Buy),
        "sell" | "s" | "ask" => Some(Side::Sell),
        _ => None,
    }
}

fn parse_kind(s: &str) -> Option<OrderKind> {
    match s.to_ascii_lowercase().as_str() {
        "limit" | "l" => Some(OrderKind::Limit),
        "market" | "m" => Some(OrderKind::Market),
        _ => None,
    }
}

// ---- handlers ------------------------------------------------------------------

async fn health(state: web::Data<AppState>) -> HttpResponse {
    let db_ok = sea_orm::TransactionTrait::begin(&state.db).await.is_ok();
    HttpResponse::Ok().json(Health {
        status: "ok",
        database: if db_ok { "connected" } else { "unavailable" },
    })
}

async fn stocks(state: web::Data<AppState>) -> HttpResponse {
    let view = state.lock().stock_views();
    HttpResponse::Ok().json(view)
}

fn default_levels() -> usize {
    10
}

#[derive(Deserialize)]
struct BookQuery {
    #[serde(default = "default_levels")]
    levels: usize,
}

async fn book(
    state: web::Data<AppState>,
    path: web::Path<String>,
    q: web::Query<BookQuery>,
) -> HttpResponse {
    let symbol = path.into_inner();
    match state.lock().book_view(&symbol, q.levels.clamp(1, 50)) {
        Some(v) => HttpResponse::Ok().json(v),
        None => HttpResponse::NotFound()
            .json(serde_json::json!({ "error": format!("unknown symbol: {symbol}") })),
    }
}

fn default_trades_limit() -> usize {
    50
}

#[derive(Deserialize)]
struct TradesQuery {
    #[serde(default = "default_trades_limit")]
    limit: usize,
    symbol: Option<String>,
}

async fn trades(state: web::Data<AppState>, q: web::Query<TradesQuery>) -> HttpResponse {
    let tape: Vec<Trade> = state
        .lock()
        .tape(q.limit.clamp(1, 400))
        .into_iter()
        .filter(|t| q.symbol.as_ref().is_none_or(|s| *s == t.symbol))
        .collect();
    HttpResponse::Ok().json(tape)
}

async fn create_agent(state: web::Data<AppState>, req: web::Json<CreateAgentReq>) -> HttpResponse {
    let name = req.name.trim();
    if name.is_empty() || name.len() > 64 {
        return HttpResponse::BadRequest()
            .json(serde_json::json!({ "error": "name must be 1..64 characters" }));
    }
    let (id, pending) = {
        let mut ex = state.lock();
        let id = ex.register_agent(name, store::STARTING_CASH);
        let pending = ex.drain_pending();
        (id, pending)
    };
    if let Err(e) = store::flush(&state.db, &pending).await {
        log::error!("flush failed: {e}");
    }
    HttpResponse::Created().json(CreateAgentResp {
        agent_id: id,
        name: name.to_string(),
        starting_cash: store::STARTING_CASH,
    })
}

async fn list_agents(state: web::Data<AppState>) -> HttpResponse {
    let rows = summaries(&state.lock());
    HttpResponse::Ok().json(rows)
}

async fn agent_detail(state: web::Data<AppState>, path: web::Path<Uuid>) -> HttpResponse {
    let id = path.into_inner();
    let detail = build_agent_detail(&state.lock(), id);
    match detail {
        Some(d) => HttpResponse::Ok().json(d),
        None => HttpResponse::NotFound().json(serde_json::json!({ "error": "unknown agent" })),
    }
}

fn build_agent_detail(ex: &Exchange, id: Uuid) -> Option<AgentDetail> {
    let marks = ex.marks();
    let cache = ex.agents.get(&id)?.clone();

    let equity = cache.equity(&marks);
    let total: f64 = ex.agents.values().map(|x| x.equity(&marks)).sum();
    let mean = if ex.agents.is_empty() { 0.0 } else { total / ex.agents.len() as f64 };
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

async fn place_order(state: web::Data<AppState>, req: web::Json<PlaceOrderReq>) -> HttpResponse {
    let Some(side) = parse_side(&req.side) else {
        return HttpResponse::BadRequest()
            .json(serde_json::json!({ "error": "side must be 'buy' or 'sell'" }));
    };
    let Some(kind) = parse_kind(&req.kind) else {
        return HttpResponse::BadRequest()
            .json(serde_json::json!({ "error": "kind must be 'limit' or 'market'" }));
    };

    let (res, pending) = {
        let mut ex = state.lock();
        let r = ex.place_order(req.agent_id, &req.symbol, side, kind, req.qty, req.price);
        let pending = ex.drain_pending();
        (r, pending)
    };

    if let Err(e) = store::flush(&state.db, &pending).await {
        log::error!("flush failed: {e}");
    }

    match res {
        Ok((order, fills)) => {
            let free_cash = {
                let ex = state.lock();
                ex.agents
                    .get(&req.agent_id)
                    .map(|a| a.free_cash())
                    .unwrap_or(0.0)
            };
            HttpResponse::Created().json(PlaceOrderResp {
                order,
                fills: fills
                    .into_iter()
                    .map(|f| FillView {
                        trade_id: f.trade_id,
                        price: f.price,
                        qty: f.qty,
                    })
                    .collect(),
                free_cash,
            })
        }
        Err(e) => place_error(e),
    }
}

async fn cancel_order(
    state: web::Data<AppState>,
    path: web::Path<u64>,
    q: web::Query<std::collections::HashMap<String, String>>,
) -> HttpResponse {
    let order_id = path.into_inner();
    let Some(agent_id) = q.get("agent_id").and_then(|s| Uuid::parse_str(s).ok()) else {
        return HttpResponse::BadRequest()
            .json(serde_json::json!({ "error": "agent_id query param required" }));
    };
    let (result, pending) = {
        let mut ex = state.lock();
        let r = ex.cancel_order(order_id, agent_id);
        let pending = ex.drain_pending();
        (r, pending)
    };
    if let Err(e) = store::flush(&state.db, &pending).await {
        log::error!("flush failed: {e}");
    }
    match result {
        Ok(rec) => HttpResponse::Ok().json(rec),
        Err(msg) => HttpResponse::BadRequest().json(serde_json::json!({ "error": msg })),
    }
}

async fn welfare(state: web::Data<AppState>) -> HttpResponse {
    let (welfare, agents) = {
        let ex = state.lock();
        (ex.welfare(), ex.mandates())
    };
    match store::welfare_history(&state.db, 90).await {
        Ok(history) => HttpResponse::Ok().json(WelfareResp {
            welfare,
            agents,
            history,
        }),
        Err(e) => {
            log::error!("welfare history failed: {e}");
            HttpResponse::Ok().json(WelfareResp {
                welfare,
                agents,
                history: vec![],
            })
        }
    }
}

fn default_symbol() -> String {
    "NOVA".into()
}

#[derive(Deserialize)]
struct SnapshotQuery {
    #[serde(default = "default_symbol")]
    symbol: String,
}

async fn snapshot(state: web::Data<AppState>, q: web::Query<SnapshotQuery>) -> HttpResponse {
    let resp = {
        let ex = state.lock();
        SnapshotResp {
            welfare: ex.welfare(),
            stocks: ex.stock_views(),
            book: ex.book_view(&q.symbol, 10),
            tape: ex.tape(40),
            agents: summaries(&ex),
        }
    };
    HttpResponse::Ok().json(resp)
}

async fn reset(state: web::Data<AppState>) -> HttpResponse {
    if let Err(e) = store::reset_all(&state.db).await {
        log::error!("reset failed: {e}");
        return HttpResponse::InternalServerError()
            .json(serde_json::json!({ "error": format!("reset failed: {e}") }));
    }
    if let Err(e) = store::seed_fresh(&state.db).await {
        log::error!("reseed failed: {e}");
        return HttpResponse::InternalServerError()
            .json(serde_json::json!({ "error": format!("reseed failed: {e}") }));
    }

    let pending = {
        let mut ex = state.lock();
        *ex = Exchange::fresh_simulated();
        ex.drain_pending()
    };
    if let Err(e) = store::flush(&state.db, &pending).await {
        log::error!("flush after reset failed: {e}");
    }
    HttpResponse::Ok().json(serde_json::json!({ "status": "reset complete" }))
}
