package coderd

// This file is fork code: it exposes the APNs dispatcher of the
// fork-apns-push experiment to the external tests.

import (
	"net/http"
	"time"

	"github.com/coder/coder/v2/coderd/webpush"
	"github.com/coder/coder/v2/codersdk"
)

// ForkPushDispatcher returns the dispatcher that api gave chatd in place of
// upstream's web push dispatcher, or nil when it gave upstream's own.
func ForkPushDispatcher(api *API) webpush.Dispatcher {
	if api.forkPush == nil {
		return nil
	}
	return api.forkPush
}

// PointForkAPNs sends the APNs pushes of api to baseURL, sandbox under
// /sandbox and production under /production, with client and a send
// timeout.
func PointForkAPNs(api *API, baseURL string, client *http.Client, timeout time.Duration) {
	api.forkPush.apns.client = client
	api.forkPush.apns.baseURLs = map[codersdk.ChatAPNsEnvironment]string{
		codersdk.ChatAPNsEnvironmentSandbox:    baseURL + "/sandbox",
		codersdk.ChatAPNsEnvironmentProduction: baseURL + "/production",
	}
	api.forkPush.timeout = timeout
}

// WaitForkPush waits for the APNs sends that api started.
func WaitForkPush(api *API) {
	api.forkPush.wg.Wait()
}
