package main

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

func testExchange() *Exchange {
	// Fresh exchange whose opening simulation has been flushed, so tests
	// start with clean pending buffers.
	ex := FreshSimulated()
	ex.DrainPending()
	return ex
}

func clearBots(ex *Exchange) {
	ex.CancelAllForAgent(MarketMakerID)
	ex.CancelAllForAgent(SolidarityID)
	ex.DrainPending()
}

func alice(ex *Exchange) uuid.UUID { return ex.RegisterAgent("alice", 100_000.0) }
func bob(ex *Exchange) uuid.UUID   { return ex.RegisterAgent("bob", 100_000.0) }

func mustPlace(t *testing.T, ex *Exchange, agent uuid.UUID, symbol string, side Side, kind OrderKind, qty uint32, price *float64) (*OrderRecord, []Fill) {
	t.Helper()
	rec, fills, perr := ex.PlaceOrder(agent, symbol, side, kind, qty, price)
	if perr != nil {
		t.Fatalf("place order failed: %+v", perr)
	}
	return rec, fills
}

func TestGiniBasics(t *testing.T) {
	if gini([]float64{5.0}) != 0.0 {
		t.Fatal("single value gini != 0")
	}
	if gini([]float64{100, 100, 100, 100}) != 0.0 {
		t.Fatal("equal gini != 0")
	}
	// [100,200,300,400]: classic textbook case -> 0.25
	g := gini([]float64{400, 100, 300, 200})
	if math.Abs(g-0.25) > 1e-9 {
		t.Fatalf("textbook gini = %v, want 0.25", g)
	}
	if gini([]float64{0, 0, 0, 100}) <= 0.7 {
		t.Fatal("concentrated gini not > 0.7")
	}
}

func TestAtkinsonIndex(t *testing.T) {
	if atkinsonIndex([]float64{5.0}, 0.5) != 0.0 {
		t.Fatal("single value atkinson != 0")
	}
	if atkinsonIndex([]float64{100, 100, 100, 100}, 0.5) != 0.0 {
		t.Fatal("equal atkinson != 0")
	}
	// [100,200,300,400] with ε = 0.5: A = 1 − [(Σ√(x/μ))/n]² ≈ 0.0556.
	a := atkinsonIndex([]float64{400, 100, 300, 200}, 0.5)
	if math.Abs(a-0.055586) > 1e-5 {
		t.Fatalf("atkinson = %v, want ≈0.0556", a)
	}
	// Fully concentrated wealth: three members with nothing, one with all.
	c := atkinsonIndex([]float64{0, 0, 0, 100}, 0.5)
	if math.Abs(c-0.75) > 1e-9 {
		t.Fatalf("concentrated atkinson = %v, want 0.75", c)
	}
	// A regressive transfer (poorer member loses, richer gains) raises the
	// index: inequality measures must be Schur-convex.
	before := atkinsonIndex([]float64{1, 2, 3, 4}, 0.5)
	after := atkinsonIndex([]float64{1, 2, 2.5, 4.5}, 0.5)
	if after <= before {
		t.Fatalf("regressive transfer did not raise atkinson: %v -> %v", before, after)
	}
}

func TestNashSocialWelfareAndDeficit(t *testing.T) {
	// Perfect equality → geometric mean equals mean → deficit 0.
	if d := nashDeficit([]float64{100, 100, 100, 100}); math.Abs(d) > 1e-12 {
		t.Fatalf("equal nash deficit = %v, want 0", d)
	}
	if nashDeficit([]float64{5.0}) != 0.0 {
		t.Fatal("single value nash deficit != 0")
	}
	// [100,200,300,400]: GM = (2.4e9)^(1/4) ≈ 221.34, mean = 250.
	gm := nashSocialWelfare([]float64{400, 100, 300, 200})
	if math.Abs(gm-221.336) > 1e-3 {
		t.Fatalf("nash sw = %v, want ≈221.336", gm)
	}
	d := nashDeficit([]float64{400, 100, 300, 200})
	if math.Abs(d-(1.0-221.336/250.0)) > 1e-4 {
		t.Fatalf("nash deficit = %v, want ≈%v", d, 1.0-221.336/250.0)
	}
	// A single non-positive member collapses Nash social welfare to 0 →
	// deficit pegs at 1 (max inequality).
	if nashDeficit([]float64{0, 100, 100, 100}) != 1.0 {
		t.Fatal("zero member should peg nash deficit at 1")
	}
	if nashSocialWelfare([]float64{0, 100, 100, 100}) != 0.0 {
		t.Fatal("zero member should zero nash sw")
	}
	// Degenerate all-non-positive population is treated as equal (0).
	if d := nashDeficit([]float64{0, 0, 0}); math.Abs(d) > 1e-12 {
		t.Fatalf("all-zero population should read as equality, got %v", d)
	}
	// AM-GM: the deficit never goes negative.
	for _, v := range [][]float64{{1, 2, 3}, {10, 10, 10}, {1, 100, 10000}} {
		if d := nashDeficit(v); d < -1e-12 || d > 1.0+1e-12 {
			t.Fatalf("nash deficit out of [0,1]: %v", d)
		}
	}
}

