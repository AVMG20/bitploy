package dockerx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/ids"
)

// ErrNoVolume is returned when an app's data volume does not exist (yet).
// The helper containers below must never be the ones to create it: Docker
// would make an unlabelled volume that the app can then never adopt.
var ErrNoVolume = errors.New("the data volume does not exist")

// helperCaps lets the helper root user delete and chown files of any owner.
var helperCaps = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER"}

// ArchiveVolume returns the contents of the volume as a tar stream (paths
// relative to /data, possibly with a leading "./"). A helper container is
// created, never started, only to read the volume; closing the stream removes
// it. The volume is mounted read-only.
func (c *Client) ArchiveVolume(ctx context.Context, volume string) (io.ReadCloser, error) {
	name, err := c.createHelper(ctx, volume, true, []string{"true"})
	if err != nil {
		return nil, err
	}
	res, err := c.API.CopyFromContainer(ctx, name, client.CopyFromContainerOptions{SourcePath: DataMount + "/."})
	if err != nil {
		_ = c.RemoveContainer(context.WithoutCancel(ctx), name)
		return nil, err
	}
	return &helperStream{ReadCloser: res.Content, remove: func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }}, nil
}

type helperStream struct {
	io.ReadCloser
	remove func()
}

func (h *helperStream) Close() error {
	err := h.ReadCloser.Close()
	h.remove()
	return err
}

// ReplaceVolume empties the volume (the /data folder itself stays) and
// unpacks the tar stream into it. The caller has stopped everything that uses
// the volume. With fixOwner the new files get the owner of /data itself, or,
// when /data belongs to root, are made readable and writable for every user:
// an archive made elsewhere carries user ids that mean nothing here.
func (c *Client) ReplaceVolume(ctx context.Context, volume string, archive io.Reader, fixOwner bool) error {
	if err := c.runHelper(ctx, volume, []string{"find", DataMount, "-mindepth", "1", "-delete"}); err != nil {
		return fmt.Errorf("emptying the volume: %w", err)
	}
	name, err := c.createHelper(ctx, volume, false, []string{"true"})
	if err != nil {
		return err
	}
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }()
	if _, err := c.API.CopyToContainer(ctx, name, client.CopyToContainerOptions{DestinationPath: DataMount, Content: archive}); err != nil {
		return fmt.Errorf("unpacking into the volume: %w", err)
	}
	if fixOwner {
		const script = `o="$(stat -c %u:%g /data)"
if [ "$o" = "0:0" ]; then
  find /data -mindepth 1 -type d -exec chmod a+rwx {} +
  find /data -mindepth 1 -type f -exec chmod a+rw {} +
else
  chown -R -h "$o" /data
fi`
		if err := c.runHelper(ctx, volume, []string{"sh", "-c", script}); err != nil {
			return fmt.Errorf("fixing owners: %w", err)
		}
	}
	return nil
}

// PauseContainer freezes a running container; UnpauseContainer resumes it.
func (c *Client) PauseContainer(ctx context.Context, name string) error {
	_, err := c.API.ContainerPause(ctx, name, client.ContainerPauseOptions{})
	return err
}

func (c *Client) UnpauseContainer(ctx context.Context, name string) error {
	_, err := c.API.ContainerUnpause(ctx, name, client.ContainerUnpauseOptions{})
	return err
}

// createHelper creates (does not start) a throwaway busybox container with
// the volume at /data. It is labelled like the volume init container, has no
// network and keeps only the capabilities in helperCaps.
func (c *Client) createHelper(ctx context.Context, volume string, readOnly bool, cmd []string) (string, error) {
	if ok, err := c.VolumeExists(ctx, volume); err != nil {
		return "", err
	} else if !ok {
		return "", ErrNoVolume
	}
	if err := c.ensureInitImage(ctx); err != nil {
		return "", err
	}
	name := "bp-copy-" + ids.Hex(10)
	pids, mem := int64(32), int64(64<<20)
	_, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: VolumeInitImage, User: "0", Cmd: cmd, Labels: c.initLabels(),
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none", CapDrop: []string{"ALL"}, CapAdd: helperCaps,
			SecurityOpt: []string{"no-new-privileges"},
			Resources:   container.Resources{Memory: mem, MemorySwap: mem, PidsLimit: &pids},
			Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: DataMount, ReadOnly: readOnly}},
		},
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// runHelper runs cmd in a helper container with the volume at /data and waits
// for it (at most 30 minutes).
func (c *Client) runHelper(ctx context.Context, volume string, cmd []string) error {
	name, err := c.createHelper(ctx, volume, false, cmd)
	if err != nil {
		return err
	}
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }()
	if err := c.StartContainer(ctx, name); err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	w := c.API.ContainerWait(wctx, name, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-w.Result:
		if res.StatusCode != 0 {
			return fmt.Errorf("helper container exited with status %d", res.StatusCode)
		}
	case err := <-w.Error:
		return err
	}
	return nil
}
