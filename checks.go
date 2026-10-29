package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Checks are data: a name, a POSIX sh script (or one per `uname -s`), and
// the name of a parser that turns what the script printed into a value.
// The defaults are embedded (checks.json); the user's file
// ($O_FLEET_CHECKS, else ~/.config/o-fleet/checks.json, same format) adds
// checks, and a row with a default's name replaces it. A new check is a
// row in that file, never code.

//go:embed checks.json
var defaultChecks []byte

// Check is one check row.
type Check struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Script runs on every machine; Scripts picks one by `uname -s`
	// (a sh case pattern: "Linux", "Darwin", "*"), Script being "*".
	Script  string            `json:"script,omitempty"`
	Scripts map[string]string `json:"scripts,omitempty"`
	// Parser is one of parsers' names: df, kv, lines, json, text.
	Parser string `json:"parser"`
	// Hide drops a row parser's rows whose FIELD matches one of the
	// glob patterns (path.Match); --all keeps them.
	Hide map[string][]string `json:"hide,omitempty"`
	// Dedupe keeps the first row of each value of this field.
	Dedupe string `json:"dedupe,omitempty"`
	// Sort orders rows by a field, descending with a leading "-".
	Sort string `json:"sort,omitempty"`
	// Timeout is a Go duration ("20s"); "" is DefaultTimeout.
	Timeout string `json:"timeout,omitempty"`
	// Source says where the row came from: "default" or the user file.
	Source string `json:"source,omitempty"`
}

type checksFile struct {
	Checks []Check `json:"checks"`
}

// checksPath is the user's checks file.
func checksPath() string {
	if p := os.Getenv("O_FLEET_CHECKS"); p != "" {
		return p
	}
	return filepath.Join(configDir(), "checks.json")
}

// loadChecks is the embedded checks with the user's file over them.
func loadChecks() ([]Check, error) {
	var base checksFile
	if err := json.Unmarshal(defaultChecks, &base); err != nil {
		return nil, fmt.Errorf("embedded checks.json: %w", err)
	}
	out := base.Checks
	for i := range out {
		out[i].Source = "default"
	}
	data, err := os.ReadFile(checksPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		var user checksFile
		if err := json.Unmarshal(data, &user); err != nil {
			return nil, fmt.Errorf("%s: %w", checksPath(), err)
		}
		for _, c := range user.Checks {
			c.Source = checksPath()
			if i := slices.IndexFunc(out, func(d Check) bool { return d.Name == c.Name }); i >= 0 {
				out[i] = c
			} else {
				out = append(out, c)
			}
		}
	}
	for _, c := range out {
		if err := c.valid(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c Check) valid() error {
	if c.Name == "" {
		return errors.New("a check row has no name")
	}
	if _, ok := parsers[c.Parser]; !ok {
		return fmt.Errorf("check %s: parser %q is not one of %s", c.Name, c.Parser, strings.Join(parserNames(), ", "))
	}
	if c.Script == "" && len(c.Scripts) == 0 {
		return fmt.Errorf("check %s: no script", c.Name)
	}
	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			return fmt.Errorf("check %s: timeout: %w", c.Name, err)
		}
	}
	return nil
}

func findCheck(checks []Check, name string) (Check, error) {
	for _, c := range checks {
		if c.Name == name {
			return c, nil
		}
	}
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}
	return Check{}, fmt.Errorf("no check %q (checks: %s)", name, strings.Join(names, ", "))
}

// RemoteScript is the one script sent to every machine: Script, or a case
// over `uname -s` choosing among Scripts (Script as the "*" arm).
func (c Check) RemoteScript() string {
	if len(c.Scripts) == 0 {
		return c.Script
	}
	arms := slices.Sorted(func(yield func(string) bool) {
		for k := range c.Scripts {
			if k != "*" && !yield(k) {
				return
			}
		}
	})
	var b strings.Builder
	b.WriteString("case \"$(uname -s)\" in\n")
	for _, k := range arms {
		fmt.Fprintf(&b, "%s)\n%s\n;;\n", k, c.Scripts[k])
	}
	fallback := c.Scripts["*"]
	if fallback == "" {
		fallback = c.Script
	}
	if fallback == "" {
		fallback = fmt.Sprintf("echo \"o-fleet check %s: no script for $(uname -s)\" >&2; exit 3", c.Name)
	}
	fmt.Fprintf(&b, "*)\n%s\n;;\nesac\n", fallback)
	return b.String()
}

func (c Check) timeout() time.Duration {
	if d, err := time.ParseDuration(c.Timeout); err == nil && d > 0 {
		return d
	}
	return DefaultTimeout
}

// Result is one check on one machine.
type Result struct {
	Machine  string        `json:"machine"`
	Check    string        `json:"check"`
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Value    any           `json:"value,omitempty"`
	Millis   int64         `json:"ms"`
	Raw      string        `json:"-"`
	Hidden   int           `json:"hidden,omitempty"`
	Parser   string        `json:"parser"`
	Duration time.Duration `json:"-"`
}