func TestInequalityDispatchesOnMetric(t *testing.T) {
	vals := []float64{400, 100, 300, 200}
	if inequality(vals, MetricGini) != gini(vals) {
		t.Fatal("gini dispatch mismatch")
	}
	if inequality(vals, MetricAtkinson) != atkinsonIndex(vals, atkinsonEpsilon) {
		t.Fatal("atkinson dispatch mismatch")
	}
	if inequality(vals, MetricNash) != nashDeficit(vals) {
		t.Fatal("nash dispatch mismatch")
	}
	// Unknown/empty metric falls back to gini.
	if inequality(vals, "") != gini(vals) {
		t.Fatal("empty metric should fall back to gini")
	}
	// metricValue surfaces the raw Nash SW only for the nash metric.
	if metricValue(vals, MetricNash, 0.5) != nashSocialWelfare(vals) {
		t.Fatal("nash metric_value should be the geometric mean")
	}
	if metricValue(vals, MetricGini, 0.25) != 0.25 {
		t.Fatal("gini metric_value should equal the index")
	}
}

func TestExchangeUsesSelectedMetric(t *testing.T) {
	ex := NewExchange(Listings, WithMetric(MetricNash))
	ex.SeedSystemAgents()
	ex.DrainPending()

	w := ex.Welfare()
	if w.Metric != MetricNash {
		t.Fatalf("metric = %q, want nash", w.Metric)
	}
	marks := ex.Marks()
	var eqs []float64
	for _, a := range ex.Agents {
		eqs = append(eqs, a.Equity(marks))
	}
	if math.Abs(w.Gini-nashDeficit(eqs)) > 1e-9 {
		t.Fatalf("gini field = %v, want nash deficit %v", w.Gini, nashDeficit(eqs))
	}
	if math.Abs(w.MetricValue-nashSocialWelfare(eqs)) > 1e-9 {
		t.Fatalf("metric_value = %v, want nsw %v", w.MetricValue, nashSocialWelfare(eqs))
	}
	if w.Gini > 1.0 || w.Gini < 0.0 {
		t.Fatalf("nash deficit out of range: %v", w.Gini)
	}

	// A gini instance still reports the plain Gini coefficient.
	ex2 := NewExchange(Listings, WithMetric(MetricGini))
	ex2.SeedSystemAgents()
	w2 := ex2.Welfare()
	if w2.Metric != MetricGini {
		t.Fatalf("metric = %q, want gini", w2.Metric)
	}
	if math.Abs(w2.MetricValue-gini(eqs)) > 1e-9 {
		t.Fatalf("gini metric_value = %v, want %v", w2.MetricValue, gini(eqs))
	}
}

