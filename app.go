package main

import (
	"embed"
	"io"
	"os"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// The application: one ochazuke runtime holding the machine, mount, check
// and peer nouns and their verbs, used by the command line (the shell runs
// one verb and settles it) and by the TUI. The runtime holds interaction
// state only; the registry is machines.json, read by each verb, and the
// last status and df answers are the fields below.

const (
	pageID     = "fleet"
	machinesID = "fleet/machines"
	mountsID   = "fleet/mounts"
)

// Version is the catalog's version.
const Version = "0.1.0"

//go:embed bindings.json
var bindingsFS embed.FS

// App is o-fleet's state.
type App struct {
	r *ochazuke.Runtime
	m *ochazuke.Model

	machines, mounts *ochazuke.List

	run Runner
	ssh *sshRunner

	fleet  Fleet             // the registry as last read
	status map[string]Status // the last status per machine
	disks  map[string][]Mount
	// following is the machine row the mount list last followed.
	following string
	tui       bool

	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Namespace is the singular namespace o-fleet declares (lexicon plan): its
// operations are fleet.<noun>.<verb> and a host mounts its catalog under it.
const Namespace = "fleet"

func init() {
	if err := ochazuke.DeclareNamespace(Namespace); err != nil {
		panic(err)
	}
}

// New builds the runtime: the Fleet page holding the machine list and the
// focused machine's mounts, the nouns with their columns, verbs and the
// default key rows (bindings.json), and the model.
func New() (*App, error) {
	r := ochazuke.NewRuntime()
	ssh := newSSHRunner()
	a := &App{r: r, run: ssh, ssh: ssh, status: map[string]Status{}, disks: map[string][]Mount{},
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	r.Instance(pageID, ochazuke.Kind("fleet"))
	var err error
	if a.machines, err = r.AddList(machinesID, nil, ochazuke.Parent(pageID), ochazuke.Columns(columns["machine"]...), ochazuke.NoMarks()); err != nil {
		return nil, err
	}
	if a.mounts, err = r.AddList(mountsID, nil, ochazuke.Parent(pageID), ochazuke.Columns(columns["mount"]...), ochazuke.NoMarks()); err != nil {
		return nil, err
	}
	for noun, cols := range columns {
		r.Define(noun).Columns(cols...)
	}
	r.Define("machine").Props(machineProps()...).Store(a.machineStore())
	r.Define("check").Props(checkProps()...).Store(a.checkStore())
	a.registerVerbs()
	a.m = ochazuke.NewModel(r)
	a.m.Title = "o-fleet"
	if err := r.LoadDefaultBindingsFS(bindingsFS, "bindings.json"); err != nil {
		return nil, err
	}
	if err := ocli.DeclarePage(r, dataPage()); err != nil {
		return nil, err
	}
	page := r.PageOf(pageID)
	page.Render, page.Focus, page.Remembered = a.render, machinesID, machinesID
	if err := r.OpenPage(pageID); err != nil {
		return nil, err
	}
	return a, nil
}

// Shell is the NOUN VERB command line over the runtime. Machine and check
// verbs need no --target: the page's lists own them.
func (a *App) Shell() *ocli.Shell {
	sh := ocli.New(a.r, "o-fleet", a.Stdin, a.Stdout, a.Stderr)
	sh.Usage = ocli.DefaultUsage("o-fleet")
	sh.Version = Version
	sh.Owners = map[string]string{"machine": machinesID, "check": machinesID, "mount": mountsID, "peer": machinesID}
	return sh
}

// reload reads the registry again and refreshes the machine list.
func (a *App) reload() {
	if f, err := loadFleet(); err == nil {
		a.fleet = f
		_ = a.machines.SetItems(itemsOf(a.machineRows(f)))
	}
}

// setMounts records a df answer per machine; the mount list shows the
// focused machine's.
func (a *App) setMounts(ms []Machine, rows []ocli.Row) {
	for _, m := range ms {
		a.disks[m.Name] = nil
	}
	for _, row := range rows {
		if mt, ok := row.Data.(Mount); ok {
			a.disks[mt.Machine] = append(a.disks[mt.Machine], mt)
		}
	}
	a.showMounts()
}

// showMounts fills the mount list with the selected machine's mounts.
func (a *App) showMounts() {
	item, ok := a.machines.Selected()
	if !ok {
		_ = a.mounts.SetItems(nil)
		return
	}
	var items []ochazuke.ListItem
	for _, mt := range a.disks[machineName(item.ID)] {
		items = append(items, mountItem(mt))
	}
	_ = a.mounts.SetItems(items)
}

// follow makes the mount list show the selected machine's mounts when the
// selection moved, reading df for a machine not read yet. It reports
// whether it started a read.
func (a *App) follow() bool {
	item, ok := a.machines.Selected()
	if !ok || item.ID == a.following {
		return false
	}
	a.following = item.ID
	a.showMounts()
	name := machineName(item.ID)
	if _, read := a.disks[name]; read {
		return false
	}
	a.disks[name] = nil // reading: asked once per selection
	return a.launch(machinesID, "machine.df", a.machineDF(ochazuke.Ctx{Runtime: a.r, Args: ochazuke.Arguments{"machines": name}}))
}

// launch starts pending work owned by an instance, outside a key's verb.
func (a *App) launch(owner, verb string, res ochazuke.Result) bool {
	_, err := a.r.Launch(owner, verb, res, ochazuke.Ctx{Runtime: a.r, Context: a.r.Context(), Args: ochazuke.Arguments{}, Instance: a.r.Instances[owner]})
	if err != nil {
		a.r.UI().Status(err.Error())
	}
	return err == nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
