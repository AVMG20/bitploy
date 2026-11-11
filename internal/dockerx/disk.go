package dockerx

// The disk page and the disk cleanup: everything Docker stores, with sizes,
// and removals that refuse what is in use. Deciding what to remove is
// internal/diskuse's job; these calls stay thin.

import (
	"context"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// InvImage is one image as `docker system df -v` shows it.
type InvImage struct {
	ID         string
	Tags       []string // repo:tag; empty for a dangling image
	Digests    []string // repo@sha256:...
	Size       int64
	SharedSize int64 // part of Size in layers other images use too; -1 when not computed
	Containers int64 // containers (running or not) made from it; -1 when not computed
	Created    time.Time
	Labels     map[string]string
}

// InvContainer is one container, running or not.
type InvContainer struct {
	ID      string
	Name    string
	State   string // running | exited | created | dead | paused | restarting | removing
	ImageID string
	SizeRw  int64 // bytes the container wrote on top of its image
	Created time.Time
	Labels  map[string]string
}

// InvVolume is one volume.
type InvVolume struct {
	Name     string
	Size     int64 // -1 when not computed
	RefCount int64 // containers that mount it; -1 when not computed
	Labels   map[string]string
}

// Inventory is everything Docker stores on the server.
type Inventory struct {
	Images          []InvImage
	Containers      []InvContainer
	Volumes         []InvVolume // only with volumes=true
	ImagesBytes     int64       // all images, shared layers counted once
	BuildCacheBytes int64       // the daemon's own build cache (not the bitploy builder's)
}

// Inventory lists every image and container with its size, and with
// volumes=true every volume too. Measuring volumes walks their files, so
// that part can take long on a big server.
func (c *Client) Inventory(ctx context.Context, volumes bool) (Inventory, error) {
	res, err := c.API.DiskUsage(ctx, client.DiskUsageOptions{
		Containers: true, Images: true, BuildCache: true, Volumes: volumes, Verbose: true,
	})
	if err != nil {
		return Inventory{}, err
	}
	out := Inventory{BuildCacheBytes: res.BuildCache.TotalSize, ImagesBytes: res.Images.TotalSize}
	for _, im := range res.Images.Items {
		var tags []string
		for _, t := range im.RepoTags {
			if t != "<none>:<none>" && t != "" {
				tags = append(tags, t)
			}
		}
		var digests []string
		for _, d := range im.RepoDigests {
			if d != "<none>@<none>" && d != "" {
				digests = append(digests, d)
			}
		}
		out.Images = append(out.Images, InvImage{
			ID: im.ID, Tags: tags, Digests: digests, Size: im.Size, SharedSize: im.SharedSize,
			Containers: im.Containers, Created: time.Unix(im.Created, 0).UTC(), Labels: im.Labels,
		})
	}
	for _, ct := range res.Containers.Items {
		out.Containers = append(out.Containers, InvContainer{
			ID: ct.ID, Name: SummaryName(ct), State: string(ct.State), ImageID: ct.ImageID,
			SizeRw: ct.SizeRw, Created: time.Unix(ct.Created, 0).UTC(), Labels: ct.Labels,
		})
	}
	for _, v := range res.Volumes.Items {
		iv := InvVolume{Name: v.Name, Size: -1, RefCount: -1, Labels: v.Labels}
		if v.UsageData != nil {
			iv.Size, iv.RefCount = v.UsageData.Size, v.UsageData.RefCount
		}
		out.Volumes = append(out.Volumes, iv)
	}
	return out, nil
}

// RemoveStoppedContainer removes a container that does not run. Docker
// refuses a running one (no force), and the container's anonymous volumes
// stay.
func (c *Client) RemoveStoppedContainer(ctx context.Context, id string) error {
	_, err := c.API.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveUnusedVolume removes a volume no container mounts; Docker refuses
// one that is in use.
func (c *Client) RemoveUnusedVolume(ctx context.Context, name string) error {
	_, err := c.API.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// VolumeLabels returns a volume's labels, or an error satisfying IsNotFound.
func (c *Client) VolumeLabels(ctx context.Context, name string) (map[string]string, error) {
	res, err := c.API.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return nil, err
	}
	return res.Volume.Labels, nil
}

// IsInUse reports whether err is Docker refusing to remove something a
// container still uses.
func IsInUse(err error) bool {
	if err == nil {
		return false
	}
	if IsConflict(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "in use") || strings.Contains(msg, "is being used")
}
