package coderd

// This file is fork code: it is the only place fork features add chat routes.
// Upstream's chat_routes.go calls registerForkChatRoutes once, so fork features
// never edit that file and an upstream sync of it stays clean.

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/coder/coder/v2/coderd/httpapi"
)

// registerForkChatRoutes mounts the fork-only chat routes on /chats/fork of r,
// the /api/v2 root router. A fork route group adds
// httpmw.RequireExperiment(api.Experiments, "fork-<feature>") as it lands, so
// every fork path stays closed until its experiment is enabled.
//
// The mount reserves /chats/fork even with no fork route registered: the
// subrouter answers 404 instead of letting a fork path fall into the /chats
// wildcard, where UUID parsing of {chat} turns it into a 400.
//
// The API key middleware wraps the mount instead of being Use'd inside the
// subrouter: chi builds a subrouter's middleware chain only when its first
// route is registered, so with no fork route the 404 would skip authentication.
func (*API) registerForkChatRoutes(r chi.Router, apiKeyMiddleware func(http.Handler) http.Handler) {
	r.With(apiKeyMiddleware).Route("/chats/fork", func(r chi.Router) {
		r.NotFound(func(rw http.ResponseWriter, _ *http.Request) {
			httpapi.RouteNotFound(rw)
		})
	})
}
