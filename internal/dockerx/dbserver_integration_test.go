//go:build integration

package dockerx

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/dbview"
	"github.com/AVMG20/bitploy/internal/ids"
)

// The helper for a database server joins the network of the database's
// container and reaches it at 127.0.0.1, without a port published anywhere.
// Run: go test -tags=integration -count=1 -run DBServerHelper ./internal/dockerx/
func TestDBServerHelperIntegration(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Skip("no docker: ", err)
	}
	defer c.Close()
	c.Instance = "test-" + strings.ToLower(ids.Hex(5))
	info, err := c.API.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	goarch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	if goarch == "" {
		t.Skip("unknown docker architecture ", info.Info.Architecture)
	}
	bin := filepath.Join(t.TempDir(), "bitploy-db-helper")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "../../cmd/bitploy")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the helper binary: %v\n%s", err, out)
	}
	c.DBHelperBin = bin

	name := "bp-test-pg-" + strings.ToLower(ids.Hex(5))
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "-e", "POSTGRES_USER=app", "-e", "POSTGRES_PASSWORD=pw", "-e", "POSTGRES_DB=app", "postgres:17-alpine").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = c.RemoveImage(context.Background(), DBHelperImage+":"+c.dbh.tag)
	})

	req, _ := json.Marshal(dbview.Request{Op: dbview.OpSQL, SQL: "SELECT current_user, current_database()",
		Server: &dbview.ServerConn{Engine: dbview.EnginePostgres, Port: 5432, User: "app", Password: "pw", Database: "app"}})
	var resp dbview.Response
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, err := c.RunDBServerHelper(ctx, name, req)
		if err != nil {
			t.Fatal(err)
		}
		resp = dbview.Response{}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if resp.Error == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if resp.Error != nil {
		t.Fatalf("helper: %+v", resp.Error)
	}
	var res dbview.SQLResult
	if err := dbview.Decode(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	if got := res.Results[0].Rows[0]; got[0] != "app" || got[1] != "app" {
		t.Fatalf("rows = %v", res.Results[0].Rows)
	}
}
