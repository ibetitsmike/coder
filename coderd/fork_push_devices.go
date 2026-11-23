package coderd

// This file is fork code: it serves the APNs device routes of the
// fork-apns-push experiment. registerForkChatRoutes mounts these handlers.

import (
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

// forkAPNsMaxTokenLength bounds a device token in hex. Apple's tokens are
// 32 bytes today and documented as variable length.
const forkAPNsMaxTokenLength = 200

// @Summary Register an APNs device for chat pushes
// @ID register-an-apns-device-for-chat-pushes
// @Security CoderSessionToken
// @Accept json
// @Tags Chats
// @Param request body codersdk.RegisterChatAPNsDeviceRequest true "Request body"
// @Success 204
// @Router /api/v2/chats/fork/push/apns [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkChatAPNsDevice(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.forkAPNsConfigured(rw, r) {
		return
	}
	var req codersdk.RegisterChatAPNsDeviceRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	token, ok := canonicalForkAPNsToken(rw, r, req.DeviceToken)
	if !ok {
		return
	}
	switch req.Environment {
	case codersdk.ChatAPNsEnvironmentSandbox, codersdk.ChatAPNsEnvironmentProduction:
	default:
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Invalid environment.",
			Detail:  `environment must be "sandbox" or "production".`,
		})
		return
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	err = fdb.UpsertChatAPNsDevice(ctx, forkdb.UpsertChatAPNsDeviceParams{
		DeviceToken:   token,
		UserID:        httpmw.APIKey(r).UserID,
		Environment:   string(req.Environment),
		FinishedTurns: req.FinishedTurns,
		Questions:     req.Questions,
		AgentErrors:   req.AgentErrors,
		Sound:         req.Sound,
		Now:           dbtime.Now(),
	})
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// @Summary Unregister an APNs device
// @ID unregister-an-apns-device
// @Security CoderSessionToken
// @Accept json
// @Tags Chats
// @Param request body codersdk.ChatAPNsDeviceRequest true "Request body"
// @Success 204
// @Router /api/v2/chats/fork/push/apns [delete]
// @x-apidocgen {"skip": true}
func (api *API) deleteForkChatAPNsDevice(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.forkAPNsConfigured(rw, r) {
		return
	}
	var req codersdk.ChatAPNsDeviceRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	token, ok := canonicalForkAPNsToken(rw, r, req.DeviceToken)
	if !ok {
		return
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	err = fdb.DeleteChatAPNsDevice(ctx, forkdb.DeleteChatAPNsDeviceParams{
		UserID:      httpmw.APIKey(r).UserID,
		DeviceToken: token,
	})
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// @Summary Send a test push to an APNs device
// @ID send-a-test-push-to-an-apns-device
// @Security CoderSessionToken
// @Accept json
// @Tags Chats
// @Param request body codersdk.ChatAPNsDeviceRequest true "Request body"
// @Success 204
// @Router /api/v2/chats/fork/push/apns/test [post]
// @x-apidocgen {"skip": true}
func (api *API) postForkChatAPNsDeviceTest(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.forkAPNsConfigured(rw, r) {
		return
	}
	var req codersdk.ChatAPNsDeviceRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	token, ok := canonicalForkAPNsToken(rw, r, req.DeviceToken)
	if !ok {
		return
	}
	userID := httpmw.APIKey(r).UserID
	// The push below runs as the notifier, so the caller must be allowed to
	// send themselves a notification, like upstream's web push test.
	if !api.Authorize(r, policy.ActionCreate, rbac.ResourceNotificationMessage.WithOwner(userID.String())) {
		httpapi.Forbidden(rw)
		return
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	device, err := fdb.GetChatAPNsDevice(ctx, forkdb.GetChatAPNsDeviceParams{
		UserID:      userID,
		DeviceToken: token,
	})
	if xerrors.Is(err, sql.ErrNoRows) {
		httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
			Message: "The device is not registered.",
		})
		return
	}
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	// The same words as upstream's web push test. The caller may notify
	// themselves, checked above, and the notifier prunes a rejected token.
	//nolint:gocritic // See above.
	res, err := api.forkPush.sendToDevice(dbauthz.AsNotifier(ctx), fdb, device, forkAPNsPush{
		Title:      "It's working!",
		Body:       "You've subscribed to push notifications.",
		Deployment: api.forkPush.deployment,
		Sound:      device.Sound,
	})
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadGateway, codersdk.Response{
			Message: "Failed to send the test push.",
			Detail:  err.Error(),
		})
		return
	}
	if !res.ok() {
		httpapi.Write(ctx, rw, http.StatusBadGateway, codersdk.Response{
			Message: "APNs rejected the test push.",
			Detail:  res.String(),
		})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// forkAPNsConfigured answers 404 when the deployment has no APNs key.
func (api *API) forkAPNsConfigured(rw http.ResponseWriter, r *http.Request) bool {
	if api.forkPush != nil {
		return true
	}
	httpapi.Write(r.Context(), rw, http.StatusNotFound, codersdk.Response{
		Message: "APNs is not configured.",
	})
	return false
}

// canonicalForkAPNsToken validates a device token and returns it in lower
// case hex, the one form the table stores, so the same device never has two
// rows.
func canonicalForkAPNsToken(rw http.ResponseWriter, r *http.Request, token string) (string, bool) {
	if _, err := hex.DecodeString(token); err == nil && token != "" && len(token) <= forkAPNsMaxTokenLength {
		return strings.ToLower(token), true
	}
	httpapi.Write(r.Context(), rw, http.StatusBadRequest, codersdk.Response{
		Message: "Invalid device token.",
		Detail:  "device_token must be the APNs device token in hex.",
	})
	return "", false
}
