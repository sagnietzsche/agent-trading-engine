package main

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type stage int

const (
	stagePick stage = iota
	stageLive
)

type metricOption struct {
	metric string
	label  string
	desc   string
}

var metricOptions = []metricOption{
	{"gini", "Gini coefficient", "classic 0..1 inequality measure (the default)"},
	{"atkinson", "Atkinson index (ε = 0.5)", "inequality-averse — weights the poorest members more heavily"},
	{"nash", "Nash social welfare", "geometric mean of equities; the engine drives on its deficit (1 − GM/mean) vs target"},
}

// Messages streamed in from the session goroutine.
type statusMsg struct{ text string }
type frameMsg struct{ frame Frame }

// Model is the bubbletea state machine: first a metric picker, then a live
// dashboard driven by WebSocket frames, with a floor chatroom and an announce
// box for telling the agents what to do.
type Model struct {
	base string

	stage      stage
	sel        int // picker selection
	metric     string
	status     string
	frame      Frame
	symbolIdx  int
	showChat   bool
	announcing bool
	input      string

	subCh      chan string
	announceCh chan string
	cancel     context.CancelFunc
	send       func(tea.Msg)

	width, height int
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		// While composing an announcement every key belongs to the input box.
		if m.announcing {
			switch msg.String() {
			case "enter":
				text := strings.TrimSpace(m.input)
				m.announcing = false
				m.input = ""
				if text != "" {
					select {
					case m.announceCh <- text:
					default:
						m.status = "announce queue full — try again"
					}
				}
			case "esc":
				m.announcing = false
				m.input = ""
			default:
				m.input = editInput(m.input, msg)
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.stopSession()
			return m, tea.Quit
		}
		switch m.stage {
		case stagePick:
			switch msg.String() {
			case "up", "k":
				m.sel = (m.sel - 1 + len(metricOptions)) % len(metricOptions)
			case "down", "j":
				m.sel = (m.sel + 1) % len(metricOptions)
			case "enter":
				m.metric = metricOptions[m.sel].metric
				m.stage = stageLive
				m.frame = Frame{}
				m.status = "starting session…"
				m.startSession()
			}
		case stageLive:
			switch msg.String() {
			case "left", "h":
				m.cycleSymbol(-1)
			case "right", "l":
				m.cycleSymbol(1)
			case "c":
				m.showChat = !m.showChat
			case "a":
				m.announcing = true
				m.input = ""
			case "r":
				// Back to the picker; the old session is torn down and a new
				// one starts with the newly chosen metric.
				m.stopSession()
				m.stage = stagePick
				m.frame = Frame{}
				m.symbolIdx = 0
				m.status = ""
			}
		}
	case statusMsg:
		m.status = msg.text
	case frameMsg:
		m.frame = msg.frame
		if m.symbolIdx >= len(m.frame.Stocks) {
			m.symbolIdx = 0
		}
	}
	return m, nil
}

func (m *Model) View() string {
	if m.stage == stagePick {
		return viewPick(m)
	}
	return viewLive(m)
}

// startSession tears down any previous session and opens a fresh one with the
// currently selected metric.
func (m *Model) startSession() {
	m.stopSession()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	sub := make(chan string, 4)
	m.subCh = sub
	announce := make(chan string, 4)
	m.announceCh = announce
	s := newSession(m.base, m.metric, m.send, sub, announce)
	go s.run(ctx)
}

// editInput applies a single keystroke to the announce input line.
func editInput(current string, msg tea.KeyMsg) string {
	switch msg.String() {
	case "backspace":
		if r := []rune(current); len(r) > 0 {
			return string(r[:len(r)-1])
		}
		return current
	case "tab":
		return current
	default:
		if msg.Type == tea.KeyRunes {
			return current + string(msg.Runes)
		}
		return current
	}
}

func (m *Model) stopSession() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *Model) cycleSymbol(d int) {
	n := len(m.frame.Stocks)
	if n == 0 {
		return
	}
	m.symbolIdx = (m.symbolIdx + d + n) % n
	select {
	case m.subCh <- m.frame.Stocks[m.symbolIdx].Symbol:
	default:
	}
}

// symbol returns the currently selected listing, if any.
func (m *Model) symbol() string {
	if len(m.frame.Stocks) == 0 {
		return "NOVA"
	}
	if m.symbolIdx >= len(m.frame.Stocks) {
		m.symbolIdx = 0
	}
	return m.frame.Stocks[m.symbolIdx].Symbol
}

// metricLabel is the human-readable name of the metric currently in force.
func (m *Model) metricLabel() string {
	metric := m.frame.Welfare.Metric
	if metric == "" {
		metric = m.metric
	}
	for _, o := range metricOptions {
		if o.metric == metric {
			return o.label
		}
	}
	return strings.Title(metric)
}