func TestLimitCrossFullyFillsAtMakerPrice(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	a := alice(ex)
	b := bob(ex)

	// Bob has inventory and offers 10 @ 100 (well below the MM quote).
	cache := ex.Agents[b]
	cache.Positions["NOVA"] = 100
	ex.Agents[b] = cache
	mustPlace(t, ex, b, "NOVA", SideSell, KindLimit, 10, fptr(100.0))

	// Alice lifts it with a buy limit at 101 -> trade at bob's 100.
	aOrder, fills := mustPlace(t, ex, a, "NOVA", SideBuy, KindLimit, 10, fptr(101.0))
	if aOrder.Status != StatusFilled {
		t.Fatalf("order status = %s, want filled", aOrder.Status)
	}
	if len(fills) != 1 {
		t.Fatalf("fills = %d, want 1", len(fills))
	}
	if fills[0].Price != 100.0 {
		t.Fatalf("fill price = %v, want 100", fills[0].Price)
	}
	if ex.Symbols[0].Info.LastTrade == nil || *ex.Symbols[0].Info.LastTrade != 100.0 {
		t.Fatalf("last_trade = %v, want 100", ex.Symbols[0].Info.LastTrade)
	}

	// Alice paid exactly cost; reservation fully released.
	aliceCache := ex.Agents[a]
	if math.Abs(aliceCache.Cash-99_000.0) > 1e-6 {
		t.Fatalf("alice cash = %v, want 99000", aliceCache.Cash)
	}
	if math.Abs(aliceCache.ReservedCash) > 1e-6 {
		t.Fatalf("alice reserved = %v, want 0", aliceCache.ReservedCash)
	}
	if aliceCache.Positions["NOVA"] != 10 {
		t.Fatalf("alice NOVA = %v, want 10", aliceCache.Positions["NOVA"])
	}

	// Bob received the cash and his shares left.
	bobCache := ex.Agents[b]
	if math.Abs(bobCache.Cash-101_000.0) > 1e-6 {
		t.Fatalf("bob cash = %v, want 101000", bobCache.Cash)
	}
	if bobCache.Positions["NOVA"] != 90 {
		t.Fatalf("bob NOVA = %v, want 90", bobCache.Positions["NOVA"])
	}
}

func TestPriceTimePriorityAndSweep(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	a := alice(ex)
	b := bob(ex)
	c := ex.RegisterAgent("carol", 100_000.0)

	bc := ex.Agents[b]
	bc.Positions["NOVA"] = 500
	ex.Agents[b] = bc
	cc := ex.Agents[c]
	cc.Positions["NOVA"] = 500
	ex.Agents[c] = cc

	mustPlace(t, ex, b, "NOVA", SideSell, KindLimit, 50, fptr(100.0))
	mustPlace(t, ex, c, "NOVA", SideSell, KindLimit, 50, fptr(100.0)) // same price, later time
	mustPlace(t, ex, b, "NOVA", SideSell, KindLimit, 40, fptr(100.50))

	_, fills := mustPlace(t, ex, a, "NOVA", SideBuy, KindMarket, 130, nil)
	prices := make([]float64, len(fills))
	qtys := make([]uint32, len(fills))
	for i, f := range fills {
		prices[i] = f.Price
		qtys[i] = f.Qty
	}
	if !eqFloats(prices, []float64{100.0, 100.0, 100.5}) {
		t.Fatalf("fill prices = %v", prices)
	}
	if !eqQtys(qtys, []uint32{50, 50, 30}) {
		t.Fatalf("fill qtys = %v", qtys)
	}
	// 10 shares remain at the second level.
	if ask := ex.Symbols[0].Book.BestAsk(); ask == nil || *ask != 100.5 {
		t.Fatalf("best ask = %v, want 100.5", ask)
	}
}

func TestPartialLimitRestsThenFills(t *testing.T) {
	ex := testExchange()
	a := alice(ex)
	b := bob(ex)
	bc := ex.Agents[b]
	bc.Positions["NOVA"] = 100
	ex.Agents[b] = bc
	ex.CancelAllForAgent(MarketMakerID)
	ex.CancelAllForAgent(SolidarityID)

	// Alice's bid is below the market: it rests untouched.
	rec, fills := mustPlace(t, ex, a, "NOVA", SideBuy, KindLimit, 25, fptr(90.0))
	if rec.Status != StatusOpen {
		t.Fatalf("status = %s, want open", rec.Status)
	}
	if len(fills) != 0 {
		t.Fatalf("fills = %d, want 0", len(fills))
	}
	if math.Abs(ex.Agents[a].ReservedCash-2_250.0) > 1e-6 {
		t.Fatalf("reserved = %v, want 2250", ex.Agents[a].ReservedCash)
	}

	// Bob sells into it partially (fills at the resting bid: 90).
	rec2, fills2 := mustPlace(t, ex, b, "NOVA", SideSell, KindLimit, 15, fptr(89.0))
	if rec2.Status != StatusFilled {
		t.Fatalf("bob status = %s, want filled", rec2.Status)
	}
	if fills2[0].Price != 90.0 {
		t.Fatalf("fill price = %v, want 90", fills2[0].Price)
	}
	if ex.Orders[rec.ID].Filled != 15 {
		t.Fatalf("maker filled = %v, want 15", ex.Orders[rec.ID].Filled)
	}
	if ex.Orders[rec.ID].Status != StatusPartiallyFilled {
		t.Fatalf("maker status = %s, want partially_filled", ex.Orders[rec.ID].Status)
	}
	// Maker reservation released down to the remaining 10 * 90.
	if math.Abs(ex.Agents[a].ReservedCash-900.0) > 1e-6 {
		t.Fatalf("reserved = %v, want 900", ex.Agents[a].ReservedCash)
	}
}

