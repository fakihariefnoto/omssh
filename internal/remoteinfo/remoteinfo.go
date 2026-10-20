// Package remoteinfo reads a small, fixed set of facts about a host: its load
// average, how much memory is in use, and how full the disk is where a pane is
// pointed. It exists for one screen — choosing a session — where a number that is
// a second old is worth more than a number that is exact.
//
// It runs one shell command. That is deliberate: /proc and df are on every Linux
// server, the command is read-only, and a parser for them is smaller and more
// predictable than a dependency. Where they are missing (a BSD, a restricted
// shell), the answer is "unknown" rather than a number that is not true.
package remoteinfo

import (
	"strconv"
	"strings"
)

// Runner runs a command on the host and returns its combined output.
type Runner func(command string) (string, error)

// Info is what a card can say about a session.
type Info struct {
	// Load is the one-minute load average. It is a load, not a CPU percentage:
	// load also counts processes waiting on disk, which is exactly what a person
	// wants to know before browsing a busy server.
	Load float64
	// CPUs is how many processors the load is measured against, so the number has
	// a scale.
	CPUs int
	// MemTotal and MemUsed are bytes.
	MemTotal, MemUsed uint64
	// DiskTotal and DiskFree are bytes, for the directory the pane is showing.
	DiskTotal, DiskFree uint64
	// Known is false when the host did not answer in a way we understand.
	Known bool
	// Err is why, when it did not.
	Err string
}

// LoadPercent turns the load into a percentage of the machine's capacity, which
// is what a bar can show. One process per core is 100%.
func (i Info) LoadPercent() int {
	if !i.Known || i.CPUs <= 0 {
		return 0
	}
	percent := i.Load / float64(i.CPUs) * 100
	if percent < 0 {
		return 0
	}
	if percent > 999 {
		return 999
	}
	return int(percent)
}

// MemPercent is used memory as a percentage of total.
func (i Info) MemPercent() int {
	if !i.Known || i.MemTotal == 0 {
		return 0
	}
	percent := i.MemUsed * 100 / i.MemTotal
	if percent > 100 {
		percent = 100
	}
	return int(percent) // 0…100, so the conversion cannot overflow
}

// DiskPercent is used disk as a percentage of total.
func (i Info) DiskPercent() int {
	if !i.Known || i.DiskTotal == 0 {
		return 0
	}
	percent := (i.DiskTotal - i.DiskFree) * 100 / i.DiskTotal
	if percent > 100 {
		percent = 100
	}
	return int(percent) // 0…100, so the conversion cannot overflow
}

// Sample asks the host one question and reads the answer.
//
// The three lines are easy to tell apart: loadavg starts with a number, free's
// Mem line starts with "Mem:", and df's data line starts with a device or a
// mount. Parsing by shape means a host that answers only two of the three still
// gives two useful numbers.
func Sample(run Runner, path string) Info {
	command := "cat /proc/loadavg 2>/dev/null; " +
		"free -b 2>/dev/null | grep '^Mem:'; " +
		"df -Pk " + shellQuote(path) + " 2>/dev/null | tail -1; " +
		"getconf _NPROCESSORS_ONLN 2>/dev/null"
	output, err := run(command)
	if err != nil && strings.TrimSpace(output) == "" {
		return Info{Err: err.Error()}
	}
	return Parse(output)
}

// Parse reads the three answers. It is exported so the formats can be tested
// without a server.
func Parse(output string) Info {
	info := Info{}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "Mem:"):
			// free -b: Mem: total used free shared buff/cache available
			if len(fields) >= 3 {
				info.MemTotal = bytes(fields[1])
				info.MemUsed = bytes(fields[2])
			}
		case fields[0] == "Filesystem" || strings.HasPrefix(line, "Filesystem"):
			// The header of df, nothing to read.
		case isNumber(fields[0]) && len(fields) >= 3 && strings.Contains(line, "/") &&
			strings.Count(line, ".") >= 2 && !strings.Contains(line, "%"):
			// loadavg: 0.12 0.34 0.56 1/234 5678
			info.Load, _ = strconv.ParseFloat(fields[0], 64)
		case len(fields) >= 6 && isInteger(fields[1]) && isInteger(fields[2]):
			// df -Pk: device 1024-blocks used available capacity mount
			total := bytes(fields[1])
			available := bytes(fields[3])
			info.DiskTotal = total * 1024
			info.DiskFree = available * 1024
		case len(fields) == 1 && isInteger(fields[0]):
			info.CPUs, _ = strconv.Atoi(fields[0])
		}
	}
	info.Known = info.MemTotal > 0 || info.DiskTotal > 0 || info.Load > 0
	if !info.Known && info.Err == "" {
		info.Err = "no answer"
	}
	if info.CPUs == 0 {
		info.CPUs = 1
	}
	return info
}

// bytes parses a count, tolerating a "k"/"m"/"g" suffix.
func bytes(value string) uint64 {
	value = strings.TrimSpace(value)
	multiplier := uint64(1)
	lower := strings.ToLower(value)
	switch {
	case strings.HasSuffix(lower, "k"):
		multiplier, value = 1024, value[:len(value)-1]
	case strings.HasSuffix(lower, "m"):
		multiplier, value = 1024*1024, value[:len(value)-1]
	case strings.HasSuffix(lower, "g"):
		multiplier, value = 1024*1024*1024, value[:len(value)-1]
	}
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return number * multiplier
}

func isInteger(value string) bool {
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func isNumber(value string) bool {
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}

// shellQuote makes a path safe to put in the one command we run.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
