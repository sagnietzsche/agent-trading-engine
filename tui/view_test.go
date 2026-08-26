package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func fptr(f float64) *float64 { return &f }

func sampleFrame() Frame {
	return Frame{
		Type: "snapshot",
		Seq:  7,
		Stocks: []StockView{
			{Symbol: "NOVA", Name: "Nova Dynamics", Fair: 184.20, Prev: 183.50, Last: fptr(184.20), Bid: fptr(184.10), Ask: fptr(184.30)},
			{Symbol: "ZEPH", Name: "Zephyr Energy", Fair: 63.90, Prev: 64.00, Last: fptr(63.80), Bid: fptr(63.70), Ask: fptr(63.90)},
		},
		Book: &BookView{
			Symbol: "NOVA",
			Bids:   []Level{{184.10, 25}, {184.05, 30}},
			Asks:   []Level{{184.30, 40}, {184.35, 60}},
		},
		Tape: []Trade{
			{ID: "t1", Symbol: "NOVA", Price: 184.11, Qty: 5, BuyerEq: 90_000, SellerEq: 10_000_000, GiniAfter: 0.401, TS: "2026-08-25T21:31:02Z"},
			{ID: "t2", Symbol: "ZEPH", Price: 63.80, Qty: 10, BuyerEq: 8_000_000, SellerEq: 95_000, GiniAfter: 0.412, TS: "2026-08-25T21:31:01Z"},
		},
		Agents: []AgentView{
			{Name: "market_maker", IsBot: true, Equity: 10_000_000, Role: "contributor"},
			{Name: "solidarity_bot", IsBot: true, Equity: 6_100_000, Role: "beneficiary"},
			{Name: "alice", Equity: 100_500, Role: "neutral"},
		},
		Welfare: Welfare{
			Metric:      "nash",
			Gini:        0.412,
			MetricValue: 612_000,
			GiniTarget:  0.20,
			MeanEquity:  1_400_000,
			TotalEquity: 17_000_000,
		},
		History: []WelfarePoint{
			{Gini: 0.40, Metric: "nash"}, {Gini: 0.41, Metric: "nash"}, {Gini: 0.42, Metric: "nash"}, {Gini: 0.412, Metric: "nash"},
		},
		Chat: []ChatMsg{
			{ID: "c2", Name: "solidarity_bot", Kind: "mandate", Text: "✊ Giving 400 NOVA to the bids of the worst-off — mandate fulfilled.", TS: "2026-08-25T21:31:02Z"},
			{ID: "c1", Name: "market_maker", Kind: "market", Text: "⚠ QNTM fell 2.10% this tick — widening the book", TS: "2026-08-25T21:30:59Z"},
			{ID: "c0", Name: "alice", Kind: "chat", Text: "hello floor!", TS: "2026-08-25T21:30:55Z"},
		},
	}
}

func TestViewPick(t *testing.T) {
	m := &Model{base: "http://127.0.0.1:8080", stage: stagePick, sel: 1}
	out := viewPick(m)
	for _, want := range []string{"TRADING ENGINE", "Gini coefficient", "Atkinson index", "Nash social welfare", "backend: http://127.0.0.1:8080"} {
		if !strings.Contains(out, want) {
			t.Fatalf("picker missing %q:\n%s", want, out)
		}
	}
}

func TestViewLiveNash(t *testing.T) {
	m := &Model{base: "http://127.0.0.1:8080", stage: stageLive, metric: "nash", frame: sampleFrame()}
	out := viewLive(m)
	for _, want := range []string{
		"Nash social welfare", "$612k", "inequality 0.41", "target 0.20", "NOVA", "STOCKS", "TIME & SALES", "AGENTS",
		"market_maker", "solidarity_bot", "184.30", "▲", "▼", "[←/→] symbol",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, out)
		}
	}
}

func TestChatPanelAndAnnounceInput(t *testing.T) {
	m := &Model{base: "http://127.0.0.1:8080", stage: stageLive, metric: "nash", frame: sampleFrame(), showChat: true}
	out := viewLive(m)
	for _, want := range []string{"CHAT ROOM", "solidarity_bot:", "Giving 400 NOVA", "market_maker:", "alice:", "hello floor!"} {
		if !strings.Contains(out, want) {
			t.Fatalf("chat panel missing %q:\n%s", want, out)
		}
	}

	// Agents panel is the default; chat replaces it when toggled on.
	m2 := &Model{base: "http://127.0.0.1:8080", stage: stageLive, metric: "nash", frame: sampleFrame()}
	out2 := viewLive(m2)
	if !strings.Contains(out2, "AGENTS") || strings.Contains(out2, "CHAT ROOM") {
		t.Fatalf("default dashboard should show agents, not chat:\n%s", out2)
	}

	// Announce input line appears while composing.
	m3 := &Model{base: "http://127.0.0.1:8080", stage: stageLive, metric: "nash", frame: sampleFrame(), announcing: true, input: "give 5%"}
	out3 := viewLive(m3)
	if !strings.Contains(out3, "📢 announce: give 5%▌") || !strings.Contains(out3, "[enter] send") {
		t.Fatalf("announce input missing:\n%s", out3)
	}
}

func TestEditInput(t *testing.T) {
	in := editInput("giv", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if in != "give" {
		t.Fatalf("append = %q", in)
	}
	in = editInput("give", tea.KeyMsg{Type: tea.KeyBackspace})
	if in != "giv" {
		t.Fatalf("backspace = %q", in)
	}
}

func TestViewLiveGiniAndAtkinson(t *testing.T) {
	f := sampleFrame()
	f.Welfare = Welfare{Metric: "gini", Gini: 0.61, MetricValue: 0.61, GiniTarget: 0.20, MeanEquity: 1_400_000}
	m := &Model{base: "http://127.0.0.1:8080", stage: stageLive, metric: "gini", frame: f}
	out := viewLive(m)
	if !strings.Contains(out, "Gini coefficient") {
		t.Fatalf("gini dashboard missing metric label:\n%s", out)
	}

	f2 := sampleFrame()
	f2.Welfare = Welfare{Metric: "atkinson", Gini: 0.22, MetricValue: 0.22, GiniTarget: 0.20, MeanEquity: 1_400_000}
	m2 := &Model{base: "http://127.0.0.1:8080", stage: stageLive, metric: "atkinson", frame: f2}
	out2 := viewLive(m2)
	if !strings.Contains(out2, "Atkinson index") {
		t.Fatalf("atkinson dashboard missing metric label:\n%s", out2)
	}
}

func TestFmtAndSparkline(t *testing.T) {
	if got := fmtMoney(1_420_000); got != "$1.42M" {
		t.Fatalf("fmtMoney = %s", got)
	}
	if got := fmtMoney(612_000); got != "$612k" {
		t.Fatalf("fmtMoney = %s", got)
	}
	if got := fmtMoney(42); got != "$42" {
		t.Fatalf("fmtMoney = %s", got)
	}
	sp := sparkline([]float64{0.2, 0.4, 0.6, 0.8}, 40)
	if utf8.RuneCountInString(sp) != 40 {
		t.Fatalf("sparkline width = %d, want 40", utf8.RuneCountInString(sp))
	}
	if sparkline([]float64{0.5}, 40) != "collecting history…" {
		t.Fatal("short sparkline should say collecting history")
	}
	if got := shortID("12345678-1234-1234-1234-123456789abc"); got != "123456" {
		t.Fatalf("shortID = %s", got)
	}
	if got := clockOf("2026-08-25T21:31:02Z"); got != "21:31:02" {
		t.Fatalf("clockOf = %s", got)
	}
}
