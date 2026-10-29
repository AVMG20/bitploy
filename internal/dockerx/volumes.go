package dockerx

import (
	"context"

	"github.com/moby/moby/client"
)

// EnsureVolume creates a named volume (idempotent).
func (c *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) error {
	_, err := c.API.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: labels})
	return err
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
	res, err := c.API.VolumeList(ctx, client.VolumeListOptions{Filters: client.Filters{}.Add("label", LabelManaged+"=1")})
	if err != nil {
		return nil, err
	}
	out := make([]VolumeRef, 0, len(res.Items))
	for _, v := range res.Items {
		out = append(out, VolumeRef{Name: v.Name, Labels: v.Labels})
	}
	return out, nil
}

type VolumeRef struct {
	Name   string
	Labels map[string]string
}
