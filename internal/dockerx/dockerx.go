// Package dockerx is a thin wrapper over the moby client. Add helpers in
// per-area files (containers.go, networks.go, stats.go, …); keep them small.
package dockerx

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

// Client wraps the moby API client. API is exported so callers can reach
// anything the wrapper does not cover yet.
type Client struct {
	API *client.Client
}

// New connects to the Docker daemon from DOCKER_HOST (or the default socket).
func New(ctx context.Context) (*Client, error) {
	api, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	if _, err := api.Ping(ctx, client.PingOptions{}); err != nil {
		return nil, fmt.Errorf("cannot reach the Docker daemon: %w", err)
	}
	return &Client{API: api}, nil
}

func (c *Client) Close() error { return c.API.Close() }

// Names used for every app; keep them in one place.
func NetworkName(slug string) string              { return "bp-" + slug }
func ContainerName(slug string, seq int64) string { return fmt.Sprintf("bp-%s-%d", slug, seq) }
func ImageName(slug string, seq int64) string     { return fmt.Sprintf("bitploy/%s:%d", slug, seq) }
func VolumeName(slug string) string               { return "bp-" + slug + "-data" }

// Labels set on every container, network, image and volume bitploy owns.
const (
	LabelManaged    = "nl.bitploy.managed" // "1"
	LabelAppID      = "nl.bitploy.app_id"
	LabelSlug       = "nl.bitploy.slug"
	LabelDeployment = "nl.bitploy.deployment_id"
	LabelSeq        = "nl.bitploy.seq"
)
