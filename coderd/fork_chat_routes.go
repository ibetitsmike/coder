package coderd

// This file is fork code: it is the only place fork features add chat routes.
// Upstream's chat_routes.go calls registerForkChatRoutes once, so fork features
// never edit that file and an upstream sync of it stays clean.

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/codersdk"
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
//
// Register fork leaves with r.With(...) instead of Use'ing middleware in a
// subrouter: a Use applies to the reserved 404 too, so a fork path that no
// route matches would answer the middleware's 403 or 400.
func (api *API) registerForkChatRoutes(r chi.Router, apiKeyMiddleware func(http.Handler) http.Handler) {
	r.With(apiKeyMiddleware).Route("/chats/fork", func(r chi.Router) {
		// The fork routes take their middleware inline rather than from a
		// {chat} subrouter: a subrouter is mounted as a prefix, so it would
		// also run for fork paths that match no route, and the reserved 404
		// below would become an experiment or chat parameter error.
		chatTodos := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkChatTodos),
			httpmw.ExtractChatParam(api.Database),
		)
		chatTodos.Get("/{chat}/todos", api.getChatTodos)
		chatTodos.Get("/{chat}/todos/watch", api.watchChatTodos)
		r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkNestedSubagents),
			httpmw.ExtractChatParam(api.Database),
		).Get("/{chat}/family", api.getForkChatFamilyCapacity)

		chatQuestions := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkNestedSubagents),
			httpmw.ExtractChatParam(api.Database),
		)
		chatQuestions.Get("/{chat}/questions", api.getChatDescendantQuestions)
		chatQuestions.Post("/{chat}/questions/secret-answer", api.postChatSecretAnswer)

		chatWakeups := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkChatWakeups),
			httpmw.ExtractChatParam(api.Database),
		)
		chatWakeups.Get("/{chat}/wakeups", api.getChatWakeups)
		chatWakeups.Patch("/{chat}/wakeups/{wakeup}", api.patchChatWakeup)

		r.NotFound(func(rw http.ResponseWriter, _ *http.Request) {
			httpapi.RouteNotFound(rw)
		})
		orgSubagents := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkNestedSubagents),
			httpmw.ExtractOrganizationParam(api.Database),
		)
		orgSubagents.Get("/organizations/{organization}/subagent-limits", api.forkOrganizationSubagentLimits)
		orgSubagents.Put("/organizations/{organization}/subagent-limits", api.updateForkOrganizationSubagentLimits)

		orgAgentDefinitions := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkAgentDefinitions),
			httpmw.ExtractOrganizationParam(api.Database),
		)
		const orgAgentDefinitionsPath = "/organizations/{organization}/agent-definitions"
		orgAgentDefinitions.Get(orgAgentDefinitionsPath, api.forkOrganizationAgentDefinitions)
		orgAgentDefinitions.Post(orgAgentDefinitionsPath, api.postForkOrganizationAgentDefinition)
		orgAgentDefinitions.Get(orgAgentDefinitionsPath+"/{agentDefinition}", api.forkOrganizationAgentDefinition)
		orgAgentDefinitions.Patch(orgAgentDefinitionsPath+"/{agentDefinition}", api.patchForkOrganizationAgentDefinition)
		orgAgentDefinitions.Delete(orgAgentDefinitionsPath+"/{agentDefinition}", api.deleteForkOrganizationAgentDefinition)
		orgAgentDefinitions.Get(orgAgentDefinitionsPath+"/{agentDefinition}/acl", api.forkOrganizationAgentDefinitionACL)
		orgAgentDefinitions.Get(orgAgentDefinitionsPath+"/{agentDefinition}/acl/available", api.forkOrganizationAgentDefinitionACLAvailable)
		orgAgentDefinitions.Patch(orgAgentDefinitionsPath+"/{agentDefinition}/acl", api.patchForkOrganizationAgentDefinitionACL)
		orgAgentDefinitions.Get("/organizations/{organization}/agent-types", api.forkAgentTypes)

		orgPluginPacks := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkPluginPacks),
			httpmw.ExtractOrganizationParam(api.Database),
		)
		orgPluginPacks.Get("/organizations/{organization}/plugin-pack-sources", api.forkPluginPackSources)
		orgPluginPacks.Put("/organizations/{organization}/plugin-pack-sources", api.updateForkPluginPackSources)
		const orgPluginPacksPath = "/organizations/{organization}/plugin-packs"
		orgPluginPacks.Get(orgPluginPacksPath, api.forkPluginPacks)
		orgPluginPacks.Post(orgPluginPacksPath, api.postForkPluginPack)
		orgPluginPacks.Get(orgPluginPacksPath+"/{pluginPack}", api.forkPluginPack)
		orgPluginPacks.Delete(orgPluginPacksPath+"/{pluginPack}", api.deleteForkPluginPack)
		orgPluginPacks.Post(orgPluginPacksPath+"/{pluginPack}/update", api.updateForkPluginPack)

		projectAgentDefinitions := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkAgentDefinitions),
			httpmw.ExtractOrganizationParam(api.Database),
			httpmw.ExtractChatProjectParam(api.Database),
		)
		const projectAgentDefinitionsPath = "/organizations/{organization}/projects/{project}/agent-definitions"
		projectAgentDefinitions.Get(projectAgentDefinitionsPath, api.forkProjectAgentDefinitions)
		projectAgentDefinitions.Post(projectAgentDefinitionsPath, api.postForkProjectAgentDefinition)
		projectAgentDefinitions.Get(projectAgentDefinitionsPath+"/{agentDefinition}", api.forkProjectAgentDefinition)
		projectAgentDefinitions.Patch(projectAgentDefinitionsPath+"/{agentDefinition}", api.patchForkProjectAgentDefinition)
		projectAgentDefinitions.Delete(projectAgentDefinitionsPath+"/{agentDefinition}", api.deleteForkProjectAgentDefinition)

		userAgentDefinitions := r.With(
			httpmw.RequireExperiment(api.Experiments, codersdk.ExperimentForkAgentDefinitions),
			httpmw.ExtractOrganizationParam(api.Database),
			httpmw.ExtractOrganizationMemberParam(api.Database),
		)
		const userAgentDefinitionsPath = "/organizations/{organization}/members/{user}/agent-definitions"
		userAgentDefinitions.Get(userAgentDefinitionsPath, api.forkUserAgentDefinitions)
		userAgentDefinitions.Post(userAgentDefinitionsPath, api.postForkUserAgentDefinition)
		userAgentDefinitions.Get(userAgentDefinitionsPath+"/{agentDefinition}", api.forkUserAgentDefinition)
		userAgentDefinitions.Patch(userAgentDefinitionsPath+"/{agentDefinition}", api.patchForkUserAgentDefinition)
		userAgentDefinitions.Delete(userAgentDefinitionsPath+"/{agentDefinition}", api.deleteForkUserAgentDefinition)
	})
}
