use actix_web::{web, HttpResponse};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::engine::{
    BookView, Exchange, Mandate, OrderKind, OrderRecord, PlaceError, Side, Trade, Welfare,
};
use crate::store::{self, WelfarePoint};
use crate::views::{build_agent_detail, summaries, AgentSummary};
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
            .route("/tournaments", web::post().to(create_tournament))
            .route("/tournaments", web::get().to(list_tournaments))
            .route("/tournaments/{id}", web::get().to(get_tournament))
            .route("/tournaments/{id}/enter", web::post().to(enter_tournament))
            .route("/tournaments/{id}/start", web::post().to(start_tournament))
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
struct SnapshotResp {
    welfare: Welfare,
    stocks: Vec<crate::engine::StockView>,
    book: Option<BookView>,
    tape: Vec<Trade>,
    agents: Vec<AgentSummary>,
    tournament: Option<crate::engine::TournamentView>,
}

#[derive(Serialize)]
struct WelfareResp {
    welfare: Welfare,
    agents: Vec<Mandate>,
    history: Vec<WelfarePoint>,
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

// ---- tournament DTOs --------------------------------------------------------

#[derive(Deserialize)]
struct CreateTournamentReq {
    name: Option<String>,
    #[serde(default = "default_duration")]
    duration_ticks: u32,
}

fn default_duration() -> u32 {
    90
}

#[derive(Deserialize)]
struct EnterTournamentReq {
    agent_id: Uuid,
    #[serde(default = "default_strategy")]
    strategy: String,
}

fn default_strategy() -> String {
    "custom".into()
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

// ---- market / account handlers ----------------------------------------------

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
            tournament: ex.active_tournament_view(),
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

// ---- tournament handlers ------------------------------------------------------

async fn create_tournament(
    state: web::Data<AppState>,
    req: web::Json<CreateTournamentReq>,
) -> HttpResponse {
    let name = req.name.clone().unwrap_or_else(|| "welfare-games".into());
    let duration = req.duration_ticks.clamp(5, 3600);
    let view = {
        let mut ex = state.lock();
        let id = ex.create_tournament(name.trim(), duration);
        ex.tournament_view(id)
    };
    let Some(view) = view else {
        return HttpResponse::InternalServerError().finish();
    };
    if let Err(e) = store::save_tournament(&state.db, &view).await {
        log::error!("tournament persist failed: {e}");
    }
    HttpResponse::Created().json(view)
}

async fn list_tournaments(state: web::Data<AppState>) -> HttpResponse {
    let views = state.lock().tournament_views();
    HttpResponse::Ok().json(views)
}

async fn get_tournament(state: web::Data<AppState>, path: web::Path<Uuid>) -> HttpResponse {
    let view = state.lock().tournament_view(path.into_inner());
    match view {
        Some(v) => HttpResponse::Ok().json(v),
        None => {
            HttpResponse::NotFound().json(serde_json::json!({ "error": "tournament not found" }))
        }
    }
}

async fn enter_tournament(
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    req: web::Json<EnterTournamentReq>,
) -> HttpResponse {
    let tid = path.into_inner();
    let strategy = req.strategy.trim();
    let res = {
        let mut ex = state.lock();
        let r = ex.enter_tournament(tid, req.agent_id, if strategy.is_empty() { "custom" } else { strategy });
        let v = ex.tournament_view(tid);
        (r, v)
    };
    let (result, view) = res;
    if let Err(msg) = result {
        return HttpResponse::BadRequest().json(serde_json::json!({ "error": msg }));
    }
    if let Some(view) = view {
        if let Err(e) = store::save_tournament(&state.db, &view).await {
            log::error!("tournament persist failed: {e}");
        }
        return HttpResponse::Ok().json(view);
    }
    HttpResponse::NotFound().json(serde_json::json!({ "error": "tournament not found" }))
}

async fn start_tournament(state: web::Data<AppState>, path: web::Path<Uuid>) -> HttpResponse {
    let tid = path.into_inner();
    let res = {
        let mut ex = state.lock();
        let r = ex.start_tournament(tid);
        let v = ex.tournament_view(tid);
        (r, v)
    };
    let (result, view) = res;
    if let Err(msg) = result {
        return HttpResponse::BadRequest().json(serde_json::json!({ "error": msg }));
    }
    if let Some(view) = view {
        if let Err(e) = store::save_tournament(&state.db, &view).await {
            log::error!("tournament persist failed: {e}");
        }
        return HttpResponse::Ok().json(view);
    }
    HttpResponse::NotFound().json(serde_json::json!({ "error": "tournament not found" }))
}
