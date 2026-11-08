package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// The verbs. The registry verbs read and write machines.json; the remote
// verbs (ping, df, status, check, run) are pending work, so ssh never runs
// on the event loop, and every machine is asked at once, each under its
// check's timeout. Remote verbs are read-only by construction: they run
// the scripts of check rows, and o-fleet ships only reads.
//
// A MACHINES word is a comma-separated list of machine names (or row
// ids); empty is every machine. A TUI key that means the focused row
// passes the row's name (bindings.json "from").

func (a *App) registerVerbs() {
	r := a.r
	r.Define("machine").
		Verb("list", a.machineList, "the registered machines with their last status: rows {noun, id: machine/NAME, label, data: {name, tailscale, ip, ssh, user, local, os, disk, tags, notes, status}, cells}").Rows("machine").Fetch().Quiet().
		Verb("show", a.machineShow, "one machine: {machine, via, ssh}").Fetch().Quiet().Positional("name").
		Verb("add", a.machineAdd, "register a machine: NAME and how to reach it (--ssh ALIAS, --tailscale HOST, --ip, --user, --local) plus --os, --disk, --tags, --notes").Positional("name").
		Verb("remove", a.machineRemove, "forget a machine (the registry only; nothing remote changes)").Destructive().Confirm("Remove machine {{name}}?").Positional("name").
		Verb("discover", a.machineDiscover, "the tailnet's machines (tailscale status --json), each marked registered or new with its ~/.ssh/config alias; --apply registers the new ones (--only NAMES picks them)").Rows("peer").
		Verb("ping", a.machinePing, "reach each machine: tailscale ping when it has a tailnet name, else ssh true (--via ssh|tailscale), with a timeout: rows {machine, via, ok, ms, detail, error}").Fetch().Quiet().Positional("machines").
		Verb("ssh", a.machineSSH, "the ssh command line for a machine, {machine, argv, command} (o-fleet ssh NAME runs it)").Fetch().Quiet().Positional("name").
		Verb("df", a.machineDF, "disk space per mount on each machine (the df check): rows {noun: mount, data: {machine, mount, filesystem, size, used, avail, use_pct, error}, cells}; --all keeps pseudo and system mounts").Rows("mount").Fetch().Quiet().Positional("machines").
		Verb("status", a.machineStatus, "up, uptime, load, memory and the main disk of each machine (the status and df checks, in parallel): machine rows with data.status").Rows("machine").Fetch().Quiet().Positional("machines").
		Verb("run", a.runChecks, "run a check on machines: results {machine, check, ok, error, value, ms} (the same as probe run CHECK MACHINES, from the machine side)").Fetch().Quiet().Positional("check", "machines").
		Verb("lists", a.machineState, "every machine row, JSON Lines {noun, id, label, data, cells}, from the registry (no remote call; the catalog's state)").Lists().Fetch().Quiet()
	r.Define("probe").
		Verb("list", a.checkList, "the checks: the embedded defaults with the user's checks.json over them: rows {noun, id: probe/NAME, data: {name, description, script, scripts, parser, hide, dedupe, sort, timeout, source}, cells}").Rows("probe").Fetch().Quiet().
		Verb("show", a.checkShow, "one check row and the script it sends: {check, script}").Fetch().Quiet().Positional("check").
		Verb("run", a.runChecks, "run CHECK on MACHINES (default every machine): results {machine, check, ok, error, value, ms}").Fetch().Quiet().Positional("check", "machines")
	setHistory(r, ochazuke.HistoryChange, "machine.add", "machine.remove", "machine.discover")

	// name and machines mean "the focused machine" when a key, the help or the
	// palette runs the verb (Parameter.From); the command line has no row and
	// still defaults machines to every machine, or "*" spells that out.
	name := ochazuke.Parameter{Type: "string", Description: "a machine name", Noun: "machine", From: "row:name"}
	machines := ochazuke.Parameter{Type: "string", Description: "machine names, comma-separated, or * for every machine (default: every machine; from a key or the palette, the focused machine)", From: "row:name"}
	check := ochazuke.Parameter{Type: "string", Description: "a check name (probe list prints them)", Noun: "probe"}
	str := func(d string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: d} }
	boolean := func(d string) ochazuke.Parameter { return ochazuke.Parameter{Type: "boolean", Description: d} }
	fields := func(extra map[string]ochazuke.Parameter) map[string]ochazuke.Parameter {
		out := map[string]ochazuke.Parameter{
			"ssh":       str("the ssh destination: a ~/.ssh/config alias or host name"),
			"tailscale": str("the tailnet name (MagicDNS name or HostName)"),
			"ip":        str("the tailnet IPv4 address"),
			"user":      str("the ssh user, when the alias does not set one"),
			"local":     boolean("this machine: run checks here, not over ssh"),
			"os":        str("the OS (linux, macos, ...); status fills the os column either way"),
			"disk":      str("the mount status reports (default: the largest)"),
			"tags":      str("tags, comma-separated"),
			"notes":     str("free text"),
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	all := boolean("keep the mounts and rows the check hides (pseudo filesystems, system volumes, duplicates)")
	set := func(key, result string, params map[string]ochazuke.Parameter) {
		op := r.Operations.At(ochazuke.Key(r.CanonicalKey(key)))
		op.Parameters, op.Result = params, result
		r.Operations.Set(op)
	}
	required := func(p ochazuke.Parameter) ochazuke.Parameter { p.Required = true; return p }
	set("machine.list", "row[]", nil)
	set("machine.show", "{machine, via, ssh}", map[string]ochazuke.Parameter{"name": name})
	set("machine.add", "machine", fields(map[string]ochazuke.Parameter{"name": required(str("the new machine's name"))}))
	set("machine.remove", "{removed}", map[string]ochazuke.Parameter{"name": name})
	set("machine.discover", "row[]", map[string]ochazuke.Parameter{
		"apply": boolean("register the new peers (default: a dry run that writes nothing)"),
		"only":  str("with --apply, only these peer names, comma-separated"),
	})
	set("machine.ping", "row[]", map[string]ochazuke.Parameter{"machines": machines, "via": {Type: "string", Enum: []string{"tailscale", "ssh"}, Description: "how to reach it (default: tailscale when the machine has a tailnet name)"}, "timeout": str("a Go duration (default 5s)")})
	set("machine.ssh", "{machine, argv, command}", map[string]ochazuke.Parameter{"name": name, "command": str("a command to run instead of a login shell")})
	set("machine.df", "row[]", map[string]ochazuke.Parameter{"machines": machines, "all": all})
	set("machine.status", "row[]", map[string]ochazuke.Parameter{"machines": machines})
	set("machine.run", "result[]", map[string]ochazuke.Parameter{"check": required(check), "machines": machines, "all": all})
	set("machine.lists", "JSON Lines of rows", nil)
	set("probe.list", "row[]", nil)
	set("probe.show", "{check, script}", map[string]ochazuke.Parameter{"check": check})
	set("probe.run", "result[]", map[string]ochazuke.Parameter{"check": required(check), "machines": machines, "all": all})
}

func boolArg(args ochazuke.Arguments, name string) bool {
	v, _ := args[name].(bool)
	return v
}

// rowMachine is the machine of the row a verb acts on, "" when none.
func rowMachine(c ochazuke.Ctx) string {
	if c.Instance == nil {
		return ""
	}
	if d, ok := c.Instance.Data.(machineRow); ok {
		return d.Name
	}
	if d, ok := c.Instance.Data.(Mount); ok {
		return d.Machine
	}
	return ""
}

// nameArg is the one machine a verb acts on: --name, else the row's.
func nameArg(c ochazuke.Ctx) (string, error) {
	n := machineName(ochazuke.StringArg(c.Args, "name", ""))
	if n == "" {
		n = rowMachine(c)
	}
	if n == "" {
		return "", &ochazuke.ArgumentError{Err: fmt.Errorf("name a machine (machine list prints them)")}
	}
	return n, nil
}

// machinesArg is the machines a remote verb asks: MACHINES, else every
// machine.
func (a *App) machinesArg(c ochazuke.Ctx) ([]Machine, error) {
	f, err := loadFleet()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range splitList(ochazuke.StringArg(c.Args, "machines", "")) {
		if n == "*" {
			names = nil
			break
		}
		names = append(names, machineName(n))
	}
	ms, err := f.pick(names)
	if err == nil && len(ms) == 0 {
		err = fmt.Errorf("no machines registered in %s (machine add NAME --ssh ALIAS, or machine discover)", tilde(machinesPath()))
	}
	return ms, err
}

func (a *App) machineList(c ochazuke.Ctx) ochazuke.Result {
	f, err := loadFleet()
	if err != nil {
		return ochazuke.Failed(err)
	}
	rows := a.machineRows(f)
	a.fleet = f
	_ = a.machines.SetItems(itemsOf(rows))
	return ochazuke.Done(rows)
}

func (a *App) machineRows(f Fleet) []ocli.Row {
	items := make([]ochazuke.ListItem, len(f.Machines))
	for i, m := range f.Machines {
		var st *Status
		if s, ok := a.status[m.Name]; ok {
			st = &s
		}
		items[i] = machineItem(m, st)
	}
	return rowsOf(items)
}

func (a *App) machineState(c ochazuke.Ctx) ochazuke.Result {
	f, err := loadFleet()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(lines(a.machineRows(f)))
}

func (a *App) machineShow(c ochazuke.Ctx) ochazuke.Result {
	n, err := nameArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	f, err := loadFleet()
	if err != nil {
		return ochazuke.Failed(err)
	}
	m, err := f.find(n)
	if err != nil {
		return ochazuke.Failed(err)
	}
	out := map[string]any{"machine": m, "via": m.Via(), "ssh": a.ssh.interactiveArgv(m, nil)}
	if st, ok := a.status[n]; ok {
		out["status"] = st
	}
	return ochazuke.Done(out)
}

// applyFields sets the machine fields the arguments name.
func applyFields(m *Machine, args ochazuke.Arguments) {
	strs := map[string]*string{"ssh": &m.SSH, "tailscale": &m.Tailscale, "ip": &m.IP, "user": &m.User, "os": &m.OS, "disk": &m.Disk, "notes": &m.Notes}
	for k, p := range strs {
		if v, ok := args[k]; ok {
			*p = fmt.Sprint(v)
		}
	}
	if v, ok := args["tags"]; ok {
		m.Tags = splitList(fmt.Sprint(v))
	}
	if v, ok := args["local"].(bool); ok {
		m.Local = v
	}
}

func (a *App) change(fn func(f *Fleet) (any, error)) ochazuke.Result {
	out, err := a.mutate(fn)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// mutate reads the registry, applies fn, writes it back and refreshes the
// machine list: the one write path of the named verbs and the store.
func (a *App) mutate(fn func(f *Fleet) (any, error)) (any, error) {
	f, err := loadFleet()
	if err != nil {
		return nil, err
	}
	out, err := fn(&f)
	if err != nil {
		return nil, err
	}
	if err := f.save(); err != nil {
		return nil, err
	}
	a.fleet = f
	_ = a.machines.SetItems(itemsOf(a.machineRows(f)))
	return out, nil
}

func (a *App) machineAdd(c ochazuke.Ctx) ochazuke.Result {
	return a.change(func(f *Fleet) (any, error) {
		m := Machine{Name: machineName(ochazuke.StringArg(c.Args, "name", ""))}
		applyFields(&m, c.Args)
		return m, f.add(m)
	})
}

func (a *App) machineRemove(c ochazuke.Ctx) ochazuke.Result {
	n, err := nameArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.change(func(f *Fleet) (any, error) {
		m, err := f.remove(n)
		delete(a.status, n)
		return map[string]any{"removed": m}, err
	})
}

// discover matches each tailnet peer to a registered machine (by
// tailnet name, IP or name) and finds its ssh alias; with apply it
// registers the unmatched ones (those only names, when given).
func (a *App) machineDiscover(c ochazuke.Ctx) ochazuke.Result {
	apply := boolArg(c.Args, "apply")
	only := splitList(ochazuke.StringArg(c.Args, "only", ""))
	res := ochazuke.Pending(func(ctx context.Context) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		peers, err := tailnetPeers(ctx)
		if err != nil {
			return nil, err
		}
		f, err := loadFleet()
		if err != nil {
			return nil, err
		}
		aliases := readSSHConfig()
		var rows []peerRow
		changed := false
		for _, p := range peers {
			row := peerRow{Peer: p, SSH: aliases.aliasFor(p)}
			row.Machine = Machine{Name: p.Name, Tailscale: p.DNSName, IP: p.IP, SSH: row.SSH, OS: strings.ToLower(p.OS), Local: p.Self}
			if row.Machine.Tailscale == "" {
				row.Machine.Tailscale = p.HostName
			}
			for _, m := range f.Machines {
				if m.Name == p.Name || (m.IP != "" && m.IP == p.IP) || (m.Tailscale != "" && (m.Tailscale == p.DNSName || m.Tailscale == p.HostName)) {
					row.Registered, row.Machine = m.Name, m
					break
				}
			}
			if apply && row.Registered == "" && (len(only) == 0 || slices.Contains(only, p.Name)) {
				if err := f.add(row.Machine); err != nil {
					return nil, err
				}
				row.Added, changed = true, true
			}
			rows = append(rows, row)
		}
		if changed {
			if err := f.save(); err != nil {
				return nil, err
			}
		}
		items := make([]ochazuke.ListItem, len(rows))
		for i, p := range rows {
			items[i] = peerItem(p)
		}
		return rowsOf(items), nil
	})
	return res.Then(func(c ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		fresh := 0
		for _, row := range v.([]ocli.Row) {
			if p := row.Data.(peerRow); p.Registered == "" && !p.Added {
				fresh++
			}
		}
		if !apply && fresh > 0 {
			c.Info(fmt.Sprintf("%d new peer(s); machine discover --apply registers them (--only NAMES picks)", fresh))
		}
		if apply {
			a.reload()
		}
	})
}

func (a *App) machinePing(c ochazuke.Ctx) ochazuke.Result {
	ms, err := a.machinesArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	via := ochazuke.StringArg(c.Args, "via", "")
	timeout, err := time.ParseDuration(ochazuke.StringArg(c.Args, "timeout", "5s"))
	if err != nil {
		return ochazuke.Failed(&ochazuke.ArgumentError{Err: fmt.Errorf("--timeout: %w", err)})
	}
	run := a.run
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		out := make([]pingResult, len(ms))
		parallel(len(ms), func(i int) {
			m := ms[i]
			cctx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
			defer cancel()
			useTS := via == "tailscale" || (via == "" && !m.Local && (m.Tailscale != "" || m.IP != ""))
			if useTS {
				out[i] = tailscalePing(cctx, m, timeout)
			} else {
				out[i] = sshPing(cctx, run, m)
			}
		})
		return out, nil
	}).Then(func(c ochazuke.Ctx, v any, err error) {
		if err == nil && a.tui {
			for _, p := range v.([]pingResult) {
				if p.OK {
					c.Info(fmt.Sprintf("%s: %s (%dms)", p.Machine, p.Detail, p.Millis))
				} else {
					c.Error(p.Machine + ": " + p.Error)
				}
			}
		}
	})
}

