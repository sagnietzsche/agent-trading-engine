package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	base := flag.String("backend", "http://127.0.0.1:8080", "trading engine base URL")
	flag.Parse()
	if env := os.Getenv("BACKEND_URL"); env != "" {
		*base = env
	}

	m := &Model{base: *base, subCh: make(chan string, 4)}
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.send = p.Send // the session goroutine streams frames into the program

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui error:", err)
		os.Exit(1)
	}
}
