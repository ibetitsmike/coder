// This file is fork code: wires the fork-agent-definitions table to the API.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	type AgentDefinitionScope,
	agentDefinitions,
	deleteAgentDefinition,
	isSameScope,
	updateAgentDefinition,
} from "#/api/queries/forkAgentDefinitions";
import type { ForkAgentDefinitionMetadata } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import type { SkillAccess } from "../../utils/skills";
import {
	type ForkAgentDefinitionDeleteState,
	ForkAgentDefinitionsTableView,
} from "./ForkAgentDefinitionsTableView";
import { useForkAgentDefinitionEditor } from "./useForkAgentDefinitionEditor";

const copy = {
	user: {
		description:
			"Custom agent types your chats can spawn with spawn_agent in this organization. Each definition is a Claude Code or Cursor agent markdown file.",
		emptyMessage: "No personal agent definitions in this organization yet.",
	},
	organization: {
		description:
			"Custom agent types that chats in this organization can spawn with spawn_agent. Each definition is a Claude Code or Cursor agent markdown file and is shared with everyone in the organization by default.",
		emptyMessage: "No organization agent definitions yet.",
	},
} satisfies Record<
	AgentDefinitionScope["tier"],
	{ description: string; emptyMessage: string }
>;

type ForkAgentDefinitionsTableProps = {
	scope: AgentDefinitionScope;
	access?: SkillAccess;
	action?: React.ReactNode;
};

export const ForkAgentDefinitionsTable: React.FC<
	ForkAgentDefinitionsTableProps
> = ({ scope, access, action }) => {
	const queryClient = useQueryClient();
	const [selectedDeleteTarget, setDeleteTarget] =
		useState<ForkAgentDefinitionMetadata>();
	const deleteTarget =
		selectedDeleteTarget?.organization_id === scope.organizationId
			? selectedDeleteTarget
			: undefined;
	if (selectedDeleteTarget && !deleteTarget) {
		setDeleteTarget(undefined);
	}
	const definitionsQuery = useQuery(agentDefinitions(scope));
	const { editorState, openCreate, openEdit } =
		useForkAgentDefinitionEditor(scope);

	// Pending toggles are keyed by organization, then name, so a second
	// toggle, or a switch to another organization with the same names, does
	// not hide or borrow a pending value while its request runs. Callbacks
	// read the organization from the variables because TanStack Query runs
	// the latest options when an earlier request settles.
	const [pendingToggles, setPendingToggles] = useState<
		Readonly<Record<string, Readonly<Record<string, boolean>>>>
	>({});
	const updateOptions = updateAgentDefinition(queryClient);
	const toggleMutation = useMutation({
		...updateOptions,
		onMutate: ({ scope: { organizationId: orgId }, name, req }) => {
			setPendingToggles((current) => ({
				...current,
				[orgId]: {
					...current[orgId],
					[name]: Boolean(req.enabled),
				},
			}));
		},
		onError: (error, { name }) => {
			toast.error(getErrorMessage(error, `Failed to update ${name}.`), {
				description: getErrorDetail(error),
			});
		},
		onSettled: async (...args) => {
			await updateOptions.onSettled?.(...args);
			const {
				scope: { organizationId: orgId },
				name,
			} = args[2];
			setPendingToggles((current) => ({
				...current,
				[orgId]: Object.fromEntries(
					Object.entries(current[orgId] ?? {}).filter(([key]) => key !== name),
				),
			}));
		},
	});

	const deleteMutation = useMutation(deleteAgentDefinition(queryClient));

	let deleteState: ForkAgentDefinitionDeleteState | undefined;
	if (deleteTarget) {
		const isTarget =
			deleteMutation.variables !== undefined &&
			isSameScope(deleteMutation.variables.scope, scope) &&
			deleteMutation.variables.name === deleteTarget.name;
		const isDeleting = isTarget && deleteMutation.isPending;
		deleteState = {
			definition: deleteTarget,
			error: isTarget ? deleteMutation.error : undefined,
			isDeleting,
			onConfirm: () =>
				deleteMutation.mutate(
					{ scope, name: deleteTarget.name },
					{
						// A lost response can still have deleted the definition,
						// so the refetched list decides whether the delete is done.
						onSettled: (_data, error, { scope: requestScope, name }) => {
							const definitions = queryClient.getQueryData(
								agentDefinitions(requestScope).queryKey,
							);
							const isGone =
								definitions !== undefined &&
								!definitions.some((definition) => definition.name === name);
							if (!error || isGone) {
								setDeleteTarget(undefined);
								toast.success("Agent definition deleted.");
							}
						},
					},
				),
			// The dialog shows the delete error, so it stays open until the
			// request settles.
			onClose: () => {
				if (!isDeleting) {
					setDeleteTarget(undefined);
				}
			},
		};
	}

	return (
		<ForkAgentDefinitionsTableView
			definitions={definitionsQuery.data ?? []}
			{...copy[scope.tier]}
			access={access}
			action={action}
			error={definitionsQuery.error}
			isLoading={definitionsQuery.isLoading}
			isRetrying={definitionsQuery.isFetching}
			onRetry={() => {
				void definitionsQuery.refetch();
			}}
			pendingToggles={pendingToggles[scope.organizationId]}
			onToggleEnabled={(name, enabled) =>
				toggleMutation.mutate({ scope, name, req: { enabled } })
			}
			onCreate={openCreate}
			onEdit={openEdit}
			editorState={editorState}
			onDelete={(definition) => {
				deleteMutation.reset();
				setDeleteTarget(definition);
			}}
			deleteState={deleteState}
		/>
	);
};