func (a *App) machineSSH(c ochazuke.Ctx) ochazuke.Result {
	n, err := nameArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	f, err := loadFleet()
	if err != nil {
		return ochazuke.Failed(err)
	}
	m, err := f.find(n)
	if err != nil {
		return ochazuke.Failed(err)
	}
	var command []string
	if cmd := ochazuke.StringArg(c.Args, "command", ""); cmd != "" {
		command = []string{cmd}
	}
	argv := a.ssh.interactiveArgv(m, command)
	quoted := make([]string, len(argv))
	for i, w := range argv {
		quoted[i] = w
		if strings.ContainsAny(w, " '\"$\\|&;<>*?") {
			quoted[i] = shellQuote(w)
		}
	}
	return ochazuke.Done(map[string]any{"machine": n, "argv": argv, "command": strings.Join(quoted, " ")})
}

func (a *App) machineDF(c ochazuke.Ctx) ochazuke.Result {
	ms, err := a.machinesArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	checks, err := loadChecks()
	if err != nil {
		return ochazuke.Failed(err)
	}
	check, err := findCheck(checks, "df")
	if err != nil {
		return ochazuke.Failed(err)
	}
	all, run := boolArg(c.Args, "all"), a.run
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		var items []ochazuke.ListItem
		for _, r := range runCheck(ctx, run, check, ms, all) {
			for _, m := range mountsOf(r.Machine, r) {
				items = append(items, mountItem(m))
			}
		}
		return rowsOf(items), nil
	}).Replacing("df").Then(func(_ ochazuke.Ctx, v any, err error) {
		if err == nil {
			a.setMounts(ms, v.([]ocli.Row))
		}
	})
}

