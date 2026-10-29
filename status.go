package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Status is a machine's summary: the status check's keys and the df
// check's mount for the machine's Disk (the largest when Disk is "").
// Both checks run at once; a machine that answers neither is down.
type Status struct {
	Machine  string  `json:"machine"`
	Up       bool    `json:"up"`
	Error    string  `json:"error,omitempty"`
	OS       string  `json:"os,omitempty"`
	Kernel   string  `json:"kernel,omitempty"`
	Arch     string  `json:"arch,omitempty"`
	UptimeS  int64   `json:"uptime_s,omitempty"`
	Load1    float64 `json:"load1"`
	Load5    float64 `json:"load5"`
	Load15   float64 `json:"load15"`
	CPUs     int     `json:"cpus,omitempty"`
	MemTotal int64   `json:"mem_total,omitempty"`
	MemAvail int64   `json:"mem_avail,omitempty"`
	Disk     *Mount  `json:"disk,omitempty"`
	Millis   int64   `json:"ms"`
}

// Mount is one df row.
type Mount struct {
	Machine    string `json:"machine"`
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem"`
	Size       int64  `json:"size"`
	Used       int64  `json:"used"`
	Avail      int64  `json:"avail"`
	UsePct     int64  `json:"use_pct"`
	Error      string `json:"error,omitempty"`
}

// statusChecks are the checks status reads.
var statusChecks = []string{"status", "df"}

// fleetStatus runs the status checks on every machine at once.
func fleetStatus(ctx context.Context, run Runner, checks []Check, machines []Machine) ([]Status, error) {
	var picked []Check
	for _, name := range statusChecks {
		c, err := findCheck(checks, name)
		if err != nil {
			return nil, err
		}
		picked = append(picked, c)
	}
	out := make([]Status, len(machines))
	var wg sync.WaitGroup
	for i, m := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := make([]Result, len(picked))
			var inner sync.WaitGroup
			for j, c := range picked {
				inner.Add(1)
				go func() { defer inner.Done(); res[j] = runOne(ctx, run, c, m, false) }()
			}
			inner.Wait()
			out[i] = statusOf(m, res[0], res[1])
		}()
	}
	wg.Wait()
	return out, nil
}

func statusOf(m Machine, st, df Result) Status {
	s := Status{Machine: m.Name, Millis: max(st.Millis, df.Millis)}
	if st.OK {
		kv, _ := st.Value.(map[string]string)
		s.Up = true
		s.OS, s.Kernel, s.Arch = kv["os"], kv["kernel"], kv["arch"]
		s.UptimeS, _ = strconv.ParseInt(kv["uptime_s"], 10, 64)
		s.Load1, _ = strconv.ParseFloat(kv["load1"], 64)
		s.Load5, _ = strconv.ParseFloat(kv["load5"], 64)
		s.Load15, _ = strconv.ParseFloat(kv["load15"], 64)
		s.CPUs, _ = strconv.Atoi(kv["cpus"])
		total, _ := strconv.ParseInt(kv["mem_total_kb"], 10, 64)
		avail, _ := strconv.ParseInt(kv["mem_avail_kb"], 10, 64)
		s.MemTotal, s.MemAvail = total*1024, avail*1024
	} else {
		s.Error = st.Error
	}
	if df.OK {
		s.Up = true
		s.Disk = pickDisk(mountsOf(m.Name, df), m.Disk)
	} else if s.Error == "" {
		s.Error = df.Error
	}
	return s
}

// mountsOf is a df result's rows as mounts; a failed result is one row
// holding its error.
func mountsOf(machine string, r Result) []Mount {
	if !r.OK {
		return []Mount{{Machine: machine, Error: r.Error}}
	}
	rows, _ := r.Value.([]map[string]any)
	out := make([]Mount, 0, len(rows))
	for _, row := range rows {
		out = append(out, Mount{
			Machine: machine, Mount: fmt.Sprint(row["mount"]), Filesystem: fmt.Sprint(row["filesystem"]),
			Size: asInt(row["size"]), Used: asInt(row["used"]), Avail: asInt(row["avail"]), UsePct: asInt(row["use_pct"]),
		})
	}
	return out
}

func asInt(v any) int64 {
	f, _ := number(v)
	return int64(f)
}

// pickDisk is the mount called want, else the largest.
func pickDisk(mounts []Mount, want string) *Mount {
	var best *Mount
	for i := range mounts {
		m := &mounts[i]
		if m.Error != "" {
			continue
		}
		if want != "" && m.Mount == want {
			return m
		}
		if best == nil || m.Size > best.Size {
			best = m
		}
	}
	return best
}

// human is a byte count the way df -h prints it (1024-based, rounded
// up): 512B, 7.7G, 200G, 15T.
func human(n int64) string {
	const units = "BKMGTPE"
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%dB", n)
	case math.Ceil(f*10) < 100:
		return fmt.Sprintf("%.1f%c", math.Ceil(f*10)/10, units[i])
	}
	return fmt.Sprintf("%.0f%c", math.Ceil(f), units[i])
}

// duration is an uptime in its two largest units: 25d 13h, 3h 5m, 42s.
func duration(s int64) string {
	if s <= 0 {
		return ""
	}
	d, h, m := s/86400, s%86400/3600, s%3600/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", s)
}

// diskText is a mount as "200G free of 15T (99%) /srv".
func diskText(m *Mount) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("%s free of %s (%d%%) %s", human(m.Avail), human(m.Size), m.UsePct, m.Mount)
}

func memText(s Status) string {
	if s.MemTotal == 0 {
		return ""
	}
	return fmt.Sprintf("%s/%s", human(s.MemTotal-s.MemAvail), human(s.MemTotal))
}

func loadText(s Status) string {
	if !s.Up || s.OS == "" {
		return ""
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", s.Load1), "0"), ".")
}
