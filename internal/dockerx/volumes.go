package dockerx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/moby/moby/client"
)

// ErrVolumeOwnedByOther is returned by EnsureVolume when a volume with that
// name exists but is labelled for a different app (or carries no owner label).
var ErrVolumeOwnedByOther = errors.New("a data volume with this name already exists and belongs to a different app")

// EnsureVolume creates a named volume (idempotent) and reports whether it was
// created now. An existing volume is only adopted when its nl.bitploy.app_id
// label equals labels[LabelAppID]; otherwise it returns ErrVolumeOwnedByOther
// and touches nothing.
func (c *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) (created bool, err error) {
	want := labels[LabelAppID]
	if want != "" {
		res, err := c.API.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
		switch {
		case err == nil:
			if res.Volume.Labels[LabelAppID] != want {
				return false, ErrVolumeOwnedByOther
			}
			return false, nil
		case !IsNotFound(err):
			return false, err
		}
	}
	if _, err := c.API.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: labels}); err != nil {
		return false, err
	}
	return true, nil
}

// VolumeInitImage runs the one-off container that prepares a new volume.
const VolumeInitImage = "busybox:1.37"

// PrepareVolume makes a new, empty volume writable for any user: Docker creates
// it root:root 755, but apps run as a non-root USER and would get "permission
// denied" on /data. A throwaway container (labelled managed, no network, all
// capabilities dropped, removed afterwards) sets mode 1777 (like /tmp: anyone
// can create files, nobody can delete another user's). Only this app's
// container ever mounts the volume. If the image has its own /data directory,
// Docker still copies that directory's owner onto the empty volume when the app
// starts, which wins over this mode.
func (c *Client) PrepareVolume(ctx context.Context, volume string) error {
	if err := c.ensureInitImage(ctx); err != nil {
		return err
	}
	name := "bp-init-" + volume
	_ = c.RemoveContainer(ctx, name)
	pids, mem := int64(16), int64(32<<20)
	_, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: VolumeInitImage, User: "0", Cmd: []string{"chmod", "1777", DataMount},
			Labels: c.initLabels(),
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none", ReadonlyRootfs: true, CapDrop: []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
			Resources:   container.Resources{Memory: mem, MemorySwap: mem, PidsLimit: &pids},
			Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: DataMount}},
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }()
	if err := c.StartContainer(ctx, name); err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	w := c.API.ContainerWait(wctx, name, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-w.Result:
		if res.StatusCode != 0 {
			return fmt.Errorf("volume init container exited with status %d", res.StatusCode)
		}
	case err := <-w.Error:
		return err
	}
	return nil
}

// ensureInitImage pulls the helper image when it is not on the server yet.
func (c *Client) ensureInitImage(ctx context.Context) error {
	if _, err := c.API.ImageInspect(ctx, VolumeInitImage); err != nil {
		if !IsNotFound(err) {
			return err
		}
		pull, err := c.API.ImagePull(ctx, VolumeInitImage, client.ImagePullOptions{})
		if err != nil {
			return err
		}
		return pull.Wait(ctx)
	}
	return nil
}

// RemoveVolume removes a named volume; a missing one is not an error.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	_, err := c.API.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// VolumeExists reports whether the named volume exists.
func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := c.API.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// ListManagedVolumes lists volumes with the managed label.
func (c *Client) ListManagedVolumes(ctx context.Context) ([]VolumeRef, error) {
	f, err := c.ownedFilter()
	if err != nil {
		return nil, err
	}
	res, err := c.API.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	out := make([]VolumeRef, 0, len(res.Items))
	for _, v := range res.Items {
		if !c.owned(v.Labels) {
			continue
		}
		out = append(out, VolumeRef{Name: v.Name, Labels: v.Labels})
	}
	return out, nil
}

type VolumeRef struct {
	Name   string
	Labels map[string]string
}

func (c *Client) initLabels() map[string]string {
	l := map[string]string{LabelManaged: "1", "nl.bitploy.init": "1"}
	if c.Instance != "" {
		l[LabelInstance] = c.Instance
	}
	return l
}
