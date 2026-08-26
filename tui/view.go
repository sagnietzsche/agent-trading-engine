package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colTitle  = lipgloss.Color("226")
	colAccent = lipgloss.Color("214")
	colDim    = lipgloss.Color("240")
	colGreen  = lipgloss.Color("48")
	colRed    = lipgloss.Color("203")
	colCyan   = lipgloss.Color("81")
	colPanel  = lipgloss.Color("237")

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colTitle)
	styleDim   = lipgloss.NewStyle().Foreground(colDim)
	styleGood  = lipgloss.NewStyle().Foreground(colGreen)
	styleBad   = lipgloss.NewStyle().Foreground(colRed)
	styleAcc   = lipgloss.NewStyle().Foreground(colAccent)
	styleCyan  = lipgloss.NewStyle().Foreground(colCyan)
)

// ---- picker ----------------------------------------------------------------

func viewPick(m *Model) string {
	var lines []string
	lines = append(lines, "", styleTitle.Render("TRADING ENGINE"), "")
	lines = append(lines, styleCyan.Render("Pick a welfare metric for this session:"), "")
	for i, opt := range metricOptions {
		marker := "  "
		label := opt.label
		desc := opt.desc
		if i == m.sel {
			marker = "▸ "
			label = styleAcc.Bold(true).Render(opt.label)
			desc = styleAcc.Render(opt.desc)
		} else {
			desc = styleDim.Render(opt.desc)
		}
		lines = append(lines, fmt.Sprintf("  %s%s", marker, label))
		lines = append(lines, "      "+desc, "")
	}
	lines = append(lines, styleDim.Render("  [enter] start session    [↑/↓] choose    [q] quit"))
	lines = append(lines, styleDim.Render("  backend: "+m.base))
	body := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(78).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(colPanel).Render(body)
}

// ---- dashboard -------------------------------------------------------------

func viewLive(m *Model) string {
	f := m.frame
	status := styleDim.Render("● connecting")
	switch {
	case strings.HasPrefix(m.status, "reconnecting"):
		status = styleBad.Render("● reconnecting")
	case strings.HasPrefix(m.status, "connected"), m.status == "":
		status = styleGood.Render("● live")
	case strings.Contains(m.status, "reseeding"), m.status == "starting session…":
		status = styleAcc.Render("● "+strings.TrimSuffix(strings.ToLower(m.status), "…"))
	}

	metric := m.frame.Welfare.Metric
	if metric == "" {
		metric = m.metric
	}
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		styleTitle.Render("TRADING ENGINE"),
		styleDim.Render("  ·  "),
		styleCyan.Render(m.symbol()),
		styleDim.Render("  ·  "),
		styleAcc.Render(metric),
		styleDim.Render("  ·  "),
		status,
	)

	welfare := welfarePanel(f.Welfare, f.History)

	stocks := panel("STOCKS", stocksLines(f.Stocks), 46)
	book := panel("BOOK — "+m.symbol(), bookLines(f.Book), 52)
	mid := lipgloss.JoinHorizontal(lipgloss.Top, stocks, "  ", book)

	tape := panel("TIME & SALES", tapeLines(f.Tape), 52)
	agents := panel("AGENTS", agentsLines(f.Agents), 46)
	bottom := lipgloss.JoinHorizontal(lipgloss.Top, tape, "  ", agents)

	footer := styleDim.Render("[←/→] symbol    [r] change metric    [q] quit")

	return lipgloss.JoinVertical(lipgloss.Left,
		header, "", welfare, "", mid, "", bottom, "", footer)
}

// panel wraps a body in a bordered box with a title.
func panel(title, body string, width int) string {
	box := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colPanel).
		Padding(0, 1)
	if title != "" {
		box = box.BorderTop(true).BorderTopForeground(colPanel)
		titleLine := styleDim.Render(title)
		return titleLine + "\n" + box.Render(body)
	}
	return box.Render(body)
}

