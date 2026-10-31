package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/oden"
)

// viewCommand is o-fleet tui: the Fleet page.
func viewCommand(stdin io.Reader, stdout, stderr io.Writer) oden.Command {
	return oden.Command{
		Name: "tui", Aliases: []string{"view"}, Source: "o-fleet",
		Summary: "open the Fleet page: machines with status columns on the left, the focused machine's detail and mounts on the right",
		Usage:   "[--smoke] [--no-status] [--width N] [--height N]",
		Details: "o-fleet with no command opens it too. It reads every machine's status on open. Keys: R reads every machine's status again, enter reads the focused machine's df, s its status, p pings it, d d forgets it (confirm), q quits; ? lists them all.",
		Run: func(_ context.Context, args []string) error {
			return runTUI(args, stdin, stdout, stderr)
		},
	}
}

// runTUI parses the page's flags and runs it (or renders one frame).
func runTUI(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("o-fleet tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	smoke := flags.Bool("smoke", false, "read the machines, render one frame to stdout and exit")
	noStatus := flags.Bool("no-status", false, "do not read the machines' status on open")
	width := flags.Int("width", 120, "--smoke frame width")
	height := flags.Int("height", 30, "--smoke frame height")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &oden.ExitError{Code: 2}
	}
	a, err := New()
	if err != nil {
		return err
	}
	a.Stdin, a.Stdout, a.Stderr = stdin, stdout, stderr
	return a.runPage(*smoke, !*noStatus, *width, *height)
}

func (a *App) runPage(smoke, status bool, width, height int) error {
	if _, err := a.r.Execute(ochazuke.Request{Noun: "machine", Verb: "list", Target: machinesID}); err != nil {
		return err
	}
	if status && len(a.fleet.Machines) > 0 {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "machine", Verb: "status", Target: machinesID}); err != nil {
			return err
		}
	}
	a.tui = true
	if smoke {
		if err := a.Settle(); err != nil {
			a.r.UI().Status(err.Error())
		}
		a.m.Width, a.m.Height = width, height
		_, err := fmt.Fprintln(a.Stdout, strings.TrimRight(ansi.Strip(a.m.View().Content), "\n"))
		return err
	}
	_, err := tea.NewProgram(&model{a: a}).Run()
	return err
}

// Settle runs the scheduled reads on this goroutine until none is left,
// the mount list following the selection (--smoke, tests).
func (a *App) Settle() error {
	return ocli.Settle(a.r, func() error { a.follow(); return nil })
}

// model is the ochazuke model plus the follow rule: after every message
// the mount list follows the selected machine row.
type model struct{ a *App }

func (m *model) Init() tea.Cmd { return tea.Batch(m.a.m.Init(), m.a.r.PendingCommands()) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.a.m.Update(msg)
	if m.a.r.Quit {
		return m, cmd
	}
	if m.a.follow() {
		cmd = tea.Batch(cmd, m.a.r.PendingCommands())
	}
	return m, cmd
}

func (m *model) View() tea.View { return m.a.m.View() }
