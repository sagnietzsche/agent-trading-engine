use std::collections::HashMap;

use migration::{Migrator, MigratorTrait};
use rust_decimal::prelude::*;
use rust_decimal::Decimal;
use sea_orm::{
    ColumnTrait, ConnectionTrait, Database, DatabaseConnection, DbBackend,
    DbErr, EntityTrait, PaginatorTrait, QueryFilter, QueryOrder, QuerySelect,
    sea_query::OnConflict, Set, Statement, TransactionTrait,
};
use uuid::Uuid;

use crate::engine::{AgentCache, Exchange, OrderKind, OrderRecord, RestoreState, Side, Status, StockInfo};
use crate::entities::{agents, orders, positions, stocks, trades, welfare_snapshots};

pub const STARTING_CASH: f64 = 100_000.0;
/// (symbol, company name, base price)
pub const LISTINGS: &[(&str, &str, f64)] = &[
    ("NOVA", "Nova Dynamics", 184.20),
    ("QNTM", "Quantum Foundry", 92.75),
    ("HELX", "Helix Biolabs", 341.10),
    ("DRCT", "Direct Commons", 47.55),
    ("ORBT", "Orbital Logistics", 128.40),
    ("ZEPH", "Zephyr Energy", 63.90),
];

fn d2f(d: Decimal) -> f64 {
    d.to_f64().unwrap_or(0.0)
}

fn f2d(x: f64) -> Decimal {
    Decimal::from_f64_retain(x).unwrap_or_default().round_dp(4)
}

pub async fn connect() -> Result<DatabaseConnection, DbErr> {
    let url = std::env::var("DATABASE_URL").expect("DATABASE_URL must be set (.env supported)");
    Database::connect(&url).await
}

pub async fn migrate(db: &DatabaseConnection) -> Result<(), DbErr> {
    Migrator::up(db, None).await
}
/// True when no agents exist yet (fresh database).
pub async fn is_empty(db: &DatabaseConnection) -> Result<bool, DbErr> {
    Ok(agents::Entity::find().count(db).await? == 0)
}

/// Wipe every table. Used by POST /api/admin/reset before a fresh reseed.
pub async fn reset_all(db: &DatabaseConnection) -> Result<(), DbErr> {
    for sql in [
        "DELETE FROM trades",
        "DELETE FROM orders",
        "DELETE FROM positions",
        "DELETE FROM welfare_snapshots",
        "DELETE FROM agents",
        "DELETE FROM stocks",
    ] {
        db.execute(Statement::from_string(DbBackend::Postgres, sql))
            .await?;
    }
    Ok(())
}

/// Insert listings + system agents. Only called when the DB starts empty.
pub async fn seed_fresh(db: &DatabaseConnection) -> Result<(), DbErr> {
    use crate::engine::{MARKET_MAKER_ID, SOLIDARITY_ID};

    for (symbol, name, base) in LISTINGS {
        let s = stocks::ActiveModel {
            symbol: Set(symbol.to_string()),
            name: Set(name.to_string()),
            fair: Set(f2d(*base)),
            prev_close: Set(f2d(*base)),
        };
        stocks::Entity::insert(s)
            .on_conflict(
                OnConflict::column(stocks::Column::Symbol)
                    .do_nothing()
                    .to_owned(),
            )
            .exec(db)
            .await?;
    }

    let bots = [
        (
            MARKET_MAKER_ID,
            "market_maker",
            10_000_000.0,
            0u32,
        ),
        (SOLIDARITY_ID, "solidarity_bot", 6_000_000.0, 40_000),
    ];
    for (id, name, cash, inv) in bots {
        let a = agents::ActiveModel {
            id: Set(id),
            name: Set(name.to_string()),
            is_bot: Set(true),
            cash: Set(f2d(cash)),
            reserved_cash: Set(f2d(0.0)),
            created_at: Set(chrono::Utc::now()),
        };
        agents::Entity::insert(a)
            .on_conflict(OnConflict::column(agents::Column::Id).do_nothing().to_owned())
            .exec(db)
            .await?;
        for (symbol, _, _) in LISTINGS {
            if inv == 0 {
                continue;
            }
            let p = positions::ActiveModel {
                agent_id: Set(id),
                symbol: Set(symbol.to_string()),
                qty: Set(inv as i32),
            };
            positions::Entity::insert(p)
                .on_conflict(
                    OnConflict::columns([positions::Column::AgentId, positions::Column::Symbol])
                        .update_column(positions::Column::Qty)
                        .to_owned(),
                )
                .exec(db)
                .await?;
        }
    }
    Ok(())
}

