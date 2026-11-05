package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ofam.dev/ochazuke"
)

// Tests run against a temp registry and checks file, a fake runner (no
// ssh), a fake tailscale script and a fake ssh config. No test reaches a
// real machine or the user's ~/.config.

const backupDF = `Filesystem       1024-blocks        Used   Available Capacity Mounted on
udev                 8000000           0     8000000       0% /dev
tmpfs                1600000        5000     1595000       1% /run
/dev/sda4            4000000     2500000     1300000      66% /data
/dev/sda1             262144      131072      131072      50% /boot
/dev/loop0            800000      800000           0     100% /rootfs/base
overlay             20000000     3000000    17000000      15% /
efivarfs                 192          86         102      46% /sys/firmware/efi/efivars
/dev/sda3           20000000     3000000    17000000      15% /overlay
/dev/mapper/vg0-srv 16106127360 15896412160  209715200      99% /srv
/dev/mapper/vg0-srv 16106127360 15896412160  209715200      99% /home
/dev/sdb1         4000000000  1200000000  2800000000      30% /mnt/usb
`

const macDF = `Filesystem     1024-blocks      Used  Available Capacity  Mounted on
/dev/disk3s1s1   488000000  12000000   30000000    29%    /
devfs                  248       248          0   100%    /dev
/dev/disk3s6     488000000   2000000   30000000     7%    /System/Volumes/VM
/dev/disk3s5     488000000 420000000   30000000    94%    /System/Volumes/Data
map auto_home            0         0          0   100%    /System/Volumes/Data/home
/dev/disk7s1    5860000000 440000000 5420000000     8%    /Volumes/my disk
`

const linuxStatus = "os=Linux\nkernel=6.1.0\narch=x86_64\nuptime_s=2209697\nload1=0.31\nload5=0.26\nload15=0.22\nmem_total_kb=16000000\nmem_avail_kb=12000000\ncpus=4\n"

// fakeRunner answers by machine and by a word the script contains; it
// records every script it was sent.
type fakeRunner struct {
	mu      sync.Mutex
	answers map[string]map[string]string // machine -> script word -> output
	down    map[string]bool
	sent    []string
}

func (f *fakeRunner) Run(_ context.Context, m Machine, script string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m.Name+": "+script)
	if f.down[m.Name] {
		return "", errors.New(m.Name + ": ssh " + m.Destination() + ": connection timed out")
	}
	for word, out := range f.answers[m.Name] {
		if strings.Contains(script, word) {
			return out, nil
		}
	}
	return "", errors.New(m.Name + ": no answer")
}

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("O_FLEET_MACHINES", filepath.Join(dir, "machines.json"))
	t.Setenv("O_FLEET_CHECKS", filepath.Join(dir, "checks.json"))
	t.Setenv("O_FLEET_SSH_CONFIG", filepath.Join(dir, "ssh_config"))
	t.Setenv("O_FLEET_TAILSCALE", filepath.Join(dir, "no-tailscale"))
	t.Setenv("O_FLEET_SSH", filepath.Join(dir, "no-ssh"))
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

func fake() *fakeRunner {
	return &fakeRunner{
		answers: map[string]map[string]string{
			"backup": {"df -P": backupDF, "uname -s": linuxStatus},
			"desk":   {"df -P": macDF, "uname -s": strings.Replace(linuxStatus, "Linux", "Darwin", 1)},
		},
		down: map[string]bool{"laptop": true},
	}
}

// run runs one command line through Main (the real runner: registry
// verbs only) and fails on an error.
func run(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := Main(args, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, errOut.String())
	}
	return out.String()
}

// runFake runs one shell command over a fake runner.
func runFake(t *testing.T, f Runner, args ...string) (string, string, error) {
	t.Helper()
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	a.run, a.Stdout, a.Stderr = f, &out, &errOut
	err = a.Shell().Run(context.Background(), args)
	return out.String(), errOut.String(), err
}

func register(t *testing.T) {
	t.Helper()
	run(t, "machine", "add", "backup", "--ssh", "backup", "--tailscale", "backup.node.example", "--tags", "backup,storage")
	run(t, "machine", "add", "desk", "--local")
	run(t, "machine", "add", "laptop", "--tailscale", "laptop.node.example", "--user", "me")
}