func welfarePanel(w Welfare, history []WelfarePoint) string {
	metric := w.Metric
	if metric == "" {
		metric = "gini"
	}
	over := w.Gini > w.GiniTarget

	ineqStyle := styleGood
	overWord := "below"
	if over {
		ineqStyle = styleBad
		overWord = "above"
	}

	var head string
	switch metric {
	case "nash":
		head = fmt.Sprintf("Nash social welfare %s", fmtMoney(w.MetricValue))
	case "atkinson":
		head = fmt.Sprintf("Atkinson index %s (ε = 0.5)", fmtNum(w.MetricValue))
	default:
		head = fmt.Sprintf("Gini coefficient %s", fmtNum(w.MetricValue))
	}
	ineq := fmt.Sprintf("inequality %s vs target %s [%s]", fmtNum(w.Gini), fmtNum(w.GiniTarget), overWord)
	mean := fmt.Sprintf("mean equity %s", fmtMoney(w.MeanEquity))

	sparkVals := make([]float64, 0, len(history))
	for _, p := range history {
		sparkVals = append(sparkVals, p.Gini)
	}
	spark := styleDim.Render(sparkline(sparkVals, 42))

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		styleAcc.Bold(true).Render(head),
		styleDim.Render("  ·  "),
		ineqStyle.Render(ineq),
		styleDim.Render("  ·  "),
		styleCyan.Render(mean),
	)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPanel).Padding(0, 1)
	return box.Render(body + "\n" + spark)
}

func stocksLines(stocks []StockView) string {
	if len(stocks) == 0 {
		return styleDim.Render("waiting for data…")
	}
	var sb strings.Builder
	sb.WriteString(styleDim.Render(fmt.Sprintf("%-6s %9s %7s %9s %9s\n", "SYM", "LAST", "CHG%", "BID", "ASK")))
	for _, s := range stocks {
		last := s.Fair
		if s.Last != nil {
			last = *s.Last
		}
		chg := 0.0
		if s.Prev > 0 {
			chg = (last - s.Prev) / s.Prev * 100
		}
		chgStyle := styleDim
		chgText := "  --"
		if s.Last != nil {
			if chg >= 0 {
				chgStyle = styleGood
				chgText = fmt.Sprintf("+%.2f%%", chg)
			} else {
				chgStyle = styleBad
				chgText = fmt.Sprintf("%.2f%%", chg)
			}
		}
		bid, ask := "--", "--"
		if s.Bid != nil {
			bid = fmtNum(*s.Bid)
		}
		if s.Ask != nil {
			ask = fmtNum(*s.Ask)
		}
		fmt.Fprintf(&sb, "%-6s %9s %7s %9s %9s\n",
			styleCyan.Render(s.Symbol), fmtNum(last), chgStyle.Render(chgText), styleDim.Render(bid), styleDim.Render(ask))
	}
	return sb.String()
}

