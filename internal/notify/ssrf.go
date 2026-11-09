package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

// ErrBlockedAddress: the host resolved to an address bitploy never sends to.
var ErrBlockedAddress = errors.New("address not allowed")

// blockedPrefixes are the networks a notification never goes to: the
// server itself, its private networks (Docker's, the cloud provider's) and
// the metadata service. Checked at dial time, after DNS, so a public name
// that resolves to 127.0.0.1 is caught too.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/96", "::1/128", "::ffff:0:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
		"2001::/32", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicAddr reports whether ip is a public unicast address.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// defaultClient is the SafeClient senders share when they are given none,
// so connections to Discord and the push services are reused.
var defaultClient = sync.OnceValue(func() *http.Client { return SafeClient(15 * time.Second) })

// SafeClient is an HTTP client for addresses that come from users: it
// connects only to public addresses (checked on every dial, so DNS
// rebinding does not help), ignores proxy settings, follows no redirects
// and gives up after timeout. Senders whose URL is not fixed (ntfy, a
// generic webhook) must use it; Discord uses it as well.
func SafeClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !PublicAddr(ap.Addr()) {
				return ErrBlockedAddress
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           func(ctx context.Context, n, a string) (net.Conn, error) { return d.DialContext(ctx, n, a) },
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
