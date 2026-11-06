package main

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke"
)

// The machine and check nouns are thing nouns (DESIGN-001).
//
// machine is a store over machines.json: list, get, create, set and delete
// are the standard verbs, over the same Fleet methods the named verbs use.
// The key is id, the machine's name as it was read; name is the label
// prop, and writing it renames the machine (set @label=NEW; the key and
// the status read for the old name follow). The named verbs add (create)
// and remove (delete, asking first) stay, and so do list, show and the
// remote verbs; the old rename verb is set {props: {"@label": NEW}}.
//
// check is a read-only (facts) store over the embedded checks and the
// user's checks.json: the file is edited by hand.

func machineProps() []ochazuke.PropSpec {
	text := func(name, desc string) ochazuke.PropSpec {
		return ochazuke.PropSpec{Name: name, Parameter: ochazuke.Parameter{Type: "string", Description: desc}}
	}
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: ochazuke.Parameter{Type: "string", Description: "The machine's name (a row id machine/NAME names it too)"}},
		{Name: "name", Role: ochazuke.RoleLabel, Human: true, Parameter: ochazuke.Parameter{Type: "string", Description: "The machine's name: letters, digits, '.', '_' and '-'; writing it renames the machine"}},
		// The prop cannot be called ssh (nor have it as an alias):
		// machine.ssh is a verb, and a word after a noun is a verb or a
		// prop. The registry file's field and add's flag stay ssh.
		{Name: "destination", Parameter: ochazuke.Parameter{Type: "string", Description: "The ssh destination: a ~/.ssh/config alias or host name (the registry's ssh field)"}},
		text("tailscale", "The tailnet name (MagicDNS name or HostName)"),
		text("ip", "The tailnet IPv4 address"),
		text("user", "The ssh user, when the alias does not set one"),
		{Name: "local", Parameter: ochazuke.Parameter{Type: ochazuke.SettingBoolean, Description: "This machine: run checks here, not over ssh"}},
		text("os", "The OS (linux, macos, ...); status fills the os column either way"),
		text("disk", "The mount status reports (default: the largest)"),
		{Name: "tags", Parameter: ochazuke.Parameter{Type: ochazuke.SettingList, Description: "Tags"}},
		text("notes", "Free text"),
	}
}

func machineThing(m Machine) ochazuke.Thing {
	t, _ := ochazuke.ThingOf(m)
	if t == nil {
		t = ochazuke.Thing{}
	}
	t["id"] = m.Name
	t["destination"] = m.SSH
	delete(t, "ssh")
	if m.Tags == nil {
		t["tags"] = []string{}
	}
	return t
}

// applyThing writes the props of a thing onto a machine (not the name).
func applyThing(m *Machine, t ochazuke.Thing) {
	if v, ok := t["destination"]; ok {
		m.SSH = fmt.Sprint(v)
	}
	strs := map[string]*string{"ssh": &m.SSH, "tailscale": &m.Tailscale, "ip": &m.IP, "user": &m.User, "os": &m.OS, "disk": &m.Disk, "notes": &m.Notes}
	for k, p := range strs {
		if v, ok := t[k]; ok {
			*p = fmt.Sprint(v)
		}
	}
	if v, ok := t["tags"]; ok {
		switch x := v.(type) {
		case []string:
			m.Tags = append([]string(nil), x...)
		case []any:
			m.Tags = nil
			for _, e := range x {
				m.Tags = append(m.Tags, fmt.Sprint(e))
			}
		default:
			m.Tags = splitList(fmt.Sprint(v))
		}
	}
	if v, ok := t["local"]; ok {
		switch x := v.(type) {
		case bool:
			m.Local = x
		default:
			m.Local = strings.EqualFold(fmt.Sprint(v), "true")
		}
	}
}

func (a *App) machineStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			f, err := loadFleet()
			if err != nil {
				return nil, err
			}
			m, err := f.find(machineName(id))
			if err != nil {
				return nil, err
			}
			return machineThing(m), nil
		},
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			f, err := loadFleet()
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, len(f.Machines))
			for i, m := range f.Machines {
				out[i] = machineThing(m)
			}
			return out, nil
		},
		Create: func(_ ochazuke.Ctx, t ochazuke.Thing) (ochazuke.Thing, error) {
			m := Machine{Name: machineName(fmt.Sprint(t["name"]))}
			applyThing(&m, t)
			_, err := a.mutate(func(f *Fleet) (any, error) { return nil, f.add(m) })
			if err != nil {
				return nil, err
			}
			return machineThing(m), nil
		},
		Patch: func(_ ochazuke.Ctx, id string, t ochazuke.Thing) (ochazuke.Thing, error) {
			var out ochazuke.Thing
			_, err := a.mutate(func(f *Fleet) (any, error) {
				from := machineName(id)
				i := f.index(from)
				if i < 0 {
					_, err := f.find(from)
					return nil, err
				}
				applyThing(&f.Machines[i], t)
				if v, ok := t["name"]; ok && fmt.Sprint(v) != from {
					to := machineName(fmt.Sprint(v))
					if err := f.rename(from, to); err != nil {
						return nil, err
					}
					if st, ok := a.status[from]; ok {
						delete(a.status, from)
						st.Machine = to
						a.status[to] = st
					}
				}
				out = machineThing(f.Machines[f.index(machineName(fmt.Sprint(firstNonNil(t["name"], from))))])
				return nil, nil
			})
			return out, err
		},
		Delete: func(_ ochazuke.Ctx, id string) error {
			_, err := a.mutate(func(f *Fleet) (any, error) {
				n := machineName(id)
				_, err := f.remove(n)
				delete(a.status, n)
				return nil, err
			})
			return err
		},
		Confirm: map[string]string{"delete": "Remove machine {{label}}? (the registry only; nothing remote changes)"},
	}
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func checkProps() []ochazuke.PropSpec {
	text := func(name, desc string) ochazuke.PropSpec {
		return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string", Description: desc}}
	}
	return []ochazuke.PropSpec{
		{Name: "name", Key: true, Parameter: ochazuke.Parameter{Type: "string", Description: "The check's name"}},
		text("description", "What the check reads"),
		text("script", "The script sent to every machine"),
		{Name: "scripts", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "object", Description: "Scripts by uname -s pattern"}},
		text("parser", "How the output is read: df, kv, lines, json, text"),
		text("dedupe", "Keep the first row of each value of this field"),
		text("sort", "Order rows by this field (a leading - descends)"),
		text("timeout", "A Go duration; empty is the default"),
		text("source", "default or the user's file"),
	}
}

func (a *App) checkStore() ochazuke.Store {
	return ochazuke.Store{Kind: ochazuke.Collection, Facts: true,
		Get: func(_ ochazuke.Ctx, name string) (ochazuke.Thing, error) {
			checks, err := loadChecks()
			if err != nil {
				return nil, err
			}
			ch, err := findCheck(checks, strings.TrimPrefix(name, "check/"))
			if err != nil {
				return nil, err
			}
			return ochazuke.ThingOf(ch)
		},
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			checks, err := loadChecks()
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, len(checks))
			for i, ch := range checks {
				if out[i], err = ochazuke.ThingOf(ch); err != nil {
					return nil, err
				}
			}
			return out, nil
		},
	}
}
