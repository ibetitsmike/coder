// This file is fork code: routes for fork experiments, so router.tsx, an
// upstream file, needs one line per route group.

import { lazy } from "react";
import { Route } from "react-router";

const ForkAgentDefinitionsPage = lazy(
	() =>
		import(
			"./pages/AgentsPage/AgentSettingsPage/ForkAgentDefinitionsPage/ForkAgentDefinitionsPage"
		),
);

const ForkAvailableAgentsPage = lazy(
	() =>
		import(
			"./pages/AgentsPage/AgentSettingsPage/ForkAvailableAgentsPage/ForkAvailableAgentsPage"
		),
);

const ForkOrganizationAgentDefinitionsPage = lazy(
	() =>
		import(
			"./pages/AISettingsPage/ForkAgentDefinitionsPage/ForkOrganizationAgentDefinitionsPage"
		),
);

/** Routes nested under /agents/settings. */
export const forkAgentSettingsRoutes = (
	<>
		<Route path="agent-definitions" element={<ForkAgentDefinitionsPage />} />
		<Route path="available-agents" element={<ForkAvailableAgentsPage />} />
	</>
);

/** Routes nested under /ai/settings. */
export const forkAISettingsRoutes = (
	<Route
		path="agent-definitions"
		element={<ForkOrganizationAgentDefinitionsPage />}
	/>
);