func (a *App) machineStatus(c ochazuke.Ctx) ochazuke.Result {
	ms, err := a.machinesArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	checks, err := loadChecks()
	if err != nil {
		return ochazuke.Failed(err)
	}
	run := a.run
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		sts, err := fleetStatus(ctx, run, checks, ms)
		if err != nil {
			return nil, err
		}
		items := make([]ochazuke.ListItem, len(sts))
		for i := range sts {
			items[i] = machineItem(ms[i], &sts[i])
		}
		return rowsOf(items), nil
	}).Replacing("status").Then(func(_ ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		for _, row := range v.([]ocli.Row) {
			if st := row.Data.(machineRow).Status; st != nil {
				a.status[st.Machine] = *st
			}
		}
		_ = a.machines.SetItems(itemsOf(a.machineRows(a.fleet)))
	})
}

func (a *App) runChecks(c ochazuke.Ctx) ochazuke.Result {
	checks, err := loadChecks()
	if err != nil {
		return ochazuke.Failed(err)
	}
	check, err := findCheck(checks, ochazuke.StringArg(c.Args, "check", ""))
	if err != nil {
		return ochazuke.Failed(&ochazuke.ArgumentError{Err: err})
	}
	ms, err := a.machinesArg(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	all, run := boolArg(c.Args, "all"), a.run
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		return runCheck(ctx, run, check, ms, all), nil
	})
}