func decode[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	return v
}

func TestRegistryVerbs(t *testing.T) {
	isolate(t)
	register(t)
	type row struct {
		ID   string  `json:"id"`
		Data Machine `json:"data"`
	}
	rows := decode[[]row](t, run(t, "machine", "list"))
	if len(rows) != 3 || rows[0].ID != "machine/backup" || !reflect.DeepEqual(rows[0].Data.Tags, []string{"backup", "storage"}) || !rows[1].Data.Local {
		t.Fatalf("%+v", rows)
	}
	if d := rows[2].Data.Destination(); d != "me@laptop.node.example" {
		t.Fatalf("destination %q", d)
	}
	run(t, "machine", "rename", "laptop", "work")
	run(t, "machine", "set", "work", "--notes", "the work laptop", "--disk", "/")
	show := decode[map[string]any](t, run(t, "machine", "show", "work"))
	if m := show["machine"].(map[string]any); m["notes"] != "the work laptop" || m["disk"] != "/" {
		t.Fatalf("%v", show)
	}
	run(t, "machine", "remove", "work", "--yes")
	if rows := decode[[]row](t, run(t, "machine", "list")); len(rows) != 2 {
		t.Fatalf("%+v", rows)
	}
	var out, errOut bytes.Buffer
	if err := Main([]string{"machine", "add", "backup"}, strings.NewReader(""), &out, &errOut); err == nil {
		t.Fatal("a second backup was registered")
	}
	if err := Main([]string{"machine", "add", "bad name"}, strings.NewReader(""), &out, &errOut); err == nil {
		t.Fatal("a name with a space was registered")
	}
}

func TestParseDF(t *testing.T) {
	v, err := parseDF(macDF)
	if err != nil {
		t.Fatal(err)
	}
	rows := v.([]map[string]any)
	if len(rows) != 6 || rows[4]["filesystem"] != "map auto_home" || rows[5]["mount"] != "/Volumes/my disk" || rows[5]["avail"] != int64(5420000000*1024) {
		t.Fatalf("%v", rows)
	}
	if _, err := parseDF("ssh: nope\n"); err == nil {
		t.Fatal("no df lines parsed")
	}
}

func TestDFHidesDedupesAndSorts(t *testing.T) {
	isolate(t)
	register(t)
	out, _, err := runFake(t, fake(), "machine", "df", "--machines", "backup,desk", "--format", "table")
	if err != nil {
		t.Fatal(err)
	}
	var mounts []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		f := strings.Fields(line)
		mounts = append(mounts, f[0]+":"+f[1])
	}
	want := []string{"backup:/srv", "backup:/mnt/usb", "backup:/", "backup:/data", "desk:/Volumes/my", "desk:/", "desk:/System/Volumes/Data"}
	if !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts %v\n%s", mounts, out)
	}
	if !strings.Contains(out, "200G") || !strings.Contains(out, "15T") || !strings.Contains(out, "99%") {
		t.Fatalf("sizes\n%s", out)
	}
	// --all keeps what the check hides.
	all, _, err := runFake(t, fake(), "machine", "df", "--machines", "backup", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, `"/home"`) || !strings.Contains(all, `"/dev"`) {
		t.Fatalf("--all\n%s", all)
	}
}

