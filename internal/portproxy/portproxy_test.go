package portproxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestParseAllow(t *testing.T) {
	got, err := ParseAllow([]string{" 203.0.113.7 ", "198.51.100.9/24", "203.0.113.7/32", "", "::ffff:192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.7/32", "198.51.100.0/24", "192.0.2.1/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"2001:db8::1", "2001:db8::/32", "example.com", "1.2.3.4/33", "1.2.3"} {
		if _, err := ParseAllow([]string{bad}); err == nil {
			t.Errorf("ParseAllow(%q) must fail", bad)
		}
	}
	many := make([]string, MaxAllow+1)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}).String()
	}
	if _, err := ParseAllow(many); err == nil {
		t.Error("too many entries must fail")
	}
	if empty, err := ParseAllow(nil); err != nil || len(empty) != 0 {
		t.Errorf("empty list: %v %v", empty, err)
	}
}

func TestAllowed(t *testing.T) {
	allow, _ := ParseAllow([]string{"10.1.0.0/16"})
	if !Allowed(allow, netip.MustParseAddr("10.1.2.3")) || Allowed(allow, netip.MustParseAddr("10.2.0.1")) {
		t.Fatal("prefix match is wrong")
	}
	if !Allowed(allow, netip.MustParseAddr("::ffff:10.1.2.3")) {
		t.Fatal("a mapped IPv4 address must match")
	}
	if !Allowed(nil, netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("an empty list allows everyone")
	}
}

// echoServer answers every line with the same line in upper case.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := io.WriteString(c, strings.ToUpper(line)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func startProxy(t *testing.T, o Options) (addr string, done chan error, cancel context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- Serve(ctx, ln, o) }()
	t.Cleanup(cancel)
	return ln.Addr().String(), done, cancel
}

func roundTrip(c net.Conn, msg string) (string, error) {
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(c, msg+"\n"); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}

func TestServeForwards(t *testing.T) {
	addr, done, cancel := startProxy(t, Options{Target: echoServer(t)})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "select 1"); err != nil || got != "SELECT 1" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

func TestServeRefusesOthers(t *testing.T) {
	allow, _ := ParseAllow([]string{"192.0.2.0/24"}) // not 127.0.0.1
	addr, _, _ := startProxy(t, Options{Target: echoServer(t), Allow: allow})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "hello"); err == nil {
		t.Fatalf("a connection from outside the allow list was forwarded: %q", got)
	}
}

func TestServeExpiryClosesEverything(t *testing.T) {
	addr, done, _ := startProxy(t, Options{Target: echoServer(t), Until: time.Now().Add(400 * time.Millisecond)})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "a"); err != nil || got != "A" {
		t.Fatalf("before the expiry: %q %v", got, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("Serve = %v, want ErrExpired", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop at the expiry")
	}
	// The open connection was closed too.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("the open connection survived the expiry")
	}
	if c2, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c2.Close()
		t.Fatal("the listener is still open after the expiry")
	}
}

func TestServeAlreadyExpired(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), ln, Options{Target: "127.0.0.1:1", Until: time.Now().Add(-time.Second)}); !errors.Is(err, ErrExpired) {
		t.Fatalf("Serve = %v", err)
	}
}

func TestServeMaxConns(t *testing.T) {
	addr, _, _ := startProxy(t, Options{Target: echoServer(t), MaxConns: 1})
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	if _, err := roundTrip(c1, "one"); err != nil {
		t.Fatal(err)
	}
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got, err := roundTrip(c2, "two"); err == nil {
		t.Fatalf("a second connection over the limit was forwarded: %q", got)
	}
}