func TestSelfTradeIsPrevented(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	a := alice(ex)
	ac := ex.Agents[a]
	ac.Positions["NOVA"] = 100
	ex.Agents[a] = ac

	tradesBefore := len(ex.Trades)
	// Try to lift your own resting order: the matcher must skip it.
	mustPlace(t, ex, a, "NOVA", SideSell, KindLimit, 10, fptr(95.0))
	rec, fills := mustPlace(t, ex, a, "NOVA", SideBuy, KindLimit, 10, fptr(96.0))
	if len(fills) != 0 {
		t.Fatalf("fills = %d, want 0", len(fills))
	}
	if rec.Status != StatusOpen {
		t.Fatalf("status = %s, want open", rec.Status)
	}
	if len(ex.Trades) != tradesBefore {
		t.Fatal("self-trade produced a print")
	}
}

func TestInsufficientBalancesAreRejected(t *testing.T) {
	ex := testExchange()
	poor := ex.RegisterAgent("poor", 500.0)

	_, _, perr := ex.PlaceOrder(poor, "NOVA", SideBuy, KindLimit, 10, fptr(184.0))
	if perr == nil || perr.Kind != ErrInsufficientCash {
		t.Fatalf("expected insufficient cash, got %+v", perr)
	}

	_, _, perr = ex.PlaceOrder(poor, "NOVA", SideSell, KindMarket, 5, nil)
	if perr == nil || perr.Kind != ErrInsufficientShares {
		t.Fatalf("expected insufficient shares, got %+v", perr)
	}

	// Reservations block re-use of the same cash ($400 of $500 locked).
	drctBuy, _ := mustPlace(t, ex, poor, "DRCT", SideBuy, KindLimit, 400, fptr(1.0))
	if drctBuy.Status != StatusOpen {
		t.Fatalf("status = %s, want open", drctBuy.Status)
	}
	_, _, perr = ex.PlaceOrder(poor, "DRCT", SideBuy, KindLimit, 400, fptr(1.0))
	if perr == nil || perr.Kind != ErrInsufficientCash {
		t.Fatalf("expected insufficient cash on re-use, got %+v", perr)
	}

	// Cancelling frees it again.
	if _, err := ex.CancelOrder(drctBuy.ID, poor); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if _, _, perr := ex.PlaceOrder(poor, "DRCT", SideBuy, KindLimit, 400, fptr(1.0)); perr != nil {
		t.Fatalf("re-place after cancel failed: %+v", perr)
	}
}

func TestCancelReleasesReservations(t *testing.T) {
	ex := testExchange()
	b := bob(ex)

	// Buy side: cash reservation returns on cancel.
	mustPlace(t, ex, b, "NOVA", SideBuy, KindLimit, 10, fptr(180.0))
	if math.Abs(ex.Agents[b].ReservedCash-1_800.0) > 1e-6 {
		t.Fatalf("reserved = %v, want 1800", ex.Agents[b].ReservedCash)
	}
	var id uint64
	for _, r := range ex.Orders {
		if r.AgentID == b {
			id = r.ID
			break
		}
	}
	if _, err := ex.CancelOrder(id, b); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if math.Abs(ex.Agents[b].ReservedCash) > 1e-6 {
		t.Fatalf("reserved after cancel = %v, want 0", ex.Agents[b].ReservedCash)
	}

	// Sell side: reserved shares come back too (acquire from MM first).
	mustPlace(t, ex, b, "ZEPH", SideBuy, KindMarket, 10, nil)
	if ex.Agents[b].Positions["ZEPH"] != 10 {
		t.Fatalf("zeph position = %v, want 10", ex.Agents[b].Positions["ZEPH"])
	}
	rec, _ := mustPlace(t, ex, b, "ZEPH", SideSell, KindLimit, 10, fptr(999_999.0))
	if bc := ex.Agents[b]; bc.FreeShares("ZEPH") != 0 {
		t.Fatalf("free zeph = %v, want 0", bc.FreeShares("ZEPH"))
	}
	if _, err := ex.CancelOrder(rec.ID, b); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if bc := ex.Agents[b]; bc.FreeShares("ZEPH") != 10 {
		t.Fatalf("free zeph after cancel = %v, want 10", bc.FreeShares("ZEPH"))
	}
}

