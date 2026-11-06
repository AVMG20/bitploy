// Package dockerx is a thin wrapper over the moby client. Add helpers in
// per-area files (containers.go, networks.go, stats.go, …); keep them small.
package dockerx

import (
	"context"
	"errors"
	"fmt"

	"github.com/moby/moby/client"
)

// Client wraps the moby API client. API is exported so callers can reach
// anything the wrapper does not cover yet.
type Client struct {
	API *client.Client
	// Instance is the id of this bitploy installation (store.InstanceID). It is
	// put on everything the client creates (nl.bitploy.instance), and every
	// list, prune and event call is limited to resources carrying it, so two
	// installations (or a test run) sharing one daemon never touch each other's
	// containers, networks, volumes and images. Empty means "not set": the
	// list calls then fail with ErrNoInstance instead of guessing.
	Instance string
	// DBHelperBin is a Linux bitploy binary for the SQLite viewer's helper
	// image (BITPLOY_DB_HELPER_BIN). Empty: use the running binary, which only
	// works when bitploy itself runs on Linux.
	DBHelperBin string

	dbh dbHelperState
}

// ErrNoInstance is returned by the list/prune/event calls when Client.Instance is empty.
var ErrNoInstance = errors.New("dockerx: the client has no instance id, refusing to touch managed resources")

// ownedFilter selects bitploy resources of this installation, plus extra
// "key=value" label conditions.
func (c *Client) ownedFilter(extra ...string) (client.Filters, error) {
	if c.Instance == "" {
		return nil, ErrNoInstance
	}
	return client.Filters{}.Add("label", append([]string{LabelManaged + "=1", LabelInstance + "=" + c.Instance}, extra...)...), nil
}

// appFilter selects the resources of one app by its id. The app id comes from
// this installation's database, so resources from before instance ids (no
// instance label) are included; ones labelled for another installation are not
// (see ownedOrLegacy).
func (c *Client) appFilter(appID string) (client.Filters, error) {
	if c.Instance == "" {
		return nil, ErrNoInstance
	}
	return client.Filters{}.Add("label", LabelManaged+"=1", LabelAppID+"="+appID), nil
}

// ownedOrLegacy is true for this installation's resources and for ones that
// predate instance ids; false for a different installation's.
func (c *Client) ownedOrLegacy(labels map[string]string) bool {
	return labels[LabelManaged] == "1" && (labels[LabelInstance] == "" || labels[LabelInstance] == c.Instance)
}

// owned reports whether a resource's labels say it belongs to this installation.
func (c *Client) owned(labels map[string]string) bool {
	return c.Instance != "" && labels[LabelManaged] == "1" && labels[LabelInstance] == c.Instance
}

// ManagedLabels are set on every container, network, image and volume bitploy
// owns: the package-level ManagedLabels plus this installation's instance id.
func (c *Client) ManagedLabels(appID, slug string) map[string]string {
	l := ManagedLabels(appID, slug)
	if c.Instance != "" {
		l[LabelInstance] = c.Instance
	}
	return l
}

// New connects to the Docker daemon from DOCKER_HOST (or the default socket).
func New(ctx context.Context) (*Client, error) {
	api, err := client.New(client.FromEnv)
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

// BeforeDeployName is the one-off container that runs the before-deploy
// command of deployment seq. It ends in a letter, so it never equals the
// bp-<slug>-<n> of another app.
func BeforeDeployName(slug string, seq int64) string {
	return fmt.Sprintf("bp-%s-%d-before", slug, seq)
}

// Names for the services of a multi-service app. The "_" between slug and
// service cannot occur in either, so bp-<slug>_<service>-<n> never equals
// another app's bp-<slug>-<n> (app "shop" with service "web" against an app
// named "shop-web"). Docker DNS and Go's URL parser accept the "_".
func ServiceContainerName(slug, service string, seq int64) string {
	return fmt.Sprintf("bp-%s_%s-%d", slug, service, seq)
}
func ServiceImageName(slug, service string, seq int64) string {
	return fmt.Sprintf("bitploy/%s_%s:%d", slug, service, seq)
}
func ServiceVolumeName(slug, volume string) string { return "bp-" + slug + "_" + volume }

// Labels set on every container, network, image and volume bitploy owns.
const (
	LabelManaged    = "nl.bitploy.managed"  // "1"
	LabelInstance   = "nl.bitploy.instance" // id of the installation that created it
	LabelAppID      = "nl.bitploy.app_id"
	LabelSlug       = "nl.bitploy.slug"
	LabelDeployment = "nl.bitploy.deployment_id"
	LabelSeq        = "nl.bitploy.seq"
	LabelService    = "nl.bitploy.service" // the app service a container runs; missing on containers from before services
	LabelProject    = "nl.bitploy.project" // on project networks (bp-p-<id>): the project id
)
