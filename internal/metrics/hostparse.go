package metrics

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Parsers for /proc files. They live outside the linux-only file so they are
// tested on every platform.

// MemInfo is the part of /proc/meminfo bitploy uses, in bytes.
type MemInfo struct {
	Total     uint64
	Available uint64
}

// ParseMemInfo reads /proc/meminfo.
func ParseMemInfo(r io.Reader) (MemInfo, error) {
	var mi MemInfo
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && strings.EqualFold(f[1], "kB") {
			v *= 1024
		}
		switch name {
		case "MemTotal":
			mi.Total, haveTotal = v, true
		case "MemAvailable":
			mi.Available, haveAvail = v, true
		}
	}
	if err := sc.Err(); err != nil {
		return mi, err
	}
	if !haveTotal {
		return mi, errors.New("MemTotal not found in meminfo")
	}
	if !haveAvail {
		mi.Available = mi.Total
	}
	return mi, nil
}

// ParseLoadAvg returns the 1-minute load average from /proc/loadavg.
func ParseLoadAvg(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, errors.New("empty loadavg")
	}
	return strconv.ParseFloat(f[0], 64)
}

// CPUTimes are the aggregate jiffies from the first line of /proc/stat.
type CPUTimes struct{ Busy, Total uint64 }

// ParseCPUStat reads the "cpu " line of /proc/stat.
func ParseCPUStat(r io.Reader) (CPUTimes, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var v [10]uint64
		for i := 1; i < len(f) && i <= 10; i++ {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				return CPUTimes{}, err
			}
			v[i-1] = n
		}
		// user nice system idle iowait irq softirq steal guest guest_nice
		// guest time is already inside user/nice, so leave it out of the total.
		idle := v[3] + v[4]
		total := v[0] + v[1] + v[2] + v[3] + v[4] + v[5] + v[6] + v[7]
		return CPUTimes{Busy: total - idle, Total: total}, nil
	}
	return CPUTimes{}, errors.New("no cpu line in stat")
}

// CPUBusyPercent is the host CPU use between two /proc/stat readings (0-100
// over all cores).
func CPUBusyPercent(a, b CPUTimes) float64 {
	if b.Total <= a.Total || b.Busy < a.Busy {
		return 0
	}
	return float64(b.Busy-a.Busy) / float64(b.Total-a.Total) * 100
}