func TestMandatesPointContributorsToSellAndBeneficiariesToBuy(t *testing.T) {
	ex := testExchange()
	whale := ex.RegisterAgent("whale", 60_000_000.0)
	wc := ex.Agents[whale]
	wc.Positions["NOVA"] = 10_000
	ex.Agents[whale] = wc
	mid := ex.RegisterAgent("mid", 30_000_000.0)
	poor := ex.RegisterAgent("poor", 10_000.0)

	mandates := ex.Mandates()
	var whaleM, poorM, midM *Mandate
	for i := range mandates {
		switch mandates[i].AgentID {
		case whale:
			whaleM = &mandates[i]
		case poor:
			poorM = &mandates[i]
		case mid:
			midM = &mandates[i]
		}
	}
	if whaleM == nil || whaleM.Role != RoleContributor {
		t.Fatalf("whale role = %+v, want contributor", whaleM)
	}
	if whaleM.Suggestion == nil || whaleM.Suggestion.Side != SideSell {
		t.Fatalf("whale suggestion = %+v, want sell", whaleM.Suggestion)
	}
	if poorM == nil || poorM.Role != RoleBeneficiary {
		t.Fatalf("poor role = %+v, want beneficiary", poorM)
	}
	if poorM.Suggestion == nil || poorM.Suggestion.Side != SideBuy {
		t.Fatalf("poor suggestion = %+v, want buy", poorM.Suggestion)
	}
	if midM == nil || midM.Role != RoleNeutral || midM.Suggestion != nil {
		t.Fatalf("mid = %+v, want neutral without suggestion", midM)
	}

	// Executing the contributor's gift must not meaningfully increase
	// inequality.
	before := ex.Welfare().Gini
	qty := whaleM.Suggestion.Qty
	if qty > 500 {
		qty = 500
	}
	_, _, perr := ex.PlaceSolidarityOrder(whale, whaleM.Suggestion.Symbol, SideSell, KindLimit, qty, &whaleM.Suggestion.Limit)
	if perr != nil {
		t.Fatalf("gift rejected: %+v", perr)
	}
	after := ex.Welfare().Gini
	if after > before+0.005 {
		t.Fatalf("gini rose unexpectedly: %v -> %v", before, after)
	}
}

func TestSolidarityOrdersRouteToBeneficiariesFirst(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	rich := ex.RegisterAgent("rich", 60_000_000.0)
	rc := ex.Agents[rich]
	rc.Positions["NOVA"] = 5_000
	ex.Agents[rich] = rc
	neutral := ex.RegisterAgent("neutral", 30_000_000.0)
	poor := ex.RegisterAgent("poor", 2_000.0)

	// Build a book where the *best* bid belongs to a comfortable member and
	// a worse-priced bid belongs to someone in need.
	nBid, _ := mustPlace(t, ex, neutral, "NOVA", SideBuy, KindLimit, 5, fptr(185.0))
	pBid, _ := mustPlace(t, ex, poor, "NOVA", SideBuy, KindLimit, 5, fptr(180.0))
	if nBid.Status != StatusOpen || pBid.Status != StatusOpen {
		t.Fatalf("bids did not rest: n=%s p=%s", nBid.Status, pBid.Status)
	}

	// Priced to cross BOTH bids; plain matching would take the 185 bid.
	_, fills, perr := ex.PlaceSolidarityOrder(rich, "NOVA", SideSell, KindLimit, 5, fptr(179.0))
	if perr != nil {
		t.Fatalf("solidarity order rejected: %+v", perr)
	}
	if len(fills) != 1 {
		t.Fatalf("fills = %d, want 1", len(fills))
	}
	if ex.Trades[0].Buyer != poor {
		t.Fatal("gift was intercepted by a non-beneficiary")
	}
	if ex.Orders[pBid.ID].Filled != 5 {
		t.Fatalf("poor bid filled = %v, want 5", ex.Orders[pBid.ID].Filled)
	}
	if ex.Orders[nBid.ID].Filled != 0 {
		t.Fatalf("neutral bid filled = %v, want 0", ex.Orders[nBid.ID].Filled)
	}
}

