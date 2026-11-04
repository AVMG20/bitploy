package app

// OWNER: deploy agent. Build and runtime logs.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	maxLogTail     = 1000
	defaultLogTail = 200
	maxBuildTail   = 10000
)

func clampTail(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func buildLines(lines []string) []LogLine {
	out := make([]LogLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, LogLine{Stream: "build", Text: l})
	}
	return out
}

// BuildLog returns the last tail lines (default 200, max 10,000) of a deployment's build log.
func (s *Service) BuildLog(ctx context.Context, a Actor, deploymentID string, tail int) ([]LogLine, error) {
	d, _, err := s.loadDeployment(ctx, a, deploymentID)
	if err != nil {
		return nil, err
	}
	return buildLines(tailFile(s.Cfg.BuildLog(d.ID), clampTail(tail, defaultLogTail, maxBuildTail))), nil
}

// FollowBuildLog streams the build log until the deployment is terminal or ctx ends.
func (s *Service) FollowBuildLog(ctx context.Context, a Actor, deploymentID string, fn func(LogLine) error) error {
	d, _, err := s.loadDeployment(ctx, a, deploymentID)
	if err != nil {
		return err
	}
	path := s.Cfg.BuildLog(d.ID)
	var off int64
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	var partial []byte
	emit := func(b []byte) error {
		partial = append(partial, b...)
		for {
			i := bytes.IndexByte(partial, '\n')
			if i < 0 {
				return nil
			}
			line := string(partial[:i])
			partial = partial[i+1:]
			if err := fn(LogLine{Stream: "build", Text: line}); err != nil {
				return err
			}
		}
	}
	buf := make([]byte, 32<<10)
	for {
		if f == nil {
			if f, err = os.Open(path); err != nil {
				f = nil
			} else if st, serr := f.Stat(); serr == nil && st.Size() > 256<<10 {
				off = st.Size() - 256<<10 // very long log: start near the end
				if _, err := f.Seek(off, io.SeekStart); err != nil {
					return apperr.Internal(err)
				}
				skip := true
				for skip { // drop the partial first line
					n, rerr := f.Read(buf[:1])
					off += int64(n)
					if rerr != nil || buf[0] == '\n' {
						skip = false
					}
				}
			}
		}
		if f != nil {
			for {
				n, rerr := f.Read(buf)
				if n > 0 {
					off += int64(n)
					if err := emit(buf[:n]); err != nil {
						return err
					}
				}
				if rerr != nil || n == 0 {
					break
				}
			}
		}
		cur, gerr := s.Q.GetDeployment(ctx, d.ID)
		if gerr != nil {
			return nil // deployment (app) deleted
		}
		if v := cur.State; v == "running" || v == "failed" || v == "superseded" || v == "cancelled" {
			// One more read after the terminal state so the last lines are not lost.
			if f != nil {
				for {
					n, rerr := f.Read(buf)
					if n > 0 {
						if err := emit(buf[:n]); err != nil {
							return err
						}
					}
					if rerr != nil || n == 0 {
						break
					}
				}
			}
			if len(partial) > 0 {
				return fn(LogLine{Stream: "build", Text: string(partial)})
			}
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		s.WaitDeployment(ctx, d.ID, 500*time.Millisecond)
	}
}

// Logs returns build (latest deployment) or runtime logs for an app, max 1000 lines.
func (s *Service) Logs(ctx context.Context, a Actor, ref string, opts LogOptions) ([]LogLine, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return nil, err
	}
	tail := clampTail(opts.Tail, defaultLogTail, maxLogTail)
	switch opts.Kind {
	case "build", "":
		deps, err := s.Q.ListDeploymentsByApp(ctx, db.ListDeploymentsByAppParams{AppID: row.ID, Seq: 1 << 62, Limit: 1})
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if len(deps) == 0 {
			return []LogLine{}, nil
		}
		return buildLines(tailFile(s.Cfg.BuildLog(deps[0].ID), tail)), nil
	case "runtime":
		name, svc, err := s.runtimeContainer(ctx, row, opts.Service)
		if err != nil {
			return nil, err
		}
		var since time.Time
		if opts.Since != nil {
			since = *opts.Since
		}
		entries, err := s.Docker.TailLogs(ctx, name, tail, since)
		if err != nil {
			if dockerx.IsNotFound(err) {
				return nil, noContainerErr()
			}
			return nil, apperr.Internal(err)
		}
		out := make([]LogLine, 0, len(entries))
		for _, e := range entries {
			l := runtimeLine(e)
			l.Service = svc
			out = append(out, l)
		}
		return out, nil
	}
	return nil, apperr.BadRequest("log_kind_invalid", "The log kind "+quote(opts.Kind)+" is not known.", "Use kind=build or kind=runtime.")
}

