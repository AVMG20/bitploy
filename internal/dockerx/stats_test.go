package dockerx

import (
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
)

func TestStatsFromResponse(t *testing.T) {
	tests := []struct {
		name       string
		resp       container.StatsResponse
		wantMem    uint64
		wantRx     uint64
		wantTx     uint64
		wantOnline uint32
	}{
		{
			name: "cgroup v2 inactive_file is subtracted",
			resp: container.StatsResponse{
				MemoryStats: container.MemoryStats{Usage: 300 << 20, Stats: map[string]uint64{"inactive_file": 100 << 20}},
				CPUStats:    container.CPUStats{OnlineCPUs: 4},
				Networks:    map[string]container.NetworkStats{"eth0": {RxBytes: 10, TxBytes: 20}, "eth1": {RxBytes: 5, TxBytes: 1}},
			},
			wantMem: 200 << 20, wantRx: 15, wantTx: 21, wantOnline: 4,
		},
		{
			name: "cgroup v1 total_inactive_file",
			resp: container.StatsResponse{
				MemoryStats: container.MemoryStats{Usage: 50 << 20, Stats: map[string]uint64{"total_inactive_file": 10 << 20}},
				CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{PercpuUsage: []uint64{1, 2}}},
			},
			wantMem: 40 << 20, wantOnline: 2,
		},
		{
			name: "cache larger than usage is ignored",
			resp: container.StatsResponse{
				MemoryStats: container.MemoryStats{Usage: 10, Stats: map[string]uint64{"inactive_file": 20}},
			},
			wantMem: 10, wantOnline: 0,
		},
		{
			name:    "no memory stats",
			resp:    container.StatsResponse{},
			wantMem: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := StatsFromResponse(&tt.resp)
			if got := s.MemWorkingSet(); got != tt.wantMem {
				t.Errorf("MemWorkingSet = %d, want %d", got, tt.wantMem)
			}
			if s.RxBytes != tt.wantRx || s.TxBytes != tt.wantTx {
				t.Errorf("rx/tx = %d/%d, want %d/%d", s.RxBytes, s.TxBytes, tt.wantRx, tt.wantTx)
			}
			if s.OnlineCPUs != tt.wantOnline {
				t.Errorf("OnlineCPUs = %d, want %d", s.OnlineCPUs, tt.wantOnline)
			}
		})
	}
}

func TestParseEvent(t *testing.T) {
	labels := func(extra map[string]string) map[string]string {
		m := map[string]string{LabelManaged: "1", LabelAppID: "app_1", LabelSlug: "quiet-otter-3f9a", "name": "bp-quiet-otter-3f9a-7"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	tests := []struct {
		name       string
		msg        events.Message
		ok         bool
		action     string
		health     string
		exitCode   string
		wantName   string
		wantAppID  string
		checkTimes bool
	}{
		{name: "die with exit code", msg: events.Message{Type: events.ContainerEventType, Action: "die", Actor: events.Actor{ID: "c1", Attributes: labels(map[string]string{"exitCode": "137"})}, TimeNano: 5e9},
			ok: true, action: "die", exitCode: "137", wantName: "bp-quiet-otter-3f9a-7", wantAppID: "app_1"},
		{name: "oom", msg: events.Message{Type: events.ContainerEventType, Action: "oom", Actor: events.Actor{ID: "c1", Attributes: labels(nil)}}, ok: true, action: "oom", wantName: "bp-quiet-otter-3f9a-7", wantAppID: "app_1"},
		{name: "health unhealthy", msg: events.Message{Type: events.ContainerEventType, Action: "health_status: unhealthy", Actor: events.Actor{ID: "c1", Attributes: labels(nil)}}, ok: true, action: "health_status", health: "unhealthy", wantName: "bp-quiet-otter-3f9a-7", wantAppID: "app_1"},
		{name: "health healthy", msg: events.Message{Type: events.ContainerEventType, Action: "health_status: healthy", Actor: events.Actor{ID: "c1", Attributes: labels(nil)}}, ok: true, action: "health_status", health: "healthy", wantName: "bp-quiet-otter-3f9a-7", wantAppID: "app_1"},
		{name: "exec events are ignored", msg: events.Message{Type: events.ContainerEventType, Action: "exec_die", Actor: events.Actor{ID: "c1", Attributes: labels(nil)}}},
		{name: "not a container", msg: events.Message{Type: events.NetworkEventType, Action: "die", Actor: events.Actor{Attributes: labels(nil)}}},
		{name: "not managed", msg: events.Message{Type: events.ContainerEventType, Action: "die", Actor: events.Actor{ID: "x", Attributes: map[string]string{"name": "redis"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := ParseEvent(tt.msg)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if ev.Action != tt.action || ev.Health != tt.health || ev.ExitCode != tt.exitCode || ev.Name != tt.wantName || ev.AppID != tt.wantAppID {
				t.Errorf("got %+v", ev)
			}
		})
	}
	ev, _ := ParseEvent(events.Message{Type: events.ContainerEventType, Action: "die", Actor: events.Actor{Attributes: labels(nil)}, TimeNano: 5e9})
	if !ev.Time.Equal(time.Unix(5, 0)) {
		t.Errorf("time = %v", ev.Time)
	}
}

func TestContainerIDFromMountinfo(t *testing.T) {
	id := "3f1c2a9b8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a"
	tests := []struct{ name, in, want string }{
		{"docker hostname mount", "1234 1200 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw\n", id},
		{"after other lines", "1 0 0:1 / / rw - overlay overlay rw\n2 1 8:1 /docker/containers/" + id + "/resolv.conf /etc/resolv.conf rw - ext4 /dev/sda1 rw\n", id},
		{"not hex", "1 0 8:1 /var/lib/docker/containers/" + strings.Repeat("z", 64) + "/hostname /etc/hostname rw - ext4 /dev/sda1 rw\n", ""},
		{"too short", "1 0 8:1 /var/lib/docker/containers/abc/hostname /etc/hostname rw - ext4 /dev/sda1 rw\n", ""},
		{"second marker on the line", "1 0 8:1 /containers/x/containers/" + id + "/hosts /etc/hosts rw - ext4 /dev/sda1 rw\n", id},
		{"outside docker", "22 1 0:21 / /proc rw - proc proc rw\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerIDFromMountinfo(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
