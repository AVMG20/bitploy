package dockerx

// Docker events for bitploy containers. Owner: ops agent.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// Event is a container event reduced to what bitploy reacts to.
type Event struct {
	Action   string // start | die | oom | destroy | health_status
	Health   string // for health_status: healthy | unhealthy | starting
	ID       string
	Name     string
	AppID    string
	Instance string // nl.bitploy.instance of the container
	Slug     string
	ExitCode string // for die
	Time     time.Time
}

// ParseEvent converts a raw message. ok is false for events that are not
// about a bitploy container.
func ParseEvent(m events.Message) (Event, bool) {
	if m.Type != events.ContainerEventType {
		return Event{}, false
	}
	attrs := m.Actor.Attributes
	if attrs[LabelManaged] != "1" || attrs[LabelAppID] == "" {
		return Event{}, false
	}
	action := string(m.Action)
	ev := Event{
		ID: m.Actor.ID, Name: strings.TrimPrefix(attrs["name"], "/"),
		AppID: attrs[LabelAppID], Instance: attrs[LabelInstance], Slug: attrs[LabelSlug], ExitCode: attrs["exitCode"],
		Time: time.Unix(0, m.TimeNano),
	}
	if m.TimeNano == 0 {
		ev.Time = time.Unix(m.Time, 0)
	}
	if rest, ok := strings.CutPrefix(action, "health_status"); ok {
		ev.Action = "health_status"
		ev.Health = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
		return ev, true
	}
	switch action {
	case "start", "die", "oom", "destroy":
		ev.Action = action
		return ev, true
	}
	return Event{}, false
}

// WatchEvents streams events of bitploy containers to fn until ctx ends or
// the stream breaks (the error says why). Pass the time of the last event
// seen as since to resume without gaps; the zero time starts from now.
func (c *Client) WatchEvents(ctx context.Context, since time.Time, fn func(Event)) error {
	f, err := c.ownedFilter()
	if err != nil {
		return err
	}
	opts := client.EventsListOptions{Filters: f.Add("type", "container")}
	if !since.IsZero() {
		opts.Since = fmt.Sprintf("%d.%09d", since.Unix(), since.Nanosecond())
	}
	res := c.API.Events(ctx, opts)
	for {
		select {
		case m, ok := <-res.Messages:
			if !ok {
				return <-res.Err
			}
			if ev, ok := ParseEvent(m); ok && ev.Instance == c.Instance {
				fn(ev)
			}
		case err := <-res.Err:
			if err == nil {
				return nil
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