func TestSustainedGiftingReachesThoseWhoAsk(t *testing.T) {
	ex := NewExchange(Listings)
	ex.SeedSystemAgents()
	clearBots(ex)

	rich := ex.RegisterAgent("rich", 60_000_000.0)
	rc := ex.Agents[rich]
	rc.Positions["NOVA"] = 1_000
	ex.Agents[rich] = rc
	pa := ex.RegisterAgent("poor_a", 2_000.0)
	pb := ex.RegisterAgent("poor_b", 5_000.0)

	fair := ex.Symbols[0].Info.Fair
	rounds := 0
	for i := 0; i < 6; i++ {
		priceA := roundCents(fair - 4.0 - float64(i))
		priceB := roundCents(fair - 5.0 - float64(i))
		if paC, pbC := ex.Agents[pa], ex.Agents[pb]; paC.FreeCash() < priceA || pbC.FreeCash() < priceB {
			break
		}
		tapeBefore := len(ex.Trades)
		mustPlace(t, ex, pa, "NOVA", SideBuy, KindLimit, 1, fptr(priceA))
		mustPlace(t, ex, pb, "NOVA", SideBuy, KindLimit, 1, fptr(priceB))
		_, _, perr := ex.PlaceSolidarityOrder(rich, "NOVA", SideSell, KindLimit, 2, fptr(priceB))
		if perr != nil {
			t.Fatalf("solidarity gift rejected round %d: %+v", i, perr)
		}
		rounds++

		// Every gifted share landed on a member who asked for help.
		newTrades := ex.Trades[:len(ex.Trades)-tapeBefore]
		if len(newTrades) != 2 {
			t.Fatalf("round %d: expected two gift fills, got %d", i, len(newTrades))
		}
		gotPa, gotPb := false, false
		for _, tr := range newTrades {
			if tr.Buyer == pa {
				gotPa = true
			}
			if tr.Buyer == pb {
				gotPb = true
			}
		}
		if !gotPa || !gotPb {
			t.Fatalf("round %d: gifts did not reach both beneficiaries", i)
		}
	}
	if rounds < 3 {
		t.Fatalf("expected several rounds, got %d", rounds)
	}
	if ex.Agents[pa].Positions["NOVA"] < int64(rounds/2+1) {
		t.Fatalf("poor_a NOVA = %v, want >= %d", ex.Agents[pa].Positions["NOVA"], rounds/2+1)
	}
}

func TestMMRequoteDoesNotGrowBooksForever(t *testing.T) {
	ex := testExchange()
	for i := 0; i < 5; i++ {
		ex.SimTick()
	}
	for i := range ex.Symbols {
		if len(ex.Symbols[i].Book.orders) > 6 {
			t.Fatalf("%s book has %d resting orders", ex.Symbols[i].Info.Symbol, len(ex.Symbols[i].Book.orders))
		}
		if b, a := ex.Symbols[i].Book.BestBid(), ex.Symbols[i].Book.BestAsk(); b != nil && a != nil && *b >= *a {
			t.Fatalf("crossed book on %s", ex.Symbols[i].Info.Symbol)
		}
	}
}

func TestPendingWritesCaptureTradesOrdersAgents(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	a := alice(ex)
	b := bob(ex)
	bc := ex.Agents[b]
	bc.Positions["NOVA"] = 100
	ex.Agents[b] = bc

	mustPlace(t, ex, b, "NOVA", SideSell, KindLimit, 10, fptr(180.0))
	mustPlace(t, ex, a, "NOVA", SideBuy, KindLimit, 10, fptr(181.0))

	pending := ex.DrainPending()
	if len(pending.Trades) != 1 {
		t.Fatalf("pending trades = %d, want 1", len(pending.Trades))
	}
	if len(pending.Orders) < 2 {
		t.Fatalf("pending orders = %d, want >= 2", len(pending.Orders))
	}
	if _, ok := pending.Agents[a]; !ok {
		t.Fatal("pending agents missing alice")
	}
	if _, ok := pending.Agents[b]; !ok {
		t.Fatal("pending agents missing bob")
	}
	if len(pending.Snapshots) != 0 {
		t.Fatal("snapshots only on sim ticks")
	}

	tr := pending.Trades[0]
	if tr.Price != 180.0 {
		t.Fatalf("trade price = %v, want 180", tr.Price)
	}
	if tr.Buyer != a || tr.Seller != b {
		t.Fatalf("trade parties = %v -> %v", tr.Buyer, tr.Seller)
	}
	if tr.GiniAfter < 0.0 || tr.GiniAfter > 1.0 {
		t.Fatalf("gini_after out of range: %v", tr.GiniAfter)
	}

	// Drain actually drains.
	if again := ex.DrainPending(); len(again.Trades) != 0 {
		t.Fatal("second drain not empty")
	}
}