func bookLines(b *BookView) string {
	if b == nil || (len(b.Asks) == 0 && len(b.Bids) == 0) {
		return styleDim.Render("no quotes yet…")
	}
	maxQty := 0.0
	for _, lvl := range append(append([]Level{}, b.Asks...), b.Bids...) {
		if lvl[1] > maxQty {
			maxQty = lvl[1]
		}
	}
	barW := 12
	var sb strings.Builder
	sb.WriteString(styleDim.Render("ASKS (deepest first)\n"))
	// Asks arrive best-first; show them deepest-first.
	for i := len(b.Asks) - 1; i >= 0; i-- {
		lvl := b.Asks[i]
		line := fmt.Sprintf("  %8s × %-4.0f %s", fmtNum(lvl[0]), lvl[1], bar(lvl[1], maxQty, barW))
		if i == 0 {
			sb.WriteString(styleBad.Render(line) + "\n")
		} else {
			sb.WriteString(line + "\n")
		}
	}
	if len(b.Asks) > 0 && len(b.Bids) > 0 {
		spread := b.Asks[0][0] - b.Bids[0][0]
		sb.WriteString(styleDim.Render(fmt.Sprintf("spread %s\n", fmtNum(spread))))
	} else {
		sb.WriteString("\n")
	}
	sb.WriteString(styleDim.Render("BIDS (best first)\n"))
	for i, lvl := range b.Bids {
		line := fmt.Sprintf("  %8s × %-4.0f %s", fmtNum(lvl[0]), lvl[1], bar(lvl[1], maxQty, barW))
		if i == 0 {
			sb.WriteString(styleGood.Render(line) + "\n")
		} else {
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

func tapeLines(trades []Trade) string {
	if len(trades) == 0 {
		return styleDim.Render("no trades yet…")
	}
	n := len(trades)
	if n > 10 {
		n = 10
	}
	var sb strings.Builder
	sb.WriteString(styleDim.Render(fmt.Sprintf("%-9s %-5s %8s %4s %s\n", "TIME", "SYM", "PRICE", "QTY", "BUYER→SELLER")))
	for _, t := range trades[:n] {
		gap := t.BuyerEq - t.SellerEq
		dir := "▸"
		dirStyle := styleDim
		switch {
		case gap < 0: // poorer agent is buying — wealth moved toward equality
			dir, dirStyle = "▲", styleGood
		case gap > 0:
			dir, dirStyle = "▼", styleBad
		}
		fmt.Fprintf(&sb, "%s %s %s %s %s %s %s\n",
			styleDim.Render(clockOf(t.TS)),
			styleCyan.Render(t.Symbol),
			fmtNum(t.Price),
			fmt.Sprintf("%4d", t.Qty),
			dirStyle.Render(dir),
			styleDim.Render(shortID(t.Buyer)+"→"+shortID(t.Seller)),
			styleDim.Render("("+usd(gap, 0)+")"),
		)
	}
	return sb.String()
}

func agentsLines(agents []AgentView) string {
	if len(agents) == 0 {
		return styleDim.Render("waiting for data…")
	}
	n := len(agents)
	if n > 10 {
		n = 10
	}
	var sb strings.Builder
	sb.WriteString(styleDim.Render(fmt.Sprintf("%-20s %10s  %s\n", "AGENT", "EQUITY", "ROLE")))
	for _, a := range agents[:n] {
		roleStyle := styleDim
		switch a.Role {
		case "contributor":
			roleStyle = styleAcc
		case "beneficiary":
			roleStyle = styleGood
		}
		name := a.Name
		if a.IsBot {
			name = styleDim.Render(name)
		}
		fmt.Fprintf(&sb, "%-20s %10s  %s\n", name, fmtMoney(a.Equity), roleStyle.Render(a.Role))
	}
	return sb.String()
}

// ---- formatting helpers ----------------------------------------------------

func fmtNum(v float64) string { return fmt.Sprintf("%.2f", v) }

// fmtMoney renders a dollar amount compactly: $1.42M, $612k, $100k, $42.
func fmtMoney(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	var s string
	switch {
	case v >= 1_000_000:
		s = fmt.Sprintf("$%.2fM", v/1_000_000)
	case v >= 1_000:
		s = fmt.Sprintf("$%.0fk", v/1_000)
	default:
		s = fmt.Sprintf("$%.0f", v)
	}
	if neg {
		return "-" + s
	}
	return s
}

func usd(v float64, _ int) string {
	return fmtMoney(v)
}

// shortID abbreviates a uuid to its first 6 hex chars.
func shortID(id string) string {
	if len(id) >= 6 {
		return id[:6]
	}
	return id
}

// clockOf extracts the HH:MM:SS portion of an RFC3339 timestamp.
func clockOf(ts string) string {
	if len(ts) < 19 {
		return ts
	}
	return ts[11:19]
}

// bar renders a proportional size bar of block characters.
func bar(qty, max float64, width int) string {
	if max <= 0 {
		return strings.Repeat(" ", width)
	}
	n := int(math.Round(qty / max * float64(width)))
	if n < 0 {
		n = 0
	}
	if n > width {
		n = width
	}
	return strings.Repeat("█", n) + strings.Repeat(" ", width-n)
}

// sparkline downsamples points into a unicode block sparkline.
func sparkline(points []float64, width int) string {
	if len(points) < 2 {
		return "collecting history…"
	}
	min, max := points[0], points[0]
	for _, p := range points {
		if p < min {
			min = p
		}
		if p > max {
			max = p
		}
	}
	span := max - min
	if span <= 0 {
		span = 1
	}
	runes := []rune("▁▂▃▄▅▆▇█")
	var sb strings.Builder
	step := float64(len(points)) / float64(width)
	for i := 0; i < width; i++ {
		idx := int(float64(i) * step)
		if idx >= len(points) {
			idx = len(points) - 1
		}
		ci := int((points[idx] - min) / span * 7.999)
		if ci > 7 {
			ci = 7
		}
		sb.WriteRune(runes[ci])
	}
	return sb.String()
}
