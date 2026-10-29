package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The registry: your machines, one JSON file. Its path is
// $O_FLEET_MACHINES, else $XDG_CONFIG_HOME/o-fleet/machines.json, else
// ~/.config/o-fleet/machines.json. A missing file is an empty fleet; only
// a change (add, remove, rename, set, discover --apply) writes it.

// Machine is one registered machine.
type Machine struct {
	Name string `json:"name"`
	// Tailscale is the machine's tailnet name (a MagicDNS name or the
	// HostName tailscale reports); IP its tailnet IPv4 address.
	Tailscale string `json:"tailscale,omitempty"`
	IP        string `json:"ip,omitempty"`
	// SSH is the ssh destination: a ~/.ssh/config alias or a host name;
	// User is prepended (USER@) when set.
	SSH  string `json:"ssh,omitempty"`
	User string `json:"user,omitempty"`
	// Local runs checks on this machine directly (sh -c), not over ssh.
	Local bool   `json:"local,omitempty"`
	OS    string `json:"os,omitempty"`
	// Disk is the mount status reports; "" is the largest mount df shows.
	Disk  string   `json:"disk,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Notes string   `json:"notes,omitempty"`
}

// Destination is what ssh is given: USER@HOST, HOST being the ssh alias,
// else the tailnet name, else the IP, else the machine's name.
func (m Machine) Destination() string {
	host := m.SSH
	for _, h := range []string{m.Tailscale, m.IP, m.Name} {
		if host == "" {
			host = h
		}
	}
	if m.User != "" && !strings.Contains(host, "@") {
		return m.User + "@" + host
	}
	return host
}

// Via says how a machine is reached, for a person.
func (m Machine) Via() string {
	if m.Local {
		return "local"
	}
	return "ssh " + m.Destination()
}

// Fleet is the registry file's content.
type Fleet struct {
	Machines []Machine `json:"machines"`
}

// machinesPath is the registry file.
func machinesPath() string {
	if p := os.Getenv("O_FLEET_MACHINES"); p != "" {
		return p
	}
	return filepath.Join(configDir(), "machines.json")
}

// configDir is o-fleet's directory under the user's config directory.
func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "o-fleet")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "o-fleet")
}

// loadFleet reads the registry; a missing file is an empty fleet.
func loadFleet() (Fleet, error) {
	var f Fleet
	data, err := os.ReadFile(machinesPath())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s: %w", machinesPath(), err)
	}
	return f, nil
}

// save writes the registry through a temp file and a rename.
func (f Fleet) save() error {
	path := machinesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".machines-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func checkName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("machine name %q: letters, digits, '.', '_' and '-', starting with a letter or digit", name)
	}
	return nil
}

// index is the position of the machine called name, -1 when none.
func (f Fleet) index(name string) int {
	return slices.IndexFunc(f.Machines, func(m Machine) bool { return m.Name == name })
}

// find is the machine called name.
func (f Fleet) find(name string) (Machine, error) {
	if i := f.index(name); i >= 0 {
		return f.Machines[i], nil
	}
	return Machine{}, fmt.Errorf("no machine %q (machine list prints them; machine add registers one)", name)
}

// pick is the machines names (all of them when names is empty), in the
// order given.
func (f Fleet) pick(names []string) ([]Machine, error) {
	if len(names) == 0 {
		return f.Machines, nil
	}
	out := make([]Machine, 0, len(names))
	for _, n := range names {
		m, err := f.find(n)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (f *Fleet) add(m Machine) error {
	if err := checkName(m.Name); err != nil {
		return err
	}
	if f.index(m.Name) >= 0 {
		return fmt.Errorf("machine %q is already registered (machine set changes it)", m.Name)
	}
	f.Machines = append(f.Machines, m)
	return nil
}

func (f *Fleet) remove(name string) (Machine, error) {
	i := f.index(name)
	if i < 0 {
		_, err := f.find(name)
		return Machine{}, err
	}
	m := f.Machines[i]
	f.Machines = slices.Delete(f.Machines, i, i+1)
	return m, nil
}

func (f *Fleet) rename(from, to string) error {
	i := f.index(from)
	if i < 0 {
		_, err := f.find(from)
		return err
	}
	if err := checkName(to); err != nil {
		return err
	}
	if from != to && f.index(to) >= 0 {
		return fmt.Errorf("machine %q is already registered", to)
	}
	f.Machines[i].Name = to
	return nil
}

// splitList is a comma-separated value, trimmed, empties dropped.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