// runCheck runs c on every machine at once, each under the check's
// timeout, and returns the results in the machines' order. all keeps the
// rows Hide and Dedupe would drop.
func runCheck(ctx context.Context, run Runner, c Check, machines []Machine, all bool) []Result {
	out := make([]Result, len(machines))
	var wg sync.WaitGroup
	for i, m := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = runOne(ctx, run, c, m, all)
		}()
	}
	wg.Wait()
	return out
}

func runOne(ctx context.Context, run Runner, c Check, m Machine, all bool) Result {
	res := Result{Machine: m.Name, Check: c.Name, Parser: c.Parser}
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	start := time.Now()
	raw, err := run.Run(ctx, m, c.RemoteScript())
	res.Duration = time.Since(start)
	res.Millis = res.Duration.Milliseconds()
	res.Raw = raw
	if err != nil {
		res.Error = err.Error()
		return res
	}
	v, err := parsers[c.Parser](raw)
	if err != nil {
		res.Error = fmt.Sprintf("%s: %s: %v", m.Name, c.Parser, err)
		return res
	}
	if rows, ok := v.([]map[string]any); ok {
		kept := c.filter(rows, all)
		res.Hidden = len(rows) - len(kept)
		v = kept
	}
	res.Value, res.OK = v, true
	return res
}

// filter applies Hide, Dedupe and Sort to a row parser's rows.
func (c Check) filter(rows []map[string]any, all bool) []map[string]any {
	var out []map[string]any
	seen := map[string]bool{}
	for _, row := range rows {
		if !all && c.hidden(row) {
			continue
		}
		if c.Dedupe != "" && !all {
			key := fmt.Sprint(row[c.Dedupe])
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, row)
	}
	if field, desc := strings.TrimPrefix(c.Sort, "-"), strings.HasPrefix(c.Sort, "-"); field != "" {
		sort.SliceStable(out, func(i, j int) bool {
			less := compare(out[i][field], out[j][field])
			if desc {
				return less > 0
			}
			return less < 0
		})
	}
	return out
}

func (c Check) hidden(row map[string]any) bool {
	for field, patterns := range c.Hide {
		value := fmt.Sprint(row[field])
		for _, p := range patterns {
			if ok, _ := path.Match(p, value); ok {
				return true
			}
		}
	}
	return false
}

func compare(a, b any) int {
	fa, aok := number(a)
	fb, bok := number(b)
	if aok && bok {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// Parsers: what a check's script printed to a value. A row parser
// returns []map[string]any, which Hide, Dedupe and Sort apply to.
var parsers = map[string]func(string) (any, error){
	"df":    parseDF,
	"kv":    func(s string) (any, error) { return parseKV(s), nil },
	"lines": parseLines,
	"json":  parseJSON,
	"text":  func(s string) (any, error) { return strings.TrimRight(s, "\n"), nil },
}

func parserNames() []string {
	return slices.Sorted(func(yield func(string) bool) {
		for k := range parsers {
			if !yield(k) {
				return
			}
		}
	})
}

// dfLine is one POSIX df -P -k line: the filesystem and the mount may
// hold spaces (macOS "map auto_home"), the four numbers between them do
// not.
var dfLine = regexp.MustCompile(`^(.*?)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+%|-)\s+(.+)$`)

// parseDF reads df -P -k: rows {filesystem, size, used, avail, use_pct,
// mount}, sizes in bytes. Lines that are not mount lines (the header,
// a "df: /x: Permission denied" note) are skipped; no mount line at all
// is an error.
func parseDF(s string) (any, error) {
	var rows []map[string]any
	for _, line := range strings.Split(s, "\n") {
		m := dfLine.FindStringSubmatch(strings.TrimRight(line, "\r "))
		if m == nil {
			continue
		}
		size, _ := strconv.ParseInt(m[2], 10, 64)
		used, _ := strconv.ParseInt(m[3], 10, 64)
		avail, _ := strconv.ParseInt(m[4], 10, 64)
		pct := int64(-1)
		if p, err := strconv.ParseInt(strings.TrimSuffix(m[5], "%"), 10, 64); err == nil {
			pct = p
		}
		rows = append(rows, map[string]any{
			"filesystem": strings.TrimSpace(m[1]), "size": size * 1024, "used": used * 1024,
			"avail": avail * 1024, "use_pct": pct, "mount": strings.TrimSpace(m[6]),
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no df lines in %q", firstLine(s, ""))
	}
	return rows, nil
}

// parseKV reads key=value (or key: value) lines into a map; a later key
// wins, quotes around a value are dropped.
func parseKV(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			if k, v, ok = strings.Cut(line, ":"); !ok {
				continue
			}
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

func parseLines(s string) (any, error) {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func parseJSON(s string) (any, error) {
	var v any
	err := json.Unmarshal([]byte(s), &v)
	return v, err
}
