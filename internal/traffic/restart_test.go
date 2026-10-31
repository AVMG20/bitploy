package traffic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// restart simulates bitploy starting again on the same database and log file.
func (e *mgrEnv) restart() {
	e.m = New(e.st, slog.New(slog.NewTextHandler(io.Discard, nil)), e.log, nil)
	e.m.Now = func() time.Time { return e.now }
}

func (e *mgrEnv) appendLog(lines ...string) {
	e.t.Helper()
	f, err := os.OpenFile(e.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		e.t.Fatal(err)
	}
}

func (e *mgrEnv) requests() int64 {
	return e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_daily WHERE app_id = 'app_1'`)
}

func TestRestartDoesNotCountLinesTwice(t *testing.T) {
	e := newMgrEnv(t)
	e.write(
		logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200),
		logLine("app-quiet-otter-3f9a@file", "websecure", "/a", "203.0.113.2", 200),
		logLine("app-quiet-otter-3f9a@file", "websecure", "/a", "203.0.113.3", 200),
	)
	e.run()
	if got := e.requests(); got != 3 {
		t.Fatalf("requests = %d", got)
	}
	e.restart()
	e.run()
	if got := e.requests(); got != 3 {
		t.Errorf("after a restart: %d requests, want 3", got)
	}
	if got := e.count(`SELECT views FROM traffic_paths WHERE app_id = 'app_1' AND path = '/a'`); got != 2 {
		t.Errorf("page views after a restart: %d", got)
	}
	if got := e.count(`SELECT SUM(visitors) FROM traffic_daily WHERE app_id = 'app_1'`); got != 3 {
		t.Errorf("visitors after a restart: %d", got)
	}
	// New lines after the restart are counted, once.
	e.appendLog(logLine("app-quiet-otter-3f9a@file", "websecure", "/b", "203.0.113.4", 200))
	e.run()
	e.restart()
	e.run()
	if got := e.requests(); got != 4 {
		t.Errorf("after new traffic: %d requests, want 4", got)
	}
}

func TestRestartReadsAgainWhatWasNotSaved(t *testing.T) {
	e := newMgrEnv(t)
	e.write(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200))
	e.m.Poll(context.Background()) // read, but never flushed (a crash)
	e.restart()
	e.run()
	if got := e.requests(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestRestartWithNewLogFileStartsAtTheTop(t *testing.T) {
	e := newMgrEnv(t)
	e.write(logLine("app-quiet-otter-3f9a@file", "websecure", "/old", "203.0.113.1", 200))
	e.run()
	// While bitploy was down the file was cut and filled again, past the old position.
	var lines []string
	for i := 0; i < 5; i++ {
		lines = append(lines, logLine("app-quiet-otter-3f9a@file", "websecure", fmt.Sprintf("/new%d", i), "203.0.113.9", 200))
	}
	e.write(lines...)
	e.restart()
	e.run()
	if got := e.requests(); got != 6 {
		t.Errorf("requests = %d, want 6 (1 before, 5 in the new file)", got)
	}
}

func TestRestartAfterTruncationByBitploy(t *testing.T) {
	e := newMgrEnv(t)
	e.m.tail.TruncateAt = 100 // small, so the first poll truncates
	e.write(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200))
	e.run()
	if st, _ := os.Stat(e.log); st.Size() != 0 {
		t.Fatalf("log not truncated: %d bytes", st.Size())
	}
	e.appendLog(logLine("app-quiet-otter-3f9a@file", "websecure", "/x", "203.0.113.2", 200))
	e.restart()
	e.run()
	if got := e.requests(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestFailedFlushKeepsCountsForTheNext(t *testing.T) {
	e := newMgrEnv(t)
	ctx := context.Background()
	e.write(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200))
	e.m.Poll(ctx)
	e.exec(`ALTER TABLE traffic_paths RENAME TO traffic_paths_away`) // makes the save fail
	e.m.Flush(ctx)
	if got := e.count(`SELECT COUNT(*) FROM traffic_daily`); got != 0 {
		t.Fatalf("a failed flush left %d rows", got)
	}
	if got := e.count(`SELECT COUNT(*) FROM traffic_cursor`); got != 0 {
		t.Fatalf("the position moved on although nothing was saved")
	}
	e.exec(`ALTER TABLE traffic_paths_away RENAME TO traffic_paths`)
	e.appendLog(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200))
	e.run()
	if got := e.requests(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if got := e.count(`SELECT visitors FROM traffic_daily WHERE app_id = 'app_1'`); got != 1 {
		t.Errorf("visitors = %d, want 1", got)
	}
}