func TestRestoreRebuildsBooksAndReservations(t *testing.T) {
	ex := testExchange()
	a := ex.RegisterAgent("restorer", 50_000.0)
	ex.CancelAllForAgent(MarketMakerID)
	ex.CancelAllForAgent(SolidarityID)

	ac := ex.Agents[a]
	ac.Positions["QNTM"] = 10
	ex.Agents[a] = ac
	rec1, _ := mustPlace(t, ex, a, "NOVA", SideBuy, KindLimit, 10, fptr(150.0))
	mustPlace(t, ex, a, "QNTM", SideSell, KindLimit, 4, fptr(999.0))

	var opens []OrderRecord
	for _, r := range ex.Orders {
		if r.AgentID == a {
			opens = append(opens, r)
		}
	}
	// Production rebuilds reservations from open orders only; mirror that.
	var agents []AgentCache
	for _, c := range ex.Agents {
		c.ReservedCash = 0.0
		c.ReservedShares = map[string]uint32{}
		agents = append(agents, c)
	}
	stocks := make([]StockInfo, len(ex.Symbols))
	for i := range ex.Symbols {
		stocks[i] = ex.Symbols[i].Info
	}
	nextOrderID := ex.nextOrderID

	ex2 := Restore(RestoreState{
		Stocks:      stocks,
		Agents:      agents,
		OpenOrders:  opens,
		Tournaments: nil,
		NextOrderID: nextOrderID,
	})
	if b := ex2.Symbols[0].Book.BestBid(); b == nil || *b != 150.0 {
		t.Fatalf("best bid = %v, want 150", b)
	}
	// Reservation rebuilt from the resting buy: 10 * 150.
	if math.Abs(ex2.Agents[a].ReservedCash-1_500.0) > 1e-6 {
		t.Fatalf("reserved = %v, want 1500", ex2.Agents[a].ReservedCash)
	}
	// 10 held minus 4 reserved by the resting sell.
	if ac2 := ex2.Agents[a]; ac2.FreeShares("QNTM") != 6 {
		t.Fatalf("free QNTM = %v, want 6", ac2.FreeShares("QNTM"))
	}
	if ex2.Orders[rec1.ID].Status != StatusOpen {
		t.Fatalf("restored order status = %s", ex2.Orders[rec1.ID].Status)
	}

	// The restored exchange keeps trading from the same id sequence.
	late := ex2.RegisterAgent("late", 10_000.0)
	rec3, _ := mustPlace(t, ex2, late, "HELX", SideBuy, KindLimit, 1, fptr(400.0))
	if rec3.ID <= rec1.ID {
		t.Fatalf("order id did not advance: %d <= %d", rec3.ID, rec1.ID)
	}
}

func TestTournamentScoringFormula(t *testing.T) {
	if math.Abs(tournamentScore(0.10, 0.0)-0.10) > 1e-9 {
		t.Fatal("score mismatch")
	}
	if math.Abs(tournamentScore(-0.05, 1.0)-0.95) > 1e-9 {
		t.Fatal("score mismatch")
	}
	// Cooperation can fully offset a small loss: pure P&L is not the goal.
	if tournamentScore(-0.20, 1.0) <= tournamentScore(0.15, 0.0) {
		t.Fatal("cooperation should beat modest profit")
	}
}

