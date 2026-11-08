package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// The Fleet page: a status line, the machine list on the left with its
// status columns and, on the right, the focused machine's detail above its
// mounts (stacked below stackWidth columns). The divider follows the
// page's pane keys (ochazuke's Runtime.PaneLayout).

// dataPage is the page as the catalog declares it, so a host draws it
// from o-fleet's rows (machine.state).
func dataPage() ocli.CatalogPage {
	return ocli.CatalogPage{
		ID: pageID, Title: "Fleet", Layout: ocli.LayoutSplit,
		Lists: []ocli.CatalogList{
			{ID: machinesID, Noun: "machine", Title: "Machines"},
			{ID: mountsID, Noun: "mount", Title: "Mounts"},
		},
		Detail: &ocli.CatalogDetail{Fields: []string{
			"name", "tailscale", "ip", "ssh", "user", "local", "os", "disk", "tags", "notes", "status",
			"machine", "mount", "filesystem", "size", "used", "avail", "use_pct", "error",
		}},
	}
}

const stackWidth = 80

func (a *App) render(r *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	l := r.PaneLayout(pageID, ochazuke.Layout{Width: width, Height: height, Styles: s})
	status := s.Muted.Render(ansi.Truncate(a.statusLine(), width, "…"))
	bh := max(0, height-1)
	if bh < 8 || width < 20 {
		return ochazuke.Text(status)
	}
	panel := func(title string, list *ochazuke.Collection, w, h int) ochazuke.Block {
		cw, ch := ochazuke.PanelContentSize(w, h)
		return l.PanelBlock(title, ochazuke.Text(list.View(cw, ch)), w, h, list.Focused())
	}
	detail := func(w, h int) ochazuke.Block {
		cw, _ := ochazuke.PanelContentSize(w, h)
		return l.PanelBlock("Detail", ochazuke.Text(ansi.Hardwrap(a.detailText(), max(1, cw), true)), w, h, false)
	}
	title := fmt.Sprintf("Machines %d", a.machines.Len())
	mountsTitle := "Mounts"
	if item, ok := a.machines.Selected(); ok {
		mountsTitle += " " + item.Label
	}
	if width < stackWidth {
		mh := l.Divide(bh*2/5, bh, ochazuke.PaneMinHeight)
		dh := (bh - mh) / 2
		return ochazuke.Text(status + "\n" + strings.Join([]string{
			panel(title, a.machines, width, mh).Text,
			detail(width, dh).Text,
			panel(mountsTitle, a.mounts, width, bh-mh-dh).Text,
		}, "\n"))
	}
	lw := l.Divide(min(max(40, width*11/20), width-36), width-1, ochazuke.PaneMinWidth)
	rw := width - lw - 1
	left := panel(title, a.machines, lw, bh)
	dh := max(6, bh/2)
	right := detail(rw, dh).Text + "\n" + panel(mountsTitle, a.mounts, rw, bh-dh).Text
	lr, rr := strings.Split(left.Text, "\n"), strings.Split(right, "\n")
	rows := make([]string, bh)
	for i := range rows {
		rows[i] = pad(at(lr, i), lw) + " " + at(rr, i)
	}
	return ochazuke.Text(status + "\n" + strings.Join(rows, "\n"))
}

func at(rows []string, i int) string {
	if i < len(rows) {
		return rows[i]
	}
	return ""
}

func pad(line string, width int) string {
	return line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
}

// statusLine counts the machines and how many answered.
func (a *App) statusLine() string {
	up, down := 0, 0
	for _, m := range a.fleet.Machines {
		if st, ok := a.status[m.Name]; ok {
			if st.Up {
				up++
			} else {
				down++
			}
		}
	}
	parts := []string{fmt.Sprintf("%d machines", len(a.fleet.Machines))}
	if up+down > 0 {
		parts = append(parts, fmt.Sprintf("%d up, %d down", up, down))
	}
	for _, j := range a.r.Jobs() {
		if j.Status == ochazuke.JobRunning {
			parts = append(parts, "asking…")
			break
		}
	}
	parts = append(parts, tilde(machinesPath()))
	return strings.Join(parts, "  ·  ")
}

// detailText describes the focused machine (or, on a mount row, its
// machine's mount).
func (a *App) detailText() string {
	var data any
	if inst := a.r.Instances[a.r.BaseFocus()]; inst != nil {
		data = inst.Data
	}
	if data == nil {
		if item, ok := a.machines.Selected(); ok {
			data = item.Data
		}
	}
	var b strings.Builder
	line := func(key, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%-9s %s\n", key, value)
		}
	}
	switch d := data.(type) {
	case machineRow:
		line("machine", d.Name)
		line("via", d.Via())
		line("tailnet", strings.TrimSpace(d.Tailscale+" "+d.IP))
		line("tags", strings.Join(d.Tags, ", "))
		line("notes", d.Notes)
		if st := d.Status; st != nil {
			if !st.Up {
				line("status", "down: "+st.Error)
				break
			}
			line("system", strings.TrimSpace(st.OS+" "+st.Kernel+" "+st.Arch))
			line("uptime", duration(st.UptimeS))
			line("load", fmt.Sprintf("%.2f %.2f %.2f (%d cpus)", st.Load1, st.Load5, st.Load15, st.CPUs))
			line("memory", memText(*st)+" used")
			line("disk", diskText(st.Disk))
			line("error", st.Error)
		} else {
			line("status", "not read (R reads every machine)")
		}
	case Mount:
		line("machine", d.Machine)
		line("mount", d.Mount)
		line("device", d.Filesystem)
		line("free", human(d.Avail))
		line("used", fmt.Sprintf("%s (%d%%)", human(d.Used), d.UsePct))
		line("size", human(d.Size))
		line("error", d.Error)
	default:
		if a.machines.Len() == 0 {
			return "No machines: o-fleet machine add NAME --ssh ALIAS, or o-fleet machine discover --apply."
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
