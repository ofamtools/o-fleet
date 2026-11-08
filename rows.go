package main

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// Rows: every list noun has an id scheme, a label, table columns and one
// cell per column. The same rows fill the TUI's lists and a list verb's
// answer, so --format table prints what the page draws.
//
//	machine/NAME          a registered machine
//	mount/NAME:PATH       one df row of a machine
//	probe/NAME            a check row
//	peer/NAME             a tailnet machine (discover)

var columns = map[string][]ochazuke.Column{
	"machine": {
		{Title: "machine", Min: 8}, {Title: "up", Priority: 1}, {Title: "uptime", Align: ochazuke.AlignRight, Priority: 4},
		{Title: "load", Align: ochazuke.AlignRight, Priority: 3}, {Title: "mem", Align: ochazuke.AlignRight, Priority: 5},
		{Title: "disk", Min: 13, Priority: 2}, {Title: "os", Priority: 6}, {Title: "via", Priority: 7},
	},
	"mount": {
		{Title: "machine", Min: 6}, {Title: "mount", Min: 8}, {Title: "free", Align: ochazuke.AlignRight},
		{Title: "used", Align: ochazuke.AlignRight, Priority: 2}, {Title: "size", Align: ochazuke.AlignRight, Priority: 1},
		{Title: "use%", Align: ochazuke.AlignRight, Priority: 1}, {Title: "filesystem", Priority: 3},
	},
	"probe": {
		{Title: "check", Min: 6}, {Title: "parser", Priority: 1}, {Title: "source", Priority: 3}, {Title: "description", Priority: 2},
	},
	"peer": {
		{Title: "name", Min: 6}, {Title: "ip", Priority: 1}, {Title: "os", Priority: 2}, {Title: "online", Priority: 3},
		{Title: "registered", Priority: 1}, {Title: "ssh", Priority: 2}, {Title: "dns", Priority: 4},
	},
}

// machineRow is a machine row's data: the registry fields, then the last
// status read, when there is one.
type machineRow struct {
	// ID is the machine store's key prop (the name).
	ID string `json:"id"`
	Machine
	Status *Status `json:"status,omitempty"`
}

func machineID(name string) string { return "machine/" + name }

// machineName is a machine word: a name, or a machine row id.
func machineName(word string) string { return strings.TrimPrefix(word, "machine/") }

func machineItem(m Machine, st *Status) ochazuke.ListItem {
	cells := []string{m.Name, "", "", "", "", "", m.OS, m.Via()}
	if st != nil {
		up := "down"
		if st.Up {
			up = "up"
		}
		cells[1], cells[2], cells[3], cells[4], cells[5] = up, duration(st.UptimeS), loadText(*st), memText(*st), diskCell(st.Disk)
		if cells[6] == "" {
			cells[6] = strings.ToLower(st.OS)
		}
	}
	return ochazuke.ListItem{ID: machineID(m.Name), Label: m.Name, Noun: "machine", Data: machineRow{ID: m.Name, Machine: m, Status: st}, Cells: cells}
}

// diskCell is a mount as "200G free 99%".
func diskCell(m *Mount) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("%s free %d%%", human(m.Avail), m.UsePct)
}

func mountItem(m Mount) ochazuke.ListItem {
	if m.Error != "" {
		return ochazuke.ListItem{ID: "mount/" + m.Machine + ":", Label: m.Machine, Noun: "mount", Data: m,
			Cells: []string{m.Machine, "-", "", "", "", "", "error: " + m.Error}}
	}
	return ochazuke.ListItem{ID: "mount/" + m.Machine + ":" + m.Mount, Label: m.Machine + ":" + m.Mount, Noun: "mount", Data: m,
		Cells: []string{m.Machine, m.Mount, human(m.Avail), human(m.Used), human(m.Size), fmt.Sprintf("%d%%", m.UsePct), m.Filesystem}}
}

func checkItem(c Check) ochazuke.ListItem {
	source := c.Source
	if source != "default" {
		source = tilde(source)
	}
	return ochazuke.ListItem{ID: "probe/" + c.Name, Label: c.Name, Noun: "probe", Data: c,
		Cells: []string{c.Name, c.Parser, source, c.Description}}
}

// peerRow is a discovered tailnet machine and what discover makes of it.
type peerRow struct {
	Peer
	Registered string  `json:"registered,omitempty"` // the machine it matches
	SSH        string  `json:"ssh,omitempty"`        // the ~/.ssh/config alias found
	Added      bool    `json:"added,omitempty"`
	Machine    Machine `json:"machine"` // what add registers (or registered)
}

func peerItem(p peerRow) ochazuke.ListItem {
	reg := p.Registered
	switch {
	case p.Added:
		reg = "added"
	case reg == "":
		reg = "new"
	}
	online := "offline"
	if p.Online {
		online = "online"
	}
	return ochazuke.ListItem{ID: "peer/" + p.Name, Label: p.Name, Noun: "peer", Data: p,
		Cells: []string{p.Name, p.IP, p.OS, online, reg, p.SSH, p.DNSName}}
}

func rowsOf(items []ochazuke.ListItem) []ocli.Row {
	out := make([]ocli.Row, len(items))
	for i, it := range items {
		out[i] = ocli.Row{Noun: it.Noun, ID: it.ID, Label: it.Label, Data: it.Data, Cells: it.Cells}
	}
	return out
}

func itemsOf(rows []ocli.Row) []ochazuke.ListItem {
	out := make([]ochazuke.ListItem, len(rows))
	for i, r := range rows {
		out[i] = ochazuke.ListItem{ID: r.ID, Label: r.Label, Noun: r.Noun, Data: r.Data, Cells: r.Cells}
	}
	return out
}

func lines(rows []ocli.Row) ocli.Lines {
	out := make(ocli.Lines, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out
}

func tilde(p string) string {
	if home := homeDir(); home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
