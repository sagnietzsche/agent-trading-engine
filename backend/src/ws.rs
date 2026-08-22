use std::sync::{Arc, Mutex};

use actix_web::{web, HttpRequest, HttpResponse};
use actix_ws::{Message, MessageStream, Session};
use futures_util::StreamExt;
use serde::Deserialize;
use uuid::Uuid;

use crate::views;
use crate::AppState;

const FRAME_INTERVAL_MS: u64 = 1000;
const EXTENDED_EVERY: u64 = 3;

/// Per-connection subscription preferences, mutated by the receiver task and
/// read by the sender task.
#[derive(Default)]
struct Prefs {
    symbol: Mutex<String>,
    agent_id: Mutex<Option<Uuid>>,
}

impl Prefs {
    fn new(symbol: Option<String>, agent_id: Option<Uuid>) -> Self {
        Prefs {
            symbol: Mutex::new(symbol.unwrap_or_else(|| "NOVA".into())),
            agent_id: Mutex::new(agent_id),
        }
    }
}

#[derive(Deserialize)]
pub struct WsQuery {
    symbol: Option<String>,
    agent_id: Option<String>,
}

pub async fn index(
    req: HttpRequest,
    body: web::Payload,
    state: web::Data<AppState>,
    q: web::Query<WsQuery>,
) -> HttpResponse {
    let agent_id = q.agent_id.as_deref().and_then(|s| Uuid::parse_str(s).ok());
    let (response, session, stream) = match actix_ws::handle(&req, body) {
        Ok(v) => v,
        Err(_) => return HttpResponse::BadRequest().finish(),
    };
    let prefs = Arc::new(Prefs::new(q.symbol.clone(), agent_id));

    actix_web::rt::spawn(sender_task(session.clone(), state.clone(), prefs.clone()));
    actix_web::rt::spawn(receiver_task(session, stream, prefs));
    response
}

async fn sender_task(mut session: Session, state: web::Data<AppState>, prefs: Arc<Prefs>) {
    use std::time::Duration;
    let mut interval = actix_web::rt::time::interval(Duration::from_millis(FRAME_INTERVAL_MS));
    let mut seq: u64 = 0;

    loop {
        interval.tick().await;
        let symbol = prefs.symbol.lock().unwrap_or_else(|p| p.into_inner()).clone();
        let agent_id = *prefs.agent_id.lock().unwrap_or_else(|p| p.into_inner());
        seq += 1;
        let extended = seq % EXTENDED_EVERY == 0;

        let frame = {
            let ex = state.lock();
            views::build_frame(&ex, &symbol, agent_id, extended, seq)
        };
        match serde_json::to_string(&frame) {
            Ok(json) => {
                if session.text(json).await.is_err() {
                    break; // peer went away
                }
            }
            Err(e) => log::error!("ws frame encode failed: {e}"),
        }
    }
}

#[derive(Deserialize)]
struct ClientMsg {
    #[serde(rename = "type")]
    kind: String,
    symbol: Option<String>,
    agent_id: Option<String>,
}

async fn receiver_task(mut session: Session, mut stream: MessageStream, prefs: Arc<Prefs>) {
    while let Some(Ok(msg)) = stream.next().await {
        match msg {
            Message::Ping(bytes) => {
                if session.pong(&bytes).await.is_err() {
                    break;
                }
            }
            Message::Text(text) => match serde_json::from_str::<ClientMsg>(&text) {
                Ok(m) if m.kind == "subscribe" => {
                    if let Some(s) = m.symbol {
                        *prefs.symbol.lock().unwrap_or_else(|p| p.into_inner()) = s;
                    }
                    let aid = m
                        .agent_id
                        .as_deref()
                        .and_then(|s| Uuid::parse_str(s).ok());
                    *prefs.agent_id.lock().unwrap_or_else(|p| p.into_inner()) = aid;
                    let ack = serde_json::json!({
                        "type": "subscribed",
                        "symbol": prefs.symbol.lock().map(|g| g.clone()).ok(),
                        "agent_id": aid,
                    });
                    let _ = session.text(ack.to_string()).await;
                }
                Ok(m) if m.kind == "ping" => {
                    let _ = session.text(r#"{"type":"pong"}"#.to_string()).await;
                }
                _ => { /* unknown or malformed messages are ignored */ }
            },
            Message::Close(_) => break,
            _ => {}
        }
    }
    let _ = session.close(None).await;
}