// FollowRuntimeLogs streams runtime logs from the current container. An
// optional service name picks the service; without it the public (or only)
// service is followed.
func (s *Service) FollowRuntimeLogs(ctx context.Context, a Actor, ref string, tail int, fn func(LogLine) error, service ...string) error {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return err
	}
	name, svc, err := s.runtimeContainer(ctx, row, firstOpt(service))
	if err != nil {
		return err
	}
	err = s.Docker.StreamLogs(ctx, name, dockerx.LogOptions{Tail: clampTail(tail, defaultLogTail, maxLogTail), Follow: true}, func(e dockerx.LogEntry) error {
		l := runtimeLine(e)
		l.Service = svc
		return fn(l)
	})
	if dockerx.IsNotFound(err) {
		return noContainerErr()
	}
	if err != nil && ctx.Err() == nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return ae
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err // the callback's own error (client went away)
	}
	return nil
}

func runtimeLine(e dockerx.LogEntry) LogLine {
	l := LogLine{Stream: e.Stream, Text: apperr.Redact(e.Text)}
	if !e.Time.IsZero() {
		t := e.Time
		l.Time = &t
	}
	return l
}

func noContainerErr() *apperr.Error {
	return apperr.Conflict("no_container", "This app has no container yet, so there are no runtime logs.",
		"Deploy it first, then read the logs. For a failed deployment, get_deployment shows the log tail.").WithNext("get_deployment")
}

func serviceNoContainerErr(service string) *apperr.Error {
	return apperr.Conflict("no_container", "The "+service+" service has no container yet, so there are no runtime logs.",
		"Deploy the app first, then read the logs. For a failed deployment, get_deployment shows the log tail.").
		WithDetail("service", service).WithNext("get_deployment")
}

// runtimeContainer picks the container whose logs to read: the named
// service's, else the public service's, else the first service's. svc is the
// service name for an app with several services and "" otherwise, so the
// lines of a single-service app look as they always did.
func (s *Service) runtimeContainer(ctx context.Context, row db.App, service string) (name, svc string, err error) {
	sp, specs, err := s.pickService(ctx, row, service)
	if err != nil {
		return "", "", err
	}
	if s.Docker == nil {
		return "", "", apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	if !row.CurrentDeploymentID.Valid {
		return "", "", noContainerErr()
	}
	if !isMulti(row) {
		d, err := s.Q.GetDeployment(ctx, row.CurrentDeploymentID.String)
		if err != nil || !d.ContainerName.Valid {
			return "", "", noContainerErr()
		}
		return d.ContainerName.String, "", nil
	}
	if sp == nil {
		if sp = publicSpec(specs); sp == nil && len(specs) > 0 {
			sp = &specs[0]
		}
	}
	if sp == nil {
		return "", "", noContainerErr()
	}
	name = s.currentContainers(ctx, row)[sp.Name]
	if name == "" {
		return "", "", serviceNoContainerErr(sp.Name)
	}
	return name, sp.Name, nil
}
