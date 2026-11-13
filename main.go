// Command o-fleet registers your machines (the fleet) and runs
// checks across them over ssh (Tailscale addresses in ~/.ssh/config).
// Read-only on the machines: a check is a row of sh script and a parser,
// and o-fleet ships only reads.
//
//	o-fleet machine list|show|get|add|create|set|edit|remove|delete|discover|ping|ssh|df|status|run|lists
//	o-fleet probe list|show|run
//	o-fleet df|status|ping [MACHINE...]            (machine df, status, ping: the verb's word alone reads as it)
//	o-fleet ssh NAME [COMMAND...]                  (machine ssh: a shell on the machine)
//	o-fleet [tui] [--smoke]                        (the Fleet page)
//	o-fleet catalog
//
// Every NOUN VERB command prints JSON unless --format table|markdown|toon.
package main

import (
	"context"
	"io"
	"os"
	"strings"

	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/oden"
)

func main() {
	os.Exit(oden.Report(os.Stderr, progName(), Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)))
}

func progName() string {
	if parent := os.Getenv("ODEN_PARENT"); parent != "" {
		return parent + " fleet"
	}
	return "o-fleet"
}

// Main runs one invocation.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	a, err := New()
	if err != nil {
		return err
	}
	a.Stdin, a.Stdout, a.Stderr = stdin, stdout, stderr
	app := oden.New("o-fleet", args, oden.WithBuiltins(), oden.WithStdio(stdin, stdout, stderr),
		oden.WithDefault(func(_ context.Context, args []string) error {
			if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				return oden.ErrNotFound
			}
			return runTUI(args, stdin, stdout, stderr)
		}))
	sh := a.Shell()
	ocli.Mount(app, sh)
	app.Add(viewCommand(stdin, stdout, stderr))
	app.HelpText += `
Page:
  o-fleet (or o-fleet tui) opens the Fleet page; --smoke renders one frame.
Files:
  ~/.config/o-fleet/machines.json ($O_FLEET_MACHINES), checks.json ($O_FLEET_CHECKS).`
	return app.Run(context.Background())
}
