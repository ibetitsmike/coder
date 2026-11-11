// Command echo is a test plugin that reflects each webhook request back to
// the test as JSON, so tests can assert exactly what the plugin received.
package main

import (
	"encoding/json"
	"net/http"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

func init() {
	pluginsdk.Register(pluginsdk.Plugin{
		Manifest: pluginsdk.Manifest{
			Name:    "echo",
			Version: "1.0.0",
			Webhook: true,
		},
		OnHTTP: onHTTP,
	})
}

func main() {}

func onHTTP(_ pluginsdk.Host, req pluginsdk.HTTPRequest) pluginsdk.HTTPResponse {
	body, _ := json.Marshal(map[string]any{
		"method":  req.Method,
		"url":     req.URL,
		"headers": req.Header,
		"body":    string(req.Body),
	})
	return pluginsdk.HTTPResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       body,
	}
}
