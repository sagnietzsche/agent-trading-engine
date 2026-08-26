package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newTestServer builds a server with a fresh exchange and no database (only
// used for endpoints that don't touch Postgres).
func newTestServer() *httptest.Server {
	ex := FreshSimulated()
	srv := &server{
		ctx: context.Background(),
		ex:  &lockedExchange{ex: ex},
	}
	ts := httptest.NewServer(srv.routes())
	t := ts.Client()
	t.Timeout = 5 * time.Second
	return ts
}

func getJSON(t *testing.T, ts *httptest.Server, path string, wantStatus int) any {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, wantStatus, body)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("GET %s: bad json: %v", path, err)
	}
	return v
}

func getObj(t *testing.T, ts *httptest.Server, path string, wantStatus int) map[string]any {
	t.Helper()
	v := getJSON(t, ts, path, wantStatus)
	obj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("GET %s: expected object, got %T", path, v)
	}
	return obj
}

func TestReadEndpointsJSONContract(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// GET /api/stocks — array of StockView objects.
	resp, err := ts.Client().Get(ts.URL + "/api/stocks")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var stocks []map[string]any
	if err := json.Unmarshal(body, &stocks); err != nil {
		t.Fatalf("stocks: %v", err)
	}
	if len(stocks) != len(Listings) {
		t.Fatalf("stocks = %d, want %d", len(stocks), len(Listings))
	}
	s := stocks[0]
	for _, k := range []string{"symbol", "name", "fair", "last_trade", "prev_close", "bid", "ask"} {
		if _, ok := s[k]; !ok {
			t.Fatalf("stock missing key %q: %v", k, s)
		}
	}

	// GET /api/book/NOVA?levels=3 — bids/asks are [price, qty] arrays.
	b := getObj(t, ts, "/api/book/NOVA?levels=3", 200)
	for _, side := range []string{"bids", "asks"} {
		levels, ok := b[side].([]any)
		if !ok {
			t.Fatalf("book %s not an array", side)
		}
		if len(levels) == 0 {
			t.Fatalf("book %s empty — MM quotes should exist", side)
		}
		lvl, ok := levels[0].([]any)
		if !ok || len(lvl) != 2 {
			t.Fatalf("book %s level not [price, qty]: %v", side, levels[0])
		}
		if _, isNum := lvl[0].(float64); !isNum {
			t.Fatalf("book level price not a number: %v", lvl[0])
		}
	}

	// GET /api/agents — leaderboard array.
	agents := getJSON(t, ts, "/api/agents", 200)
	if _, ok := agents.([]any); !ok {
		t.Fatalf("agents: expected array, got %T", agents)
	}

	// GET /api/snapshot — full aggregate shape.
	snap := getObj(t, ts, "/api/snapshot?symbol=NOVA", 200)
	for _, k := range []string{"welfare", "stocks", "book", "tape", "agents", "tournament", "chat"} {
		if _, ok := snap[k]; !ok {
			t.Fatalf("snapshot missing key %q", k)
		}
	}

	// GET /api/chat — the floor chatroom (seeded agents chatter on boot).
	chat := getJSON(t, ts, "/api/chat?limit=10", 200)
	if msgs, ok := chat.([]any); !ok || len(msgs) == 0 {
		t.Fatalf("chat: expected non-empty message list, got %v", chat)
	}

	w := snap["welfare"].(map[string]any)
	for _, k := range []string{"gini", "metric", "metric_value", "total_equity", "mean_equity", "gini_target"} {
		if _, ok := w[k]; !ok {
			t.Fatalf("welfare missing key %q", k)
		}
	}
	if w["metric"] != "gini" {
		t.Fatalf("default welfare metric = %v, want gini", w["metric"])
	}

	// GET /api/trades — array.
	if trades := getJSON(t, ts, "/api/trades?limit=5", 200); trades == nil {
		t.Fatal("trades: nil body")
	}

	// GET /api/tournaments — array (empty is fine).
	if tournaments := getJSON(t, ts, "/api/tournaments", 200); tournaments == nil {
		t.Fatal("tournaments: nil body")
	}

	// Unknown symbol book → 404 {"error": ...}.
	errResp := getObj(t, ts, "/api/book/NOPE", 404)
	if errResp["error"] == nil {
		t.Fatal("expected error body on unknown symbol")
	}
}

