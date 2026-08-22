mod api;
mod entities;
mod engine;
mod store;
mod views;
mod ws;

use std::sync::Mutex;
use std::time::Duration;

use actix_web::{web, App, HttpServer};
use sea_orm::DatabaseConnection;

use engine::Exchange;

pub struct AppState {
    pub db: DatabaseConnection,
    pub exchange: Mutex<Exchange>,
}

impl AppState {
    /// Lock the matching engine, recovering from any poisoned mutex: the
    /// in-memory state stays internally consistent because panics cannot
    /// corrupt plain structs.
    pub(crate) fn lock(&self) -> std::sync::MutexGuard<'_, Exchange> {
        self.exchange.lock().unwrap_or_else(|p| p.into_inner())
    }
}

const SIM_INTERVAL_MS: u64 = 1000;

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    dotenvy::dotenv().ok();
    env_logger::init_from_env(env_logger::Env::default().default_filter_or("info"));

    let db = store::connect().await.unwrap_or_else(|e| {
        eprintln!("failed to connect to Postgres: {e}");
        eprintln!("start the database first:  docker compose up -d");
        std::process::exit(1);
    });
    store::migrate(&db).await.expect("failed to run migrations");

    if store::is_empty(&db).await.expect("inspect database") {
        log::info!("empty database: seeding listings and system agents");
        store::seed_fresh(&db).await.expect("seed database");
        let mut ex = Exchange::fresh_simulated();
        let pending = ex.drain_pending();
        store::flush(&db, &pending)
            .await
            .expect("persist opening state");
    }

    let exchange = store::boot_exchange(&db).await.unwrap_or_else(|e| {
        eprintln!("failed to rebuild exchange from Postgres: {e}");
        std::process::exit(1);
    });
    log::info!(
        "restored {} listings, {} agents from postgres",
        exchange.symbols.len(),
        exchange.agents.len()
    );

    let state = web::Data::new(AppState {
        db,
        exchange: Mutex::new(exchange),
    });

    // Market simulation: random-walk fairs, neutral MM quotes, solidarity flow.
    {
        let st = state.clone();
        actix_web::rt::spawn(async move {
            let mut interval =
                actix_web::rt::time::interval(Duration::from_millis(SIM_INTERVAL_MS));
            loop {
                interval.tick().await;
                let pending = {
                    let mut ex = st.lock();
                    ex.sim_tick();
                    ex.drain_pending()
                };
                if let Err(e) = store::flush(&st.db, &pending).await {
                    log::error!("simulation flush failed: {e}");
                }
            }
        });
    }

    let host = std::env::var("HOST").unwrap_or_else(|_| "127.0.0.1".into());
    let port: u16 = std::env::var("PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(8080);

    log::info!("trading engine listening on http://{host}:{port}");
    HttpServer::new(move || App::new().app_data(state.clone()).route("/ws", web::get().to(ws::index))
                .configure(api::routes))
        .bind((host.as_str(), port))?
        .run()
        .await
}
