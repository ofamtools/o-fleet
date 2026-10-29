package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner runs a shell script on a machine and returns what it printed.
// The script is POSIX sh; the runner wraps it in sh -c, so the remote
// login shell (zsh, bash, fish) never parses it. Tests use a fake runner.
type Runner interface {
	Run(ctx context.Context, m Machine, script string) (string, error)
}

// DefaultTimeout bounds one check on one machine.
const DefaultTimeout = 15 * time.Second

// sshRunner reaches a machine with the ssh client (BatchMode, so it never
// asks for a password), or runs locally for a Local machine.
type sshRunner struct {
	// SSH is the ssh binary ($O_FLEET_SSH, else ssh).
	SSH string
	// ConnectTimeout is ssh's ConnectTimeout in seconds.
	ConnectTimeout int
}

func newSSHRunner() *sshRunner {
	bin := os.Getenv("O_FLEET_SSH")
	if bin == "" {
		bin = "ssh"
	}
	return &sshRunner{SSH: bin, ConnectTimeout: 8}
}

// sshArgs is the ssh command line that runs script on m (without the
// binary): the non-interactive options, the destination, then sh -c.
func (s *sshRunner) sshArgs(m Machine, script string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(s.ConnectTimeout), "-T", m.Destination()}
	if script != "" {
		args = append(args, "sh -c "+shellQuote(script))
	}
	return args
}

func (s *sshRunner) Run(ctx context.Context, m Machine, script string) (string, error) {
	var cmd *exec.Cmd
	if m.Local {
		cmd = exec.CommandContext(ctx, "sh", "-c", script)
	} else {
		cmd = exec.CommandContext(ctx, s.SSH, s.sshArgs(m, script)...)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errOut, nil
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), fmt.Errorf("%s: timed out", m.Name)
	}
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 255 && !m.Local {
			// ssh's own failure: unreachable, refused, no key.
			return out.String(), fmt.Errorf("%s: ssh %s: %s", m.Name, m.Destination(), firstLine(msg, "connection failed"))
		}
		if msg != "" && out.Len() == 0 {
			return out.String(), fmt.Errorf("%s: %s", m.Name, firstLine(msg, err.Error()))
		}
		// A command that printed something and exited non-zero (df with
		// one unreadable mount): the output is used, the parser decides.
		if out.Len() == 0 {
			return "", fmt.Errorf("%s: %w", m.Name, err)
		}
	}
	return out.String(), nil
}

// interactiveArgv is the command line machine ssh runs: an interactive
// session, or one command, on the machine.
func (s *sshRunner) interactiveArgv(m Machine, command []string) []string {
	if m.Local {
		if len(command) == 0 {
			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "sh"
			}
			return []string{shell}
		}
		return []string{"sh", "-c", strings.Join(command, " ")}
	}
	argv := []string{s.SSH, m.Destination()}
	return append(argv, command...)
}

func firstLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return fallback
}

// shellQuote is s as one single-quoted sh word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