func TestTournamentLifecycleAndAttribution(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	rich := ex.RegisterAgent("rich", 60_000_000.0)
	rc := ex.Agents[rich]
	rc.Positions["NOVA"] = 100
	ex.Agents[rich] = rc
	poor := ex.RegisterAgent("poor", 2_000.0)

	tid := ex.CreateTournament("test-games", 5)
	if err := ex.EnterTournament(tid, poor, "receiver"); err != nil {
		t.Fatalf("enter poor: %v", err)
	}
	if err := ex.EnterTournament(tid, rich, "giver"); err != nil {
		t.Fatalf("enter rich: %v", err)
	}
	// Double entry rejected.
	if err := ex.EnterTournament(tid, poor, "again"); err == nil {
		t.Fatal("double entry accepted")
	}
	// Unknown agents rejected.
	if err := ex.EnterTournament(tid, uuid.New(), "x"); err == nil {
		t.Fatal("unknown agent accepted")
	}

	if err := ex.StartTournament(tid); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Starting twice is rejected.
	if err := ex.StartTournament(tid); err == nil {
		t.Fatal("double start accepted")
	}

	// rich sells to poorer resting bid -> rich accrues prosocial volume.
	bid, _ := mustPlace(t, ex, poor, "NOVA", SideBuy, KindLimit, 4, fptr(180.0))
	_, fills, perr := ex.PlaceSolidarityOrder(rich, "NOVA", SideSell, KindLimit, 4, fptr(179.0))
	if perr != nil {
		t.Fatalf("solidarity order rejected: %+v", perr)
	}
	if len(fills) != 1 {
		t.Fatalf("fills = %d, want 1", len(fills))
	}

	view := ex.TournamentView(tid)
	if view == nil {
		t.Fatal("no view")
	}
	if view.Status != TStatusRunning {
		t.Fatalf("status = %s, want running", view.Status)
	}
	var richE, poorE *TournamentEntryView
	for i := range view.Entries {
		switch view.Entries[i].AgentID {
		case rich:
			richE = &view.Entries[i]
		case poor:
			poorE = &view.Entries[i]
		}
	}
	if richE == nil || poorE == nil {
		t.Fatal("missing entries")
	}
	if richE.TotalVolume != 4 || richE.ProsocialVolume != 4 {
		t.Fatalf("rich vol = %d/%d, want 4/4", richE.TotalVolume, richE.ProsocialVolume)
	}
	if poorE.TotalVolume != 4 || poorE.ProsocialVolume != 0 {
		t.Fatalf("poor vol = %d/%d, want 4/0", poorE.TotalVolume, poorE.ProsocialVolume)
	}
	if math.Abs(poorE.CoopShare) > 1e-9 {
		t.Fatalf("poor coop = %v, want 0", poorE.CoopShare)
	}
	if ex.Orders[bid.ID].Filled != 4 {
		t.Fatalf("poor bid filled = %v, want 4", ex.Orders[bid.ID].Filled)
	}

	// Run out the clock.
	for i := 0; i < 5; i++ {
		ex.SimTick()
	}
	done := ex.TournamentView(tid)
	if done == nil || done.Status != TStatusFinished {
		t.Fatalf("status = %+v, want finished", done)
	}
	if done.GiniFinal == nil {
		t.Fatal("gini_final missing")
	}

	// Finalized results are queued for persistence exactly once.
	pending := ex.DrainPending()
	if len(pending.TournamentsFinalized) != 1 {
		t.Fatalf("finalized = %d, want 1", len(pending.TournamentsFinalized))
	}
	if again := ex.DrainPending(); len(again.TournamentsFinalized) != 0 {
		t.Fatal("second drain not empty")
	}
}

func TestRunningTournamentsSurviveRestore(t *testing.T) {
	ex := testExchange()
	clearBots(ex)
	a := ex.RegisterAgent("runner", 10_000.0)
	tid := ex.CreateTournament("persist-me", 30)
	if err := ex.EnterTournament(tid, a, "hold"); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := ex.StartTournament(tid); err != nil {
		t.Fatalf("start: %v", err)
	}

	var tournaments []Tournament
	for _, tm := range ex.Tournaments {
		if tm.ID == tid {
			tournaments = append(tournaments, tm)
		}
	}
	stocks := make([]StockInfo, len(ex.Symbols))
	for i := range ex.Symbols {
		stocks[i] = ex.Symbols[i].Info
	}
	agents := make([]AgentCache, 0, len(ex.Agents))
	for _, c := range ex.Agents {
		agents = append(agents, c)
	}
	ex2 := Restore(RestoreState{
		Stocks:      stocks,
		Agents:      agents,
		OpenOrders:  nil,
		Tournaments: tournaments,
		NextOrderID: ex.nextOrderID,
	})
	v := ex2.TournamentView(tid)
	if v == nil || v.Status != TStatusRunning {
		t.Fatalf("restored status = %+v, want running", v)
	}
	if v.DurationTicks != 30 {
		t.Fatalf("duration = %d, want 30", v.DurationTicks)
	}
	if len(v.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(v.Entries))
	}
}

func TestWelfareSnapshotRecordedPerTick(t *testing.T) {
	ex := testExchange()
	ex.SimTick()
	pending := ex.DrainPending()
	if len(pending.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(pending.Snapshots))
	}
	if pending.Snapshots[0].MeanEquity <= 0.0 {
		t.Fatal("mean equity not positive")
	}
}

// --- helpers ----------------------------------------------------------------

func fptr(p float64) *float64 { return &p }

func eqFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}

func eqQtys(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
