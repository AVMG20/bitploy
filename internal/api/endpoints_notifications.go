package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
)

// NotificationChannelRef is exported: huma reads the fields of exported
// embedded structs only.
type NotificationChannelRef struct {
	ID string `path:"id" doc:"The channel's id (nch_...)"`
}

// notificationRoutes registers the notification channels of the signed-in
// user (docs/decisions.md, "Notifications"). Session only: a channel holds
// a secret (a webhook URL) and reaches people.
func (a *API) notificationRoutes() {
	reg(a, sessionOnly, huma.Operation{
		OperationID: "getNotificationOptions", Method: http.MethodGet, Path: "/notifications/options", Tags: []string{"Notifications"},
		Summary: "List the notification events and channel types",
		Description: "Session only. Emergency events mention @everyone on Discord (the others @here) and stay on screen in a browser until clicked. " +
			"vapid_public_key is the applicationServerKey for a browser's pushManager.subscribe.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.NotificationOptions], error) {
		return okOrNil(a.svc.NotificationOptions(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "listNotificationChannels", Method: http.MethodGet, Path: "/notification-channels", Tags: []string{"Notifications"},
		Summary:     "List your notification channels",
		Description: "Session only, oldest first. Secrets in config are masked.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.NotificationChannel]], error) {
		items, err := a.svc.ListNotificationChannels(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "createNotificationChannel", Method: http.MethodPost, Path: "/notification-channels", Tags: []string{"Notifications"},
		Summary: "Add a notification channel",
		Description: "Session only. You get the events of every app you can use: your own and those of the projects you are a member of. " +
			"type browser takes the PushSubscription as config; adding a browser that is already a channel of yours updates that channel, " +
			"and removes it from another user who had it. " +
			"400 notification_type_invalid, webhook_url_required, webhook_url_invalid, push_subscription_invalid, channel_name_invalid, notification_event_invalid; " +
			"403 notification_channels_limit (20).",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct {
		Body app.CreateNotificationChannelInput
	}) (*Out[app.NotificationChannel], error) {
		return okOrNil(a.svc.CreateNotificationChannel(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "updateNotificationChannel", Method: http.MethodPatch, Path: "/notification-channels/{id}", Tags: []string{"Notifications"},
		Summary:     "Change a notification channel",
		Description: "Session only. Fields left out keep their value; config without webhook_url keeps the stored one. 400 channel_config_fixed (a browser channel's subscription cannot be changed); 404 notification_channel_not_found.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		NotificationChannelRef
		Body app.UpdateNotificationChannelInput
	}) (*Out[app.NotificationChannel], error) {
		return okOrNil(a.svc.UpdateNotificationChannel(ctx, act, in.ID, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "deleteNotificationChannel", Method: http.MethodDelete, Path: "/notification-channels/{id}", Tags: []string{"Notifications"},
		Summary: "Delete a notification channel", Description: "Session only. 404 notification_channel_not_found.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct{ NotificationChannelRef }) (*Empty, error) {
		if err := a.svc.DeleteNotificationChannel(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "testNotificationChannel", Method: http.MethodPost, Path: "/notification-channels/{id}/test", Tags: []string{"Notifications"},
		Summary: "Send a test message",
		Description: "Session only. Sends now and waits for the answer; returns the channel with its last_success_at. " +
			"422 notification_test_failed with the reason (like Discord's answer), also stored as last_error.",
	}, func(ctx context.Context, act app.Actor, in *struct{ NotificationChannelRef }) (*Out[app.NotificationChannel], error) {
		return okOrNil(a.svc.TestNotificationChannel(ctx, act, in.ID))
	})
}
