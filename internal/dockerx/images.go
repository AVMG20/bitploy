package dockerx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// BuiltImage is what the pipeline needs to know about a built image.
type BuiltImage struct {
	ID          string
	SizeBytes   int64
	Healthcheck *container.HealthConfig // nil when the image defines none (or NONE)
	ExposedTCP  []int                   // TCP ports the image config declares (EXPOSE), sorted
	// Fingerprint sums up the image's layers and its config without labels.
	// Two builds of the same source get different IDs (bitploy labels each
	// image with its deployment) but the same fingerprint.
	Fingerprint string
}

// InspectImage returns size and HEALTHCHECK config, or an error satisfying IsNotFound.
func (c *Client) InspectImage(ctx context.Context, ref string) (BuiltImage, error) {
	res, err := c.API.ImageInspect(ctx, ref)
	if err != nil {
		return BuiltImage{}, err
	}
	info := BuiltImage{ID: res.ID, SizeBytes: res.Size}
	{
		var cfg any
		if res.Config != nil {
			c := *res.Config
			c.Labels = nil
			cfg = c
		}
		b, _ := json.Marshal(struct {
			Layers []string
			Config any
		}{res.RootFS.Layers, cfg})
		sum := sha256.Sum256(b)
		info.Fingerprint = hex.EncodeToString(sum[:])
	}
	if res.Config != nil {
		for raw := range res.Config.ExposedPorts { // "8080/tcp"
			if p, err := network.ParsePort(raw); err == nil && p.Proto() == network.TCP {
				info.ExposedTCP = append(info.ExposedTCP, int(p.Num()))
			}
		}
		sort.Ints(info.ExposedTCP)
	}
	if res.Config != nil && res.Config.Healthcheck != nil {
		hc := res.Config.Healthcheck
		if len(hc.Test) > 0 && hc.Test[0] != "NONE" {
			info.Healthcheck = hc
		}
	}
	return info, nil
}

// RemoveImage removes an image tag (not forced: images in use stay). Missing is not an error.
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	_, err := c.API.ImageRemove(ctx, ref, client.ImageRemoveOptions{PruneChildren: true})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// TagImage adds a tag to an image.
func (c *Client) TagImage(ctx context.Context, source, target string) error {
	_, err := c.API.ImageTag(ctx, client.ImageTagOptions{Source: source, Target: target})
	return err
}

// ListAppImages returns the tags of every image built for an app (label match).
func (c *Client) ListAppImages(ctx context.Context, appID string) ([]string, error) {
	f, err := c.appFilter(appID)
	if err != nil {
		return nil, err
	}
	res, err := c.API.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, im := range res.Items {
		if !c.ownedOrLegacy(im.Labels) {
			continue
		}
		tags = append(tags, im.RepoTags...)
	}
	return tags, nil
}

// HealthWindow is how long a HEALTHCHECK may take to turn healthy, derived
// from the image's start period, interval and retries (min 60 s, max 300 s).
func HealthWindow(hc *container.HealthConfig) time.Duration {
	d := 60 * time.Second
	if hc == nil {
		return d
	}
	interval := hc.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	retries := hc.Retries
	if retries <= 0 {
		retries = 3
	}
	want := hc.StartPeriod + interval*time.Duration(retries) + 5*time.Second
	if want > d {
		d = want
	}
	if d > 300*time.Second {
		d = 300 * time.Second
	}
	return d
}

// ManagedImage is a bitploy-built image with its owning app.
type ManagedImage struct {
	AppID string
	Tags  []string
}

// ListManagedImages lists images carrying the managed label.
func (c *Client) ListManagedImages(ctx context.Context) ([]ManagedImage, error) {
	f, err := c.ownedFilter()
	if err != nil {
		return nil, err
	}
	res, err := c.API.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	out := make([]ManagedImage, 0, len(res.Items))
	for _, im := range res.Items {
		if !c.owned(im.Labels) {
			continue
		}
		out = append(out, ManagedImage{AppID: im.Labels[LabelAppID], Tags: im.RepoTags})
	}
	return out, nil
}
