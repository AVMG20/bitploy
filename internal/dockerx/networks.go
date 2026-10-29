package dockerx

import (
	"context"
	"strings"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// EnsureNetwork creates the app network if it does not exist yet.
func (c *Client) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	_, err := c.API.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Labels: labels})
	if IsConflict(err) || (err != nil && strings.Contains(err.Error(), "already exists")) {
		return nil
	}
	return err
}

// ConnectNetwork attaches a container to a network; "already connected" is ignored.
func (c *Client) ConnectNetwork(ctx context.Context, networkName, containerName string) error {
	_, err := c.API.NetworkConnect(ctx, networkName, client.NetworkConnectOptions{
		Container: containerName, EndpointConfig: &network.EndpointSettings{},
	})
	if err != nil && strings.Contains(err.Error(), "already exists in network") {
		return nil
	}
	return err
}

// DisconnectNetwork detaches a container from a network; missing is ignored.
func (c *Client) DisconnectNetwork(ctx context.Context, networkName, containerName string) error {
	_, err := c.API.NetworkDisconnect(ctx, networkName, client.NetworkDisconnectOptions{Container: containerName, Force: true})
	if IsNotFound(err) || (err != nil && strings.Contains(err.Error(), "is not connected")) {
		return nil
	}
	return err
}

// RemoveNetwork removes a network; a missing one is not an error.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	_, err := c.API.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ListManagedNetworks lists networks with the managed label.
func (c *Client) ListManagedNetworks(ctx context.Context) ([]network.Summary, error) {
	f, err := c.ownedFilter()
	if err != nil {
		return nil, err
	}
	res, err := c.API.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	return keepOwned(c, res.Items, func(n network.Summary) map[string]string { return n.Labels }), nil
}
