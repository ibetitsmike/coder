package codersdk

// This file is fork code: it holds the request types and client methods of
// the APNs device routes in the fork-apns-push experiment.

import (
	"context"
	"net/http"
)

// ChatAPNsEnvironment is the APNs environment that issued a device token.
type ChatAPNsEnvironment string

const (
	ChatAPNsEnvironmentSandbox    ChatAPNsEnvironment = "sandbox"
	ChatAPNsEnvironmentProduction ChatAPNsEnvironment = "production"
)

// RegisterChatAPNsDeviceRequest registers an iOS device for the chat pushes
// of the caller, or updates its choices.
type RegisterChatAPNsDeviceRequest struct {
	// DeviceToken is the APNs device token in hex.
	DeviceToken string              `json:"device_token"`
	Environment ChatAPNsEnvironment `json:"environment"`
	// FinishedTurns pushes when a root chat finishes a turn.
	FinishedTurns bool `json:"finished_turns"`
	// Questions pushes when a root chat asks a question. A turn that ends
	// with a question looks like a finished turn to the server, so it is
	// pushed when either flag is set.
	Questions bool `json:"questions"`
	// AgentErrors pushes when a root chat's turn fails.
	AgentErrors bool `json:"agent_errors"`
	// Sound plays the app's chime with each push.
	Sound bool `json:"sound"`
}

// ChatAPNsDeviceRequest names one device of the caller.
type ChatAPNsDeviceRequest struct {
	DeviceToken string `json:"device_token"`
}

const forkChatAPNsDevicesPath = "/api/v2/chats/fork/push/apns"

// RegisterChatAPNsDevice registers a device for the caller's chat pushes.
func (c *Client) RegisterChatAPNsDevice(ctx context.Context, req RegisterChatAPNsDeviceRequest) error {
	return requestNoContent(ctx, c, http.MethodPost, forkChatAPNsDevicesPath, req)
}

// DeleteChatAPNsDevice unregisters a device of the caller. It succeeds when
// the device is not registered.
func (c *Client) DeleteChatAPNsDevice(ctx context.Context, req ChatAPNsDeviceRequest) error {
	return requestNoContent(ctx, c, http.MethodDelete, forkChatAPNsDevicesPath, req)
}

// TestChatAPNsDevice sends a test push to a device of the caller.
func (c *Client) TestChatAPNsDevice(ctx context.Context, req ChatAPNsDeviceRequest) error {
	return requestNoContent(ctx, c, http.MethodPost, forkChatAPNsDevicesPath+"/test", req)
}
