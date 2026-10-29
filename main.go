// Command o-fleet registers your machines (the fleet) and runs
// checks across them over ssh (Tailscale addresses in ~/.ssh/config).
// Read-only on the machines: a check is a row of sh script and a parser,
// and o-fleet ships only reads.
//
//	o-fleet machine list|show|add|set|remove|rename|discover|ping|ssh|df|status|check|state
//	o-fleet check list|show|run
//	o-fleet df [MACHINE...] [--all] [--json]       (machine df, as a table)
//	o-fleet status [MACHINE...] [--json]           (machine status, as a table)
//	o-fleet ping [MACHINE...] [--via ssh] [--json] (machine ping, as a table)
//	o-fleet ssh NAME [COMMAND...]                  (runs ssh to the machine)
//	o-fleet [tui] [--smoke]                        (the Fleet page)
//	o-fleet catalog
//
// Every NOUN VERB command prints JSON unless --format table|markdown|toon.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
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

// shortcut is a top-level command that runs one machine verb with its
// bare words joined into one comma-separated parameter, printed as a
// table unless --json or --format says otherwise.
type shortcut struct {
	name, noun, verb, list, summary string
}

var shortcuts = []shortcut{
	{"df", "machine", "df", "machines", "free, used and total space per mount on MACHINES (default every machine); --all keeps pseudo mounts"},
	{"status", "machine", "status", "machines", "up, uptime, load, memory and main disk of MACHINES (default every machine), in parallel"},
	{"ping", "machine", "ping", "machines", "reach MACHINES by tailscale ping (or ssh true with --via ssh)"},
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
	for _, s := range shortcuts {
		app.Add(oden.Command{
			Name: s.name, Summary: s.summary, Source: "o-fleet",
			Usage:   "[MACHINE...] [--json | --format table|markdown|toon] [--ARG VALUE]",
			Details: fmt.Sprintf("The same as %s %s --%s M1,M2 --format table.", s.noun, s.verb, s.list),
			Run: func(ctx context.Context, args []string) error {
				return sh.Run(ctx, s.args(a.r, args))
			},
		})
	}
	app.Add(sshCommand(a), viewCommand(stdin, stdout, stderr))
	app.HelpText += `
Page:
  o-fleet (or o-fleet tui) opens the Fleet page; --smoke renders one frame.
Files:
  ~/.config/o-fleet/machines.json ($O_FLEET_MACHINES), checks.json ($O_FLEET_CHECKS).`
	return app.Run(context.Background())
}

// args is the shell command line a shortcut runs: NOUN VERB, its bare
// words joined as --LIST, the flags as given, and --format table unless
// a format was asked for. A flag takes the next word as its value unless
// the verb's parameter is a boolean (or it is --yes, or a bare --json),
// as the shell reads them.
func (s shortcut) args(r *ochazuke.Runtime, in []string) []string {
	op := r.Operations[r.CanonicalKey(s.noun+"."+s.verb)]
	var words, flags []string
	format := false // --json alone or --format: the caller picked the output
	for i := 0; i < len(in); i++ {
		w := in[i]
		if !strings.HasPrefix(w, "--") {
			words = append(words, w)
			continue
		}
		flags = append(flags, w)
		name, _, inline := strings.Cut(strings.TrimPrefix(w, "--"), "=")
		next := i+1 < len(in) && !strings.HasPrefix(in[i+1], "--")
		switch {
		case name == "format":
			format = true
			if !inline && next {
				i++
				flags = append(flags, in[i])
			}
		case name == "json" && !inline && (!next || !strings.HasPrefix(in[i+1], "{")):
			format = true
		case inline || !next || name == "yes" || op.Parameters[name].Type == "boolean":
		default:
			i++
			flags = append(flags, in[i])
		}
	}
	out := []string{s.noun, s.verb}
	if len(words) > 0 {
		out = append(out, "--"+s.list, strings.Join(words, ","))
	}
	out = append(out, flags...)
	if !format {
		out = append(out, "--format", "table")
	}
	return out
}

// sshCommand is o-fleet ssh NAME [COMMAND...]: it replaces itself with
// the ssh client (or a local shell for a local machine).
func sshCommand(a *App) oden.Command {
	return oden.Command{
		Name: "ssh", Summary: "open a shell (or run COMMAND) on a machine over ssh", Source: "o-fleet",
		Usage: "NAME [COMMAND...]", Details: "machine ssh NAME prints the command line instead.",
		Run: func(_ context.Context, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: o-fleet ssh NAME [COMMAND...]")
			}
			f, err := loadFleet()
			if err != nil {
				return err
			}
			m, err := f.find(machineName(args[0]))
			if err != nil {
				return err
			}
			argv := a.ssh.interactiveArgv(m, args[1:])
			path, err := exec.LookPath(argv[0])
			if err != nil {
				return err
			}
			return syscall.Exec(path, argv, os.Environ())
		},
	}
}
