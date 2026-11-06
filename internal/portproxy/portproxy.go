// Package portproxy is the forwarder behind a public port: `bitploy
// port-proxy` runs in a small locked-down container on an app's own network
// (bp-<slug>), listens on the port Docker publishes on the host, and copies
// each TCP connection to the app (like app:5432). It is how a database that
// has no web address can be reached from psql or TablePlus for a while,
// without publishing a port on the app container itself.
//
// It refuses connections from addresses outside the allow list, and at the
// expiry time it closes its listener and every open connection by itself, so
// the port closes on time even when bitploy is not running. One address may
// hold only a few of the connections, and a connection without traffic is
// closed after a while, so nobody can take every slot and lock the owner out.
package portproxy

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// MaxAllow is how many entries an allow list may have.
	MaxAllow = 20
	// DefaultMaxConns is how many connections run at the same time.
	DefaultMaxConns = 64
	// DefaultMaxPerAddr is how many of them may come from one address.
	DefaultMaxPerAddr = 8
	// DefaultIdleTimeout closes a connection that sent no bytes either way
	// for that long. A database client keeps a pool open; 30 minutes leaves
	// it alone during work and frees the slot once it is forgotten.
	DefaultIdleTimeout = 30 * time.Minute
	dialTimeout        = 5 * time.Second
)

// ParseAllow reads an allow list: IPv4 addresses (1.2.3.4) and networks
// (1.2.3.0/24). The result is normalized (1.2.3.4/32, 1.2.3.0/24) and has no
// duplicates. An empty list means every address. Public ports are IPv4 only,
// so an IPv6 entry is an error.
func ParseAllow(items []string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	seen := map[netip.Prefix]bool{}
	for _, raw := range items {
		it := strings.TrimSpace(raw)
		if it == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(it, "/") {
			pp, err := netip.ParsePrefix(it)
			if err != nil {
				return nil, fmt.Errorf("%q is not an address or a network like 203.0.113.7 or 203.0.113.0/24", it)
			}
			p = pp
		} else {
			a, err := netip.ParseAddr(it)
			if err != nil {
				return nil, fmt.Errorf("%q is not an address or a network like 203.0.113.7 or 203.0.113.0/24", it)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), max(p.Bits()-96, 0))
		}
		if !p.Addr().Is4() {
			return nil, fmt.Errorf("%q is an IPv6 address; public ports are IPv4 only, use your IPv4 address", it)
		}
		p = p.Masked()
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) > MaxAllow {
		return nil, fmt.Errorf("the list has %d entries; at most %d are allowed", len(out), MaxAllow)
	}
	return out, nil
}

