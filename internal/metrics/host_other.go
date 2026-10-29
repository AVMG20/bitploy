//go:build !linux

package metrics

import (
	"context"
	"errors"
)

// HostSupported is false off Linux (local development on macOS).
const HostSupported = false

type Host struct {
	CPUPercent float64
	Load1      float64
	MemTotal   uint64
	MemUsed    uint64
}

func ReadHost(ctx context.Context) (Host, error) {
	return Host{}, errors.New("host stats need Linux /proc")
}

func HostMemTotal() (uint64, bool) { return 0, false }