func (a *App) checkList(c ochazuke.Ctx) ochazuke.Result {
	checks, err := loadChecks()
	if err != nil {
		return ochazuke.Failed(err)
	}
	items := make([]ochazuke.ListItem, len(checks))
	for i, ch := range checks {
		items[i] = checkItem(ch)
	}
	return ochazuke.Done(rowsOf(items))
}

func (a *App) checkShow(c ochazuke.Ctx) ochazuke.Result {
	checks, err := loadChecks()
	if err != nil {
		return ochazuke.Failed(err)
	}
	name := strings.TrimPrefix(ochazuke.StringArg(c.Args, "check", ""), "probe/")
	if name == "" && c.Instance != nil {
		if ch, ok := c.Instance.Data.(Check); ok {
			name = ch.Name
		}
	}
	ch, err := findCheck(checks, name)
	if err != nil {
		return ochazuke.Failed(&ochazuke.ArgumentError{Err: err})
	}
	return ochazuke.Done(map[string]any{"check": ch, "script": ch.RemoteScript()})
}

// parallel runs fn(0..n-1) at once and waits.
func parallel(n int, fn func(int)) {
	done := make(chan struct{}, n)
	for i := range n {
		go func() { defer func() { done <- struct{}{} }(); fn(i) }()
	}
	for range n {
		<-done
	}
}

// setHistory declares the history kind of each named verb.
func setHistory(r *ochazuke.Runtime, kind string, keys ...string) {
	for _, key := range keys {
		op, ok := r.Operations.Find(ochazuke.Key(r.CanonicalKey(key)))
		if !ok {
			panic("setHistory: no operation " + key)
		}
		op.HistoryKind = kind
		r.Operations.Set(op)
	}
}