// Allowed reports whether addr may connect under allow (empty: everyone).
func Allowed(allow []netip.Prefix, addr netip.Addr) bool {
	if len(allow) == 0 {
		return true
	}
	addr = addr.Unmap()
	for _, p := range allow {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Options configure Serve.
type Options struct {
	Target   string         // host:port to forward to, like app:5432
	Allow    []netip.Prefix // empty: every address
	Until    time.Time      // zero: no expiry
	MaxConns int            // 0: DefaultMaxConns
	// MaxPerAddr caps the connections from one source address; 0:
	// DefaultMaxPerAddr.
	MaxPerAddr int
	// IdleTimeout closes a connection without bytes in either direction for
	// that long; 0: DefaultIdleTimeout.
	IdleTimeout time.Duration
	Log         *slog.Logger
	// Dial connects to the target; nil uses a net.Dialer with a 5 s timeout.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Serve accepts connections on ln and forwards them until ctx ends or the
// expiry time passes. At the expiry the listener and every open connection
// are closed, and Serve returns ErrExpired.
func Serve(ctx context.Context, ln net.Listener, o Options) error {
	if o.MaxConns <= 0 {
		o.MaxConns = DefaultMaxConns
	}
	if o.MaxPerAddr <= 0 {
		o.MaxPerAddr = DefaultMaxPerAddr
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if o.Dial == nil {
		d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
		o.Dial = d.DialContext
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	expired := atomic.Bool{}
	if !o.Until.IsZero() {
		wait := time.Until(o.Until)
		if wait <= 0 {
			_ = ln.Close()
			return ErrExpired
		}
		t := time.AfterFunc(wait, func() { expired.Store(true); cancel() })
		defer t.Stop()
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()

	var (
		mu    sync.Mutex
		conns = map[net.Conn]struct{}{}
		perIP = map[netip.Addr]int{}
		wg    sync.WaitGroup
	)
	// take counts a connection from addr, or reports false when that address
	// has its share already.
	take := func(addr netip.Addr) bool {
		mu.Lock()
		defer mu.Unlock()
		if perIP[addr] >= o.MaxPerAddr {
			return false
		}
		perIP[addr]++
		return true
	}
	release := func(addr netip.Addr) {
		mu.Lock()
		defer mu.Unlock()
		if perIP[addr]--; perIP[addr] <= 0 {
			delete(perIP, addr)
		}
	}
	slots := make(chan struct{}, o.MaxConns)
	track := func(c net.Conn, on bool) {
		mu.Lock()
		defer mu.Unlock()
		if on {
			conns[c] = struct{}{}
		} else {
			delete(conns, c)
		}
	}
	defer func() {
		mu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				if expired.Load() {
					o.Log.Info("closed: the public port expired")
					return ErrExpired
				}
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		from := remoteAddr(c)
		if !Allowed(o.Allow, from) {
			o.Log.Info("refused: address not in the allow list", "from", from.String())
			_ = c.Close()
			continue
		}
		if !take(from) {
			o.Log.Warn("refused: too many connections from this address", "from", from.String(), "max", o.MaxPerAddr)
			_ = c.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			release(from)
			o.Log.Warn("refused: too many connections", "from", from.String(), "max", o.MaxConns)
			_ = c.Close()
			continue
		}
		track(c, true)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer release(from)
			defer track(c, false)
			forward(ctx, c, from, o, track)
		}()
	}
}

// ErrExpired is returned by Serve when the expiry time passed.
var ErrExpired = errors.New("the public port expired")

func remoteAddr(c net.Conn) netip.Addr {
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return ta.AddrPort().Addr().Unmap()
	}
	ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// forward copies one connection both ways until both sides are done.
func forward(ctx context.Context, c net.Conn, from netip.Addr, o Options, track func(net.Conn, bool)) {
	defer c.Close()
	start := time.Now()
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	up, err := o.Dial(dctx, "tcp", o.Target)
	cancel()
	if err != nil {
		o.Log.Warn("the app did not answer", "from", from.String(), "target", o.Target, "err", err.Error())
		return
	}
	track(up, true)
	defer track(up, false)
	defer up.Close()
	o.Log.Info("connected", "from", from.String())
	idle := newIdleWatch(o.IdleTimeout, func() {
		o.Log.Info("closed: no traffic", "from", from.String(), "idle_seconds", int(o.IdleTimeout.Seconds()))
		_ = c.Close()
		_ = up.Close()
	})
	defer idle.stop()
	var in, out int64
	done := make(chan struct{}, 2)
	go func() { in = pipe(up, c, idle); done <- struct{}{} }()
	go func() { out = pipe(c, up, idle); done <- struct{}{} }()
	<-done
	<-done
	o.Log.Info("disconnected", "from", from.String(), "bytes_in", in, "bytes_out", out, "seconds", int(time.Since(start).Seconds()))
}

// idleWatch calls fire once no bytes went either way for d. Reads only
// store the time; the timer checks it when it goes off and waits the rest.
type idleWatch struct {
	d    time.Duration
	last atomic.Int64 // unix nanoseconds of the last bytes
	t    *time.Timer
}

func newIdleWatch(d time.Duration, fire func()) *idleWatch {
	w := &idleWatch{d: d}
	w.touch()
	// Made stopped and started once w.t is set, so check never sees it nil.
	w.t = time.AfterFunc(time.Duration(math.MaxInt64), func() {
		if rest := w.d - time.Since(time.Unix(0, w.last.Load())); rest > 0 {
			w.t.Reset(rest)
			return
		}
		fire()
	})
	w.t.Reset(d)
	return w
}

func (w *idleWatch) touch() { w.last.Store(time.Now().UnixNano()) }
func (w *idleWatch) stop()  { w.t.Stop() }

// activeReader marks the watch each time bytes come in.
type activeReader struct {
	r io.Reader
	w *idleWatch
}

func (a activeReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.w.touch()
	}
	return n, err
}

// pipe copies src to dst and then closes the writing side of dst, so the
// other end sees the end of the stream while the other direction goes on.
func pipe(dst, src net.Conn, idle *idleWatch) int64 {
	n, _ := io.Copy(dst, activeReader{r: src, w: idle})
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		_ = dst.Close()
	}
	return n
}

// Main is `bitploy port-proxy --listen :30001 --target app:5432 [--allow
// 203.0.113.7/32,198.51.100.0/24] [--until <unix seconds>]`. Not for people:
// bitploy starts it in the forwarder container of a public port.
func Main(args []string) int {
	fs := flag.NewFlagSet("port-proxy", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to listen on, like :30001")
	target := fs.String("target", "", "where to forward to, like app:5432")
	allow := fs.String("allow", "", "addresses that may connect, comma-separated; empty: everyone")
	until := fs.String("until", "", "unix time at which the port closes; empty: never")
	maxConns := fs.Int("max-conns", DefaultMaxConns, "connections at the same time")
	maxPerAddr := fs.Int("max-per-addr", DefaultMaxPerAddr, "connections at the same time from one address")
	idle := fs.Duration("idle", DefaultIdleTimeout, "close a connection without traffic for this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if *listen == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "usage: bitploy port-proxy --listen :PORT --target HOST:PORT [--allow CIDR,...] [--until UNIX]")
		return 2
	}
	o := Options{Target: *target, MaxConns: *maxConns, MaxPerAddr: *maxPerAddr, IdleTimeout: *idle, Log: log}
	var err error
	if *allow != "" {
		if o.Allow, err = ParseAllow(strings.Split(*allow, ",")); err != nil {
			fmt.Fprintln(os.Stderr, "port-proxy:", err)
			return 2
		}
	}
	if *until != "" {
		n, err := strconv.ParseInt(*until, 10, 64)
		if err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "port-proxy: --until must be unix seconds")
			return 2
		}
		o.Until = time.Unix(n, 0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !o.Until.IsZero() && !time.Now().Before(o.Until) {
		// Expired: stay up without listening, so Docker's restart policy does
		// not start it over and over; bitploy removes the container.
		log.Info("closed: the public port expired")
		<-ctx.Done()
		return 0
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "port-proxy:", err)
		return 1
	}
	log.Info("listening", "listen", *listen, "target", *target, "allow", *allow, "until", *until)
	err = Serve(ctx, ln, o)
	if errors.Is(err, ErrExpired) {
		<-ctx.Done()
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "port-proxy:", err)
		return 1
	}
	return 0
}