func TestDFReportsAnUnreachableMachineAsARow(t *testing.T) {
	isolate(t)
	register(t)
	out, _, err := runFake(t, fake(), "machine", "df")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"error": "laptop: ssh me@laptop.node.example: connection timed out"`) && !strings.Contains(out, `laptop: ssh me@laptop.node.example: connection timed out`) {
		t.Fatalf("%s", out)
	}
}

func TestShortcutsPrintTables(t *testing.T) {
	isolate(t)
	register(t)
	a, _ := New()
	for _, s := range shortcuts {
		if s.name != "df" {
			continue
		}
		cases := map[string][]string{
			"backup":                  {"machine", "df", "--machines", "backup", "--format", "table"},
			"backup desk --all":       {"machine", "df", "--machines", "backup,desk", "--all", "--format", "table"},
			"backup --json":           {"machine", "df", "--machines", "backup", "--json"},
			"--format markdown":       {"machine", "df", "--format", "markdown"},
			"--all backup":            {"machine", "df", "--machines", "backup", "--all", "--format", "table"},
			"--json {\"all\":true} x": {"machine", "df", "--machines", "x", "--json", "{\"all\":true}", "--format", "table"},
		}
		for in, want := range cases {
			words := strings.Fields(in)
			if got := s.args(a.r, words); !reflect.DeepEqual(got, want) {
				t.Errorf("%q: %v, want %v", in, got, want)
			}
		}
	}
}

func TestStatus(t *testing.T) {
	isolate(t)
	register(t)
	out, _, err := runFake(t, fake(), "machine", "status", "--format", "table")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.Contains(lines[1], "backup") || !strings.Contains(lines[1], "up") || !strings.Contains(lines[1], "25d 13h") ||
		!strings.Contains(lines[1], "200G free 99%") || !strings.Contains(lines[3], "down") {
		t.Fatalf("%s", out)
	}
	type row struct {
		Data machineRow `json:"data"`
	}
	js, _, _ := runFake(t, fake(), "machine", "status", "--machines", "backup")
	rows := decode[[]row](t, js)
	st := rows[0].Data.Status
	if st == nil || st.CPUs != 4 || st.MemTotal != 16000000*1024 || st.Disk.Mount != "/srv" {
		t.Fatalf("%+v", st)
	}
	// A machine's Disk picks the mount status reports.
	run(t, "machine", "set", "backup", "--disk", "/")
	js, _, _ = runFake(t, fake(), "machine", "status", "--machines", "backup")
	if d := decode[[]row](t, js)[0].Data.Status.Disk; d.Mount != "/" {
		t.Fatalf("%+v", d)
	}
}

func TestChecksAreConfig(t *testing.T) {
	dir := isolate(t)
	register(t)
	user := `{"checks": [
	  {"name": "who", "script": "whoami", "parser": "lines"},
	  {"name": "uname", "scripts": {"Linux": "uname -sr", "*": "uname -s"}, "parser": "text"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "checks.json"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	list := run(t, "check", "list", "--format", "table")
	if !strings.Contains(list, "who ") || !strings.Contains(list, "~") && !strings.Contains(list, dir) {
		t.Fatalf("%s", list)
	}
	show := decode[map[string]any](t, run(t, "check", "show", "uname"))
	script := show["script"].(string)
	if !strings.Contains(script, "case \"$(uname -s)\" in\nLinux)\nuname -sr\n;;\n*)\nuname -s\n;;\nesac") {
		t.Fatalf("%q", script)
	}
	f := fake()
	f.answers["backup"]["whoami"] = "me\n"
	for _, args := range [][]string{{"check", "run", "who", "backup"}, {"machine", "check", "who", "backup"}} {
		out, _, err := runFake(t, f, args...)
		if err != nil {
			t.Fatal(err)
		}
		res := decode[[]Result](t, out)
		if len(res) != 1 || !res[0].OK || !reflect.DeepEqual(res[0].Value, []any{"me"}) {
			t.Fatalf("%v: %s", args, out)
		}
	}
	bad := `{"checks": [{"name": "x", "script": "true", "parser": "nope"}]}`
	os.WriteFile(filepath.Join(dir, "checks.json"), []byte(bad), 0o644)
	if _, _, err := runFake(t, f, "check", "list"); err == nil || !strings.Contains(err.Error(), "parser") {
		t.Fatalf("%v", err)
	}
}

func TestEveryScriptIsSentThroughShC(t *testing.T) {
	s := &sshRunner{SSH: "ssh", ConnectTimeout: 8}
	args := s.sshArgs(Machine{Name: "backup", SSH: "backup"}, "echo 'hi'")
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "-T", "backup", `sh -c 'echo '\''hi'\'''`}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("%q", args)
	}
}

func TestDiscover(t *testing.T) {
	dir := isolate(t)
	status := `{"Self": {"HostName": "desk", "DNSName": "desk.node.example.", "OS": "macOS", "TailscaleIPs": ["100.64.0.5", "fd7a::5"]},
	  "Peer": {"k1": {"HostName": "backup-01", "DNSName": "backup.node.example.", "OS": "linux", "TailscaleIPs": ["100.64.0.3"], "Online": true},
	           "k2": {"HostName": "web", "DNSName": "web.node.example.", "OS": "linux", "TailscaleIPs": ["100.64.0.6"], "Online": false}}}`
	os.WriteFile(filepath.Join(dir, "status.json"), []byte(status), 0o644)
	ts := filepath.Join(dir, "tailscale")
	os.WriteFile(ts, []byte("#!/bin/sh\n[ \"$1\" = status ] || exit 2\ncat '"+filepath.Join(dir, "status.json")+"'\n"), 0o755)
	t.Setenv("O_FLEET_TAILSCALE", ts)
	os.WriteFile(filepath.Join(dir, "ssh_config"), []byte("Host backup\n  User me\n  HostName 100.64.0.3\nHost *\n  ServerAliveInterval 5\n"), 0o644)
	run(t, "machine", "add", "web", "--tailscale", "web.node.example")

	type row struct {
		Data peerRow `json:"data"`
	}
	var out, errOut bytes.Buffer
	if err := Main([]string{"machine", "discover"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err, errOut.String())
	}
	rows := decode[[]row](t, out.String())
	if len(rows) != 3 || rows[0].Data.Name != "backup" || rows[0].Data.SSH != "backup" || rows[2].Data.Registered != "web" || !rows[1].Data.Self {
		t.Fatalf("%+v", rows)
	}
	if !strings.Contains(errOut.String(), "2 new peer(s)") {
		t.Fatalf("%s", errOut.String())
	}
	// A dry run writes nothing.
	if n := len(decode[[]any](t, run(t, "machine", "list"))); n != 1 {
		t.Fatalf("dry run registered %d", n)
	}
	out.Reset()
	if err := Main([]string{"machine", "discover", "--apply", "--only", "backup"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	type mrow struct {
		Data Machine `json:"data"`
	}
	ms := decode[[]mrow](t, run(t, "machine", "list"))
	if len(ms) != 2 || ms[1].Data.Name != "backup" || ms[1].Data.SSH != "backup" || ms[1].Data.IP != "100.64.0.3" || ms[1].Data.Tailscale != "backup.node.example" {
		t.Fatalf("%+v", ms)
	}
}

func TestPingOverSSH(t *testing.T) {
	isolate(t)
	register(t)
	f := fake()
	f.answers["desk"]["true"] = ""
	out, _, err := runFake(t, f, "machine", "ping", "desk,laptop", "--via", "ssh")
	if err != nil {
		t.Fatal(err)
	}
	res := decode[[]pingResult](t, out)
	if !res[0].OK || res[0].Via != "local" || res[1].OK || !strings.Contains(res[1].Error, "timed out") {
		t.Fatalf("%+v", res)
	}
}

func TestSSHCommandLine(t *testing.T) {
	isolate(t)
	register(t)
	t.Setenv("O_FLEET_SSH", "ssh")
	out := decode[map[string]any](t, run(t, "machine", "ssh", "backup", "--command", "df -h"))
	if out["command"] != "ssh backup 'df -h'" {
		t.Fatalf("%v", out)
	}
}

func TestHuman(t *testing.T) {
	for n, want := range map[int64]string{
		512: "512B", 1024: "1.0K", 209715200 * 1024: "200G", 16106127360 * 1024: "15T",
		3089236 * 1024: "3.0G", 10*1024*1024*1024 - 1: "10G", 15195288 * 1024: "15G",
	} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %s, want %s", n, got, want)
		}
	}
	if d := duration(2209697); d != "25d 13h" {
		t.Fatal(d)
	}
}

func TestSmoke(t *testing.T) {
	isolate(t)
	register(t)
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	a.run, a.Stdout = fake(), &out
	if err := a.runPage(true, true, 140, 24); err != nil {
		t.Fatal(err)
	}
	frame := out.String()
	for _, want := range []string{"Machines 3", "3 machines", "2 up, 1 down", "backup", "200G free 99%", "Mounts backup", "/srv", "via       ssh backup"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q\n%s", want, frame)
		}
	}
	// With no machines the page says how to add one.
	isolate(t)
	b, _ := New()
	out.Reset()
	b.Stdout = &out
	if err := b.runPage(true, true, 100, 20); err != nil || !strings.Contains(out.String(), "machine add") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestPageKeys(t *testing.T) {
	isolate(t)
	register(t)
	a, _ := New()
	f := fake()
	a.run = f
	a.tui = true
	if _, err := a.r.Execute(ochazuke.Request{Noun: "machine", Verb: "list", Target: machinesID}); err != nil {
		t.Fatal(err)
	}
	if err := a.Settle(); err != nil {
		t.Fatal(err)
	}
	// Following the selection read backup's df only.
	if len(f.sent) != 1 || !strings.HasPrefix(f.sent[0], "backup: df") {
		t.Fatalf("%q", f.sent)
	}
	key := func(k string) {
		if _, err := a.r.Dispatch(ochazuke.MacroKey{Key: k}); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		_ = a.Settle()
	}
	key("j")
	if item, _ := a.machines.Selected(); item.Label != "desk" || a.mounts.Len() != 3 {
		t.Fatalf("%v %d", item.Label, a.mounts.Len())
	}
	key("s") // status of the focused row only
	if _, ok := a.status["desk"]; !ok || len(a.status) != 1 {
		t.Fatalf("%v", a.status)
	}
	key("R") // every machine
	if len(a.status) != 3 || a.status["laptop"].Up {
		t.Fatalf("%v", a.status)
	}
	key("d")
	key("d")
	key("y")
	if fl, _ := loadFleet(); len(fl.Machines) != 2 || fl.index("desk") >= 0 {
		t.Fatalf("%+v", fl)
	}
}

func TestBindingsAreAtomic(t *testing.T) {
	isolate(t)
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := a.r.ValidateBindings()
	if err != nil || len(warnings) > 0 {
		t.Fatalf("%v %v", warnings, err)
	}
}

// Every o-fleet verb declares its history kind.
func TestEveryVerbDeclaresItsHistory(t *testing.T) {
	isolate(t)
	r := ochazuke.NewRuntime()
	ochazuke.NewModel(r)
	a, _ := New()
	n := 0
	for key, op := range a.r.Operations {
		if _, framework := r.Operations[key]; framework || op.Noun == "fleet" {
			continue
		}
		n++
		if op.HistoryKind == "" {
			t.Errorf("%s declares no history kind", key)
		}
	}
	if n != 16 {
		t.Fatalf("%d o-fleet verbs, want 16", n)
	}
	if _, err := New(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run(t, "catalog"), `"machine"`) {
		t.Fatal("catalog lacks machine")
	}
}

// The parameters declare the focused machine, so the palette, the help and a
// request that names a row fill machines and name without a binding's from.
func TestParameterSourcesFillFromTheRow(t *testing.T) {
	isolate(t)
	register(t)
	for _, p := range []struct{ op, arg string }{{"machine.df", "machines"}, {"machine.status", "machines"}, {"machine.ping", "machines"}, {"machine.remove", "name"}, {"machine.show", "name"}} {
		if got := opsOf(t).Operations[p.op].Parameters[p.arg].From; got != "row:name" {
			t.Errorf("%s %s: From %q", p.op, p.arg, got)
		}
	}
	a, _ := New()
	f := fake()
	a.run = f
	a.tui = true
	if _, err := a.r.Execute(ochazuke.Request{Noun: "machine", Verb: "list", Target: machinesID}); err != nil {
		t.Fatal(err)
	}
	if err := a.Settle(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Dispatch(ochazuke.MacroKey{Key: "j"}); err != nil {
		t.Fatal(err)
	}
	if err := a.r.RunUnbound(a.r.BaseFocus(), ochazuke.Request{Noun: "machine", Verb: "status"}); err != nil {
		t.Fatal(err)
	}
	_ = a.Settle()
	if _, ok := a.status["desk"]; !ok || len(a.status) != 1 {
		t.Fatalf("%v", a.status)
	}
}

func opsOf(t *testing.T) *ochazuke.Runtime {
	t.Helper()
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return a.r
}
