package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// The tailnet: `tailscale status --json` for discover, `tailscale ping`
// for ping. Copied from tuios internal/federation/tailnet_status.go (the
// status shape, the command lookup with the macOS app bundle, the name
// from the MagicDNS label) and cut to what o-fleet reads.

// tailnetStatus is the part of ipnstate.Status discover reads.
type tailnetStatus struct {
	Self *tailnetPeer
	Peer map[string]*tailnetPeer
}

type tailnetPeer struct {
	HostName     string
	DNSName      string
	OS           string
	TailscaleIPs []string
	Online       bool
}

// Peer is one tailnet machine as discover reports it.
type Peer struct {
	Name     string `json:"name"`
	DNSName  string `json:"dns_name,omitempty"`
	HostName string `json:"host_name"`
	IP       string `json:"ip,omitempty"`
	OS       string `json:"os,omitempty"`
	Online   bool   `json:"online"`
	Self     bool   `json:"self,omitempty"`
}

// tailscaleCommands are the tailscale commands to try, in order:
// $O_FLEET_TAILSCALE alone when set.
func tailscaleCommands() []string {
	if v := os.Getenv("O_FLEET_TAILSCALE"); v != "" {
		return []string{v}
	}
	cmds := []string{"tailscale"}
	if runtime.GOOS == "darwin" {
		cmds = append(cmds, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	return cmds
}

// tailscale runs the first tailscale command found with args.
func tailscale(ctx context.Context, args ...string) ([]byte, []byte, error) {
	var lastErr error
	for _, name := range tailscaleCommands() {
		path, err := exec.LookPath(name)
		if err != nil {
			lastErr = err
			continue
		}
		cmd := exec.CommandContext(ctx, path, args...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err = cmd.Run()
		return out.Bytes(), errOut.Bytes(), err
	}
	return nil, nil, fmt.Errorf("no tailscale command: %w", lastErr)
}

// tailnetPeers reads the tailnet, the machine itself among them, by name.
func tailnetPeers(ctx context.Context) ([]Peer, error) {
	out, errOut, err := tailscale(ctx, "status", "--json")
	var st tailnetStatus
	if jerr := json.Unmarshal(out, &st); jerr != nil {
		if err == nil {
			err = jerr
		}
		return nil, fmt.Errorf("tailscale status: %w %s", err, strings.TrimSpace(string(errOut)))
	}
	return peersOf(&st), nil
}

func peersOf(st *tailnetStatus) []Peer {
	var out []Peer
	if st.Self != nil {
		p := peerOf(st.Self)
		p.Self, p.Online = true, true
		out = append(out, p)
	}
	for _, p := range st.Peer {
		if p != nil {
			out = append(out, peerOf(p))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func peerOf(p *tailnetPeer) Peer {
	out := Peer{HostName: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), OS: p.OS, Online: p.Online, Name: p.HostName}
	if label, _, ok := strings.Cut(out.DNSName, "."); ok && label != "" {
		out.Name = label
	}
	out.Name = sanitizeName(out.Name)
	for _, ip := range p.TailscaleIPs {
		if strings.Count(ip, ".") == 3 {
			out.IP = ip
			break
		}
	}
	return out
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	s = strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(s), "-"), "-.")
	if s == "" {
		return "machine"
	}
	return s
}

// pingResult is one tailscale ping or ssh probe.
type pingResult struct {
	Machine string `json:"machine"`
	Via     string `json:"via"`
	OK      bool   `json:"ok"`
	Millis  int64  `json:"ms"`
	Detail  string `json:"detail,omitempty"`
	Error   string `json:"error,omitempty"`
}

var pongLine = regexp.MustCompile(`pong from .*`)

// tailscalePing is one `tailscale ping -c 1` to the machine's tailnet name.
func tailscalePing(ctx context.Context, m Machine, timeout time.Duration) pingResult {
	res := pingResult{Machine: m.Name, Via: "tailscale"}
	host := m.Tailscale
	if host == "" {
		host = m.IP
	}
	start := time.Now()
	out, errOut, err := tailscale(ctx, "ping", "-c", "1", "--timeout", timeout.String(), host)
	res.Millis = time.Since(start).Milliseconds()
	if line := pongLine.Find(out); line != nil {
		res.OK, res.Detail = true, string(line)
		return res
	}
	msg := strings.TrimSpace(string(out) + " " + string(errOut))
	if err != nil && msg == "" {
		msg = err.Error()
	}
	res.Error = firstLine(msg, "no pong")
	return res
}

// sshPing runs `true` over ssh.
func sshPing(ctx context.Context, run Runner, m Machine) pingResult {
	res := pingResult{Machine: m.Name, Via: m.Via()}
	start := time.Now()
	_, err := run.Run(ctx, m, "true")
	res.Millis = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	return res
}

// sshAliases maps a HostName in ~/.ssh/config ($O_FLEET_SSH_CONFIG) to
// the first plain Host alias that names it, and records each alias's User,
// so discover can fill a peer's ssh alias. Read only; Include and Match
// blocks are not followed.
type sshAliases struct {
	byHost map[string]string
	user   map[string]string
}

func readSSHConfig() sshAliases {
	out := sshAliases{byHost: map[string]string{}, user: map[string]string{}}
	path := os.Getenv("O_FLEET_SSH_CONFIG")
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".ssh", "config")
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	var hosts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		key, val := strings.ToLower(fields[0]), fields[1]
		switch key {
		case "host":
			hosts = hosts[:0]
			for _, h := range fields[1:] {
				if !strings.ContainsAny(h, "*?!") {
					hosts = append(hosts, h)
				}
			}
		case "match":
			hosts = hosts[:0]
		case "hostname":
			for _, h := range hosts {
				if _, ok := out.byHost[strings.ToLower(val)]; !ok {
					out.byHost[strings.ToLower(val)] = h
				}
			}
		case "user":
			for _, h := range hosts {
				out.user[h] = val
			}
		}
	}
	return out
}

// aliasFor is the ssh alias whose HostName is one of the peer's names or
// its IP, else "".
func (s sshAliases) aliasFor(p Peer) string {
	for _, h := range []string{p.IP, p.DNSName, p.HostName, p.Name} {
		if a, ok := s.byHost[strings.ToLower(h)]; ok && h != "" {
			return a
		}
	}
	return ""
}