struct LoadedRow {
    agent_rows: Vec<agents::Model>,
    stock_rows: Vec<stocks::Model>,
    position_rows: Vec<positions::Model>,
    open_orders: Vec<orders::Model>,
    max_order_id: i64,
    last_trades: HashMap<String, f64>,
}

async fn load_rows(db: &DatabaseConnection) -> Result<LoadedRow, DbErr> {
    let agent_rows = agents::Entity::find().all(db).await?;
    let stock_rows = stocks::Entity::find().all(db).await?;
    let position_rows = positions::Entity::find().all(db).await?;
    let open_orders = orders::Entity::find()
        .filter(orders::Column::Status.is_in(["open", "partially_filled"]))
        .all(db)
        .await?;
    let max_order_id = orders::Entity::find()
        .select_only()
        .column_as(orders::Column::Id.max(), "max")
        .into_tuple::<Option<i64>>()
        .one(db)
        .await?
        .flatten()
        .unwrap_or(0);
    let recent = trades::Entity::find()
        .order_by_desc(trades::Column::Ts)
        .limit(1000)
        .all(db)
        .await?;
    let mut last_trades = HashMap::new();
    for t in recent {
        last_trades
            .entry(t.symbol.clone())
            .or_insert_with(|| d2f(t.price));
    }
    Ok(LoadedRow {
        agent_rows,
        stock_rows,
        position_rows,
        open_orders,
        max_order_id,
        last_trades,
    })
}

fn to_restore(state: LoadedRow) -> RestoreState {
    let mut caches: HashMap<Uuid, AgentCache> = state
        .agent_rows
        .iter()
        .map(|a| {
            (
                a.id,
                AgentCache {
                    id: a.id,
                    name: a.name.clone(),
                    is_bot: a.is_bot,
                    cash: d2f(a.cash),
                    reserved_cash: d2f(a.reserved_cash),
                    positions: HashMap::new(),
                    reserved_shares: HashMap::new(),
                },
            )
        })
        .collect();
    // Stored reserved values are informational only; reservations are rebuilt
    // from open orders by Exchange::restore, so zero them out here.
    for c in caches.values_mut() {
        c.reserved_cash = 0.0;
    }
    for p in &state.position_rows {
        if let Some(c) = caches.get_mut(&p.agent_id) {
            c.positions.insert(p.symbol.clone(), p.qty as i64);
        }
    }

    let stocks_vec: Vec<StockInfo> = state
        .stock_rows
        .iter()
        .map(|s| StockInfo {
            symbol: s.symbol.clone(),
            name: s.name.clone(),
            fair: d2f(s.fair),
            prev_close: d2f(s.prev_close),
            last_trade: state.last_trades.get(&s.symbol).copied(),
        })
        .collect();

    let opens: Vec<OrderRecord> = state
        .open_orders
        .iter()
        .map(|o| OrderRecord {
            id: o.id as u64,
            agent_id: o.agent_id,
            symbol: o.symbol.clone(),
            side: match o.side.as_str() {
                "sell" => Side::Sell,
                _ => Side::Buy,
            },
            kind: match o.kind.as_str() {
                "market" => OrderKind::Market,
                _ => OrderKind::Limit,
            },
            price: o.price.map(d2f),
            qty: o.qty.max(0) as u32,
            filled: o.filled.clamp(0, o.qty.max(0)) as u32,
            status: Status::Open,
            created_at: o.created_at.to_rfc3339(),
        })
        .collect();

    RestoreState {
        stocks: stocks_vec,
        agents: caches.into_values().collect(),
        open_orders: opens,
        next_order_id: (state.max_order_id as u64) + 1,
    }
}

/// Build the in-memory exchange from whatever is currently in Postgres.
pub async fn boot_exchange(db: &DatabaseConnection) -> Result<Exchange, DbErr> {
    let rows = load_rows(db).await?;
    let restored = to_restore(rows);
    Ok(Exchange::restore(restored))
}

