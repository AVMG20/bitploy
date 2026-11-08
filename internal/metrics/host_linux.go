//go:build linux

package metrics

import (
	"context"
	"os"
	"time"
)

// HostSupported reports whether host stats can be read on this platform.
const HostSupported = true

// Host is a snapshot of the machine bitploy runs on.
type Host struct {
	CPUPercent float64
	Load1      float64
	MemTotal   uint64 // bytes
	MemUsed    uint64 // bytes, excluding reclaimable cache
}

// ReadHost reads /proc. CPU is measured over a 250 ms window.
func ReadHost(ctx context.Context) (Host, error) {
	var h Host
	mem, err := readMemInfo()
	if err != nil {
		return h, err
	}
	h.MemTotal, h.MemUsed = mem.Total, mem.Total-mem.Available
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		h.Load1, _ = ParseLoadAvg(string(b))
	}
	a, err := readCPU()
	if err != nil {
		return h, nil // memory and load are still useful
	}
	select {
	case <-time.After(250 * time.Millisecond):
	case <-ctx.Done():
		return h, nil
	}
	if b, err := readCPU(); err == nil {
		h.CPUPercent = CPUBusyPercent(a, b)
	}
	return h, nil
}

// HostMemTotal returns the machine's RAM in bytes.
func HostMemTotal() (uint64, bool) {
	mem, err := readMemInfo()
	if err != nil {
		return 0, false
	}
	return mem.Total, true
}

// readHostCounters reads the same /proc files as ReadHost, without the
// 250 ms wait: the CPU counters (cumulative) and the memory now. The host
// collector takes the CPU use between two of these, 15 s apart.
func readHostCounters() (CPUTimes, MemInfo, error) {
	mem, err := readMemInfo()
	if err != nil {
		return CPUTimes{}, MemInfo{}, err
	}
	cpu, err := readCPU()
	if err != nil {
		return CPUTimes{}, MemInfo{}, err
	}
	return cpu, mem, nil
}

func readMemInfo() (MemInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemInfo{}, err
	}
	defer f.Close()
	return ParseMemInfo(f)
}

func readCPU() (CPUTimes, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUTimes{}, err
	}
	defer f.Close()
	return ParseCPUStat(f)
}
