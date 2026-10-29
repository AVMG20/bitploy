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

	"github.com/arno/bitploy/internal/api"
	"github.com/arno/bitploy/internal/app"
	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/config"
	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/mcp"
	"github.com/arno/bitploy/internal/proxy"
	"github.com/arno/bitploy/internal/store"
	"github.com/arno/bitploy/web"
)

func main() {
	log := slog.New(newRedactingHandler(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.SetDefault(log)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(log)
	case "migrate":
		_, _, err = open(context.Background())
	case "admin":
		err = admin(log, os.Args[2:])
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
	if err := svc.Routes.WriteCatchAll(svc.Cfg.AppsDomain, svc.Cfg.InternalURL); err != nil {
		return fmt.Errorf("write catch-all route: %w", err)
	}

	go svc.RunReconciler(ctx)
	go svc.RunWorker(ctx)
	go svc.RunBackground(ctx)
	go svc.RunAccountsMaintenance(ctx)

	srv := &http.Server{
		Addr:              svc.Cfg.Listen,
		Handler:           api.New(svc, mcp.Handler(svc), web.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("bitploy listening", "addr", srv.Addr, "public_url", svc.Cfg.PublicURL)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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