/// Write-through persistence for everything the engine mutated in one step.
pub async fn flush(db: &DatabaseConnection, pending: &crate::engine::Pending) -> Result<(), DbErr> {
    if pending.agents.is_empty()
        && pending.positions.is_empty()
        && pending.orders.is_empty()
        && pending.trades.is_empty()
        && pending.snapshots.is_empty()
    {
        return Ok(());
    }
    let txn = db.begin().await?;

    for cache in pending.agents.values() {
        let am = agents::ActiveModel {
            id: Set(cache.id),
            name: Set(cache.name.clone()),
            is_bot: Set(cache.is_bot),
            cash: Set(f2d(cache.cash)),
            reserved_cash: Set(f2d(cache.reserved_cash)),
            created_at: Set(chrono::Utc::now()),
        };
        agents::Entity::insert(am)
            .on_conflict(
                OnConflict::column(agents::Column::Id)
                    .update_columns([
                        agents::Column::Name,
                        agents::Column::IsBot,
                        agents::Column::Cash,
                        agents::Column::ReservedCash,
                    ])
                    .to_owned(),
            )
            .exec(&txn)
            .await?;
    }

    for ((agent_id, symbol), qty) in &pending.positions {
        let am = positions::ActiveModel {
            agent_id: Set(*agent_id),
            symbol: Set(symbol.clone()),
            qty: Set(*qty as i32),
        };
        positions::Entity::insert(am)
            .on_conflict(
                OnConflict::columns([positions::Column::AgentId, positions::Column::Symbol])
                    .update_column(positions::Column::Qty)
                    .to_owned(),
            )
            .exec(&txn)
            .await?;
    }

    for rec in pending.orders.values() {
        let am = orders::ActiveModel {
            id: Set(rec.id as i64),
            agent_id: Set(rec.agent_id),
            symbol: Set(rec.symbol.clone()),
            side: Set(rec.side.as_str().to_string()),
            kind: Set(rec.kind.as_str().to_string()),
            price: Set(rec.price.map(f2d)),
            qty: Set(rec.qty as i32),
            filled: Set(rec.filled as i32),
            status: Set(rec.status.as_str().to_string()),
            created_at: Set(
                chrono::DateTime::parse_from_rfc3339(&rec.created_at)
                    .map(|t| t.with_timezone(&chrono::Utc))
                    .unwrap_or_else(|_| chrono::Utc::now()),
            ),
        };
        orders::Entity::insert(am)
            .on_conflict(
                OnConflict::column(orders::Column::Id)
                    .update_columns([
                        orders::Column::Filled,
                        orders::Column::Status,
                        orders::Column::Price,
                    ])
                    .to_owned(),
            )
            .exec(&txn)
            .await?;
    }

    for t in &pending.trades {
        let tm = trades::ActiveModel {
            id: Set(Uuid::parse_str(&t.id).unwrap_or_default()),
            symbol: Set(t.symbol.clone()),
            price: Set(f2d(t.price)),
            qty: Set(t.qty as i32),
            buyer: Set(t.buyer),
            seller: Set(t.seller),
            taker_order: Set(t.taker_order as i64),
            buyer_equity: Set(f2d(t.buyer_equity)),
            seller_equity: Set(f2d(t.seller_equity)),
            gini_after: Set(f2d(t.gini_after)),
            ts: Set(
                chrono::DateTime::parse_from_rfc3339(&t.ts)
                    .map(|x| x.with_timezone(&chrono::Utc))
                    .unwrap_or_else(|_| chrono::Utc::now()),
            ),
        };
        trades::Entity::insert(tm)
            .on_conflict(OnConflict::column(trades::Column::Id).do_nothing().to_owned())
            .exec(&txn)
            .await?;
    }

    for s in &pending.snapshots {
        let sm = welfare_snapshots::ActiveModel {
            id: Default::default(),
            gini: Set(f2d(s.gini)),
            total_equity: Set(f2d(s.total_equity)),
            mean_equity: Set(f2d(s.mean_equity)),
            ts: Set(
                chrono::DateTime::parse_from_rfc3339(&s.ts)
                    .map(|x| x.with_timezone(&chrono::Utc))
                    .unwrap_or_else(|_| chrono::Utc::now()),
            ),
        };
        welfare_snapshots::Entity::insert(sm).exec(&txn).await?;
    }

    txn.commit().await
}

#[derive(Debug, Clone, serde::Serialize)]
pub struct WelfarePoint {
    pub gini: f64,
    pub total_equity: f64,
    pub mean_equity: f64,
    pub ts: String,
}

/// Recent welfare trend, newest last, for charting.
pub async fn welfare_history(
    db: &DatabaseConnection,
    limit: u64,
) -> Result<Vec<WelfarePoint>, DbErr> {
    let rows = welfare_snapshots::Entity::find()
        .order_by_asc(welfare_snapshots::Column::Ts)
        .limit(Some(limit))
        .all(db)
        .await?;
    // Keep only the most recent `limit` points in chronological order.
    let skip = rows.len().saturating_sub(limit as usize);
    Ok(rows
        .into_iter()
        .skip(skip)
        .map(|r| WelfarePoint {
            gini: d2f(r.gini),
            total_equity: d2f(r.total_equity),
            mean_equity: d2f(r.mean_equity),
            ts: r.ts.to_rfc3339(),
        })
        .collect())
}
