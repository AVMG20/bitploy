// Command bitploy is the whole control plane: dashboard, REST API, MCP
// server, deploy worker, reconciler and background jobs.
//
//	bitploy serve
//	bitploy migrate
//	bitploy admin create-user --email you@x.nl [--name You] [--admin]
//	bitploy admin invite [--email someone@x.nl] [--admin]
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/AVMG20/bitploy/internal/api"
	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/dbview"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/mcp"
	"github.com/AVMG20/bitploy/internal/portproxy"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/web"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	log := slog.New(newRedactingHandler(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.SetDefault(log)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
		return
	case "serve":
		err = serve(log)
	case "migrate":
		_, _, err = open(context.Background())
	case "admin":
		err = admin(log, os.Args[2:])
	case "db-helper":
		// Not for people: the SQLite viewer runs this in a helper container (and, with --control, as a process, for the admin viewer of bitploy's own database)
		// (internal/dbview). It reads one JSON request and prints one response.
		os.Exit(dbview.HelperMain(os.Args[2:], os.Stdin, os.Stdout))
	case "port-proxy":
		// Not for people: the forwarder container of a public port runs this
		// (internal/portproxy).
		os.Exit(portproxy.Main(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  bitploy serve
  bitploy migrate
  bitploy admin create-user --email you@x.nl [--name You] [--admin]
  bitploy admin invite [--email someone@x.nl] [--admin]`)
}

func open(ctx context.Context) (*config.Config, *store.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return cfg, st, nil
}

func newService(ctx context.Context, log *slog.Logger, needDocker bool) (*app.Service, error) {
	cfg, st, err := open(ctx)
	if err != nil {
		return nil, err
	}
	crypto, err := store.NewCrypto(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	var dk *dockerx.Client
	if needDocker {
		dk, err = dockerx.New(ctx)
		if err != nil {
			return nil, err
		}
		// Everything bitploy creates in Docker carries this id, and cleanup only touches resources with it.
		if dk.Instance, err = st.InstanceID(ctx); err != nil {
			return nil, err
		}
		dk.DBHelperBin = cfg.DBHelperBin
	}
	routes := &proxy.Writer{Dir: cfg.RoutesDir(), EntryPoint: "websecure", TLS: true}
	if cfg.Dev {
		routes.EntryPoint, routes.TLS = "web", false
	}
	return app.New(cfg, st, crypto, dk, routes, log), nil
}

func serve(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc, err := newService(ctx, log, true)
	if err != nil {
		return err
	}
	// The catch-all route for every app domain and the certificates of the
	// extra domains (both rewritten whenever an admin changes the domains).
	if err := svc.ApplyAppDomains(ctx); err != nil {
		return fmt.Errorf("write catch-all route and app domain certificates: %w", err)
	}

	go svc.RunReconciler(ctx)
	go svc.RunWorker(ctx)
	bgDone := make(chan struct{})
	go func() {
		defer close(bgDone)
		svc.RunBackground(ctx)
	}()
	go svc.RunAccountsMaintenance(ctx)

	srv := &http.Server{
		Addr:              svc.Cfg.Listen,
		Handler:           api.New(svc, mcp.Handler(svc), web.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// No ReadTimeout/WriteTimeout on purpose: they would cut SSE log
		// streams, long-polls and 95 MB uploads. internal/api sets per-route
		// body read deadlines (API 30 s, /mcp 60 s, /u/{id} 15 min).
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("bitploy listening", "version", version, "addr", srv.Addr, "public_url", svc.Cfg.PublicURL)
		errCh <- srv.ListenAndServe()
	}()
	var serveErr error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	stop() // also when the server failed: the background jobs end and save what they hold
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	// Let the background jobs finish: traffic counts are saved and running backups thaw their app.
	select {
	case <-bgDone:
	case <-time.After(5 * time.Second):
		log.Warn("background jobs did not stop in time")
	}
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
}

func admin(log *slog.Logger, args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing admin command")
	}
	ctx := context.Background()
	switch args[0] {
	case "create-user":
		fs := flag.NewFlagSet("create-user", flag.ExitOnError)
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name (default: part of the email before @)")
		isAdmin := fs.Bool("admin", false, "make the user an admin")
		_ = fs.Parse(args[1:])
		if *email == "" {
			return errors.New("--email is required")
		}
		if *name == "" {
			*name = strings.Split(*email, "@")[0]
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		svc, err := newService(ctx, log, false)
		if err != nil {
			return err
		}
		u, err := svc.CreateUser(ctx, *email, *name, pw, *isAdmin)
		if err != nil {
			return describe(err)
		}
		fmt.Printf("Created %s (%s, role %s). Log in at %s\n", u.Email, u.ID, u.Role, svc.Cfg.PublicURL)
		return nil
	case "invite":
		fs := flag.NewFlagSet("invite", flag.ExitOnError)
		email := fs.String("email", "", "lock the invite to this email (optional)")
		isAdmin := fs.Bool("admin", false, "invite as admin")
		_ = fs.Parse(args[1:])
		svc, err := newService(ctx, log, false)
		if err != nil {
			return err
		}
		role := "user"
		if *isAdmin {
			role = "admin"
		}
		a := app.System
		a.Scope = app.ScopeSession
		inv, err := svc.CreateInvite(ctx, a, app.CreateInviteInput{Email: *email, Role: role})
		if err != nil {
			return describe(err)
		}
		fmt.Printf("Invite link (valid until %s):\n%s\n", inv.ExpiresAt.Format(time.RFC3339), inv.URL)
		return nil
	}
	usage()
	return fmt.Errorf("unknown admin command %q", args[0])
}

func describe(err error) error {
	e := apperr.As(err)
	if e.Hint != "" {
		return fmt.Errorf("%s (%s). %s", e.Message, e.Code, e.Hint)
	}
	return fmt.Errorf("%s (%s)", e.Message, e.Code)
}

func readPassword() (string, error) {
	if v := os.Getenv("BITPLOY_ADMIN_PASSWORD"); v != "" {
		return v, nil
	}
	fmt.Fprint(os.Stderr, "Password (min 10 characters): ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}