func TestChatSayAndAnnounce(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Register a real agent.
	var agentID string
	{
		resp, err := ts.Client().Post(ts.URL+"/api/agents", "application/json", strings.NewReader(`{"name":"chatty"}`))
		if err != nil {
			t.Fatalf("create agent: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create agent = %d: %s", resp.StatusCode, body)
		}
		var created map[string]any
		if err := json.Unmarshal(body, &created); err != nil {
			t.Fatal(err)
		}
		agentID = created["agent_id"].(string)
	}

	// POST /api/chat as that agent.
	resp, err := ts.Client().Post(ts.URL+"/api/chat", "application/json",
		strings.NewReader(fmt.Sprintf(`{"agent_id":"%s","text":" hello floor! "}`, agentID)))
	if err != nil {
		t.Fatalf("say: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("say = %d: %s", resp.StatusCode, body)
	}
	var said map[string]any
	if err := json.Unmarshal(body, &said); err != nil {
		t.Fatal(err)
	}
	if said["name"] != "chatty" || said["text"] != "hello floor!" {
		t.Fatalf("say response = %v", said)
	}

	// The message shows up in the feed, newest first.
	chat := getJSON(t, ts, "/api/chat?limit=5", 200).([]any)
	if len(chat) == 0 || chat[0].(map[string]any)["text"] != "hello floor!" {
		t.Fatalf("chat feed = %v", chat)
	}

	// An announcement draws scripted replies from the system agents.
	resp, err = ts.Client().Post(ts.URL+"/api/admin/announce", "application/json",
		strings.NewReader(`{"text":"Everyone give 5% of your wealth"}`))
	if err != nil {
		t.Fatalf("announce: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("announce = %d: %s", resp.StatusCode, body)
	}
	var posted []map[string]any
	if err := json.Unmarshal(body, &posted); err != nil {
		t.Fatal(err)
	}
	var sawBotReply bool
	for _, m := range posted {
		if m["name"] == "solidarity_bot" || m["name"] == "market_maker" {
			sawBotReply = true
		}
	}
	if !sawBotReply {
		t.Fatalf("announce drew no bot reply: %v", posted)
	}

	// Unknown agent cannot chat.
	resp, err = ts.Client().Post(ts.URL+"/api/chat", "application/json",
		strings.NewReader(`{"agent_id":"11111111-1111-1111-1111-111111111111","text":"nope"}`))
	if err != nil {
		t.Fatalf("say unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown-agent say = %d, want 400", resp.StatusCode)
	}
}

func TestWSProtocol(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// First frame is a snapshot with the core fields.
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("frame json: %v", err)
	}
	if frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v", frame["type"])
	}
	for _, k := range []string{"seq", "stocks", "book", "tape", "agents", "welfare", "tournament"} {
		if _, ok := frame[k]; !ok {
			t.Fatalf("frame missing key %q", k)
		}
	}

	// Subscribe → ack.
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"subscribe","symbol":"HELX","agent_id":null}`)); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
	_, data, err = c.Read(ctx)
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	var ack map[string]any
	if err := json.Unmarshal(data, &ack); err != nil {
		t.Fatalf("ack json: %v", err)
	}
	if ack["type"] != "subscribed" || ack["symbol"] != "HELX" {
		t.Fatalf("ack = %v", ack)
	}

	// Ping → pong.
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("ping write: %v", err)
	}
	_, data, err = c.Read(ctx)
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	var pong map[string]any
	if err := json.Unmarshal(data, &pong); err != nil {
		t.Fatalf("pong json: %v", err)
	}
	if pong["type"] != "pong" {
		t.Fatalf("pong = %v", pong)
	}
}
