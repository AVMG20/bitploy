package app

// The server's VAPID key pair for browser notifications (notify.WebPush).
// It is made on first use and kept in the database like the GitHub App's
// private key: sealed with the server's secret key. A browser subscribes
// with the public key, so a new pair (a fresh database) means every
// browser has to turn notifications on again.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/AVMG20/bitploy/internal/notify"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const vapidAAD = "vapid"

type vapidState struct {
	mu   sync.Mutex
	keys *notify.VAPID
}

// vapidKeys returns the VAPID key pair, making it the first time.
func (s *Service) vapidKeys(ctx context.Context) (*notify.VAPID, error) {
	s.vapid.mu.Lock()
	defer s.vapid.mu.Unlock()
	if s.vapid.keys != nil {
		return s.vapid.keys, nil
	}
	row, err := s.Q.GetVapidKeys(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		key, gerr := notify.NewVAPID()
		if gerr != nil {
			return nil, gerr
		}
		raw, gerr := key.Bytes()
		if gerr != nil {
			return nil, gerr
		}
		pub := (&notify.VAPID{Key: key}).PublicKey()
		if err := s.Q.InsertVapidKeys(ctx, db.InsertVapidKeysParams{
			PublicKey: pub, PrivateKeyEnc: s.Crypto.Seal(raw, vapidAAD), CreatedAt: s.now(),
		}); err != nil {
			return nil, err
		}
		row, err = s.Q.GetVapidKeys(ctx) // another process may have won the insert
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.Crypto.Open(row.PrivateKeyEnc, vapidAAD)
	if err != nil {
		return nil, errors.New("the VAPID key cannot be decrypted (the server's secret key changed)")
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, err
	}
	s.vapid.keys = &notify.VAPID{Key: key, Subject: s.vapidSubject()}
	return s.vapid.keys, nil
}

// vapidSubject is the contact push services see: the dashboard's https
// address, or (no https in dev) a mailto: on its host.
func (s *Service) vapidSubject() string {
	pub := strings.TrimRight(s.Cfg.PublicURL, "/")
	if strings.HasPrefix(pub, "https://") {
		return pub
	}
	host := "localhost"
	if u, err := url.Parse(pub); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return "mailto:bitploy@" + host
}

// vapidPublicKey is the key browsers subscribe with, or "" when it cannot
// be had (then the dashboard does not offer browser notifications).
func (s *Service) vapidPublicKey(ctx context.Context) string {
	k, err := s.vapidKeys(ctx)
	if err != nil {
		s.Log.Error("VAPID key unavailable", "err", err)
		return ""
	}
	return k.PublicKey()
}
