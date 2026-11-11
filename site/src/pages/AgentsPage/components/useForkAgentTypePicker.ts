// This file is fork code: the fork-agent-definitions experiment's agent type
// choice for a new chat, kept here so AgentCreateForm only calls the hook and
// renders ForkCompactAgentTypeSelector with its props.

import { useState } from "react";
import { useQuery } from "react-query";
import { agentTypes } from "#/api/queries/forkAgentDefinitions";
import type { ForkCompactAgentTypeSelector } from "./ChatElements/ForkCompactAgentTypeSelector";
import { useForkExperiment } from "./ChatElements/tools/forkToolUtils";

type ForkAgentTypePickerOptions = {
	organizationId: string;
	projectId?: string;
	workspaceId?: string;
	modelConfigId?: string;
};

/**
 * Returns whether the experiment shows the agent type picker, the agent type
 * a new chat starts as (undefined for the default agent), and the picker's
 * props apart from `disabled`.
 */
export const useForkAgentTypePicker = ({
	organizationId,
	projectId,
	workspaceId,
	modelConfigId,
}: ForkAgentTypePickerOptions) => {
	const isEnabled =
		useForkExperiment("fork-agent-definitions") && organizationId !== "";
	const [picked, setPicked] = useState<string | null>(null);
	const query = useQuery({
		...agentTypes(organizationId, {
			project: projectId,
			workspace: workspaceId,
			model_config_id: modelConfigId,
		}),
		enabled: isEnabled,
	});
	// A loaded list is authoritative: a type it drops or can't start a chat
	// as falls back to the default agent instead of failing the send.
	const isPickedAvailable =
		query.data === undefined ||
		query.data.some((type) => type.name === picked && type.root_available);
	const agentType =
		isEnabled && picked !== null && isPickedAvailable ? picked : undefined;

	const selectorProps: Omit<
		React.ComponentProps<typeof ForkCompactAgentTypeSelector>,
		"disabled"
	> = {
		value: agentType ?? null,
		agentTypes: query.data ?? [],
		onChange: setPicked,
		isLoading: query.isLoading,
		error: query.data === undefined ? query.error : undefined,
		onRetry: () => void query.refetch(),
	};
	return { isEnabled, agentType, selectorProps };
};
