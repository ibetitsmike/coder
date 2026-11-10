// This file is fork code: wires the fork-agent-definitions table to the API.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	deleteUserAgentDefinition,
	updateUserAgentDefinition,
	userAgentDefinitions,
} from "#/api/queries/forkAgentDefinitions";
import type { ForkAgentDefinitionMetadata } from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import {
	type ForkAgentDefinitionDeleteState,
	ForkAgentDefinitionsTableView,
} from "./ForkAgentDefinitionsTableView";
import { useForkAgentDefinitionEditor } from "./useForkAgentDefinitionEditor";

type ForkAgentDefinitionsTableProps = {
	organizationId: string;
	action?: React.ReactNode;
};

export const ForkAgentDefinitionsTable: React.FC<
	ForkAgentDefinitionsTableProps
> = ({ organizationId, action }) => {
	const queryClient = useQueryClient();
	const [selectedDeleteTarget, setDeleteTarget] =
		useState<ForkAgentDefinitionMetadata>();
	const deleteTarget =
		selectedDeleteTarget?.organization_id === organizationId
			? selectedDeleteTarget
			: undefined;
	if (selectedDeleteTarget && !deleteTarget) {
		setDeleteTarget(undefined);
	}
	const definitionsQuery = useQuery(userAgentDefinitions(organizationId));
	const { editorState, openCreate, openEdit } =
		useForkAgentDefinitionEditor(organizationId);

	// Pending toggles are keyed by organization, then name, so a second
	// toggle, or a switch to another organization with the same names, does
	// not hide or borrow a pending value while its request runs. Callbacks
	// read the organization from the variables because TanStack Query runs
	// the latest options when an earlier request settles.
	const [pendingToggles, setPendingToggles] = useState<
		Readonly<Record<string, Readonly<Record<string, boolean>>>>
	>({});
	const updateOptions = updateUserAgentDefinition(queryClient);
	const toggleMutation = useMutation({
		...updateOptions,
		onMutate: ({ organizationId: orgId, name, req }) => {
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
			const { organizationId: orgId, name } = args[2];
			setPendingToggles((current) => ({
				...current,
				[orgId]: Object.fromEntries(
					Object.entries(current[orgId] ?? {}).filter(([key]) => key !== name),
				),
			}));
		},
	});

	const deleteMutation = useMutation(deleteUserAgentDefinition(queryClient));

	let deleteState: ForkAgentDefinitionDeleteState | undefined;
	if (deleteTarget) {
		const isTarget =
			deleteMutation.variables?.organizationId === organizationId &&
			deleteMutation.variables.name === deleteTarget.name;
		const isDeleting = isTarget && deleteMutation.isPending;
		deleteState = {
			definition: deleteTarget,
			error: isTarget ? deleteMutation.error : undefined,
			isDeleting,
			onConfirm: () =>
				deleteMutation.mutate(
					{ organizationId, name: deleteTarget.name },
					{
						// A lost response can still have deleted the definition,
						// so the refetched list decides whether the delete is done.
						onSettled: (_data, error, { organizationId: orgId, name }) => {
							const definitions = queryClient.getQueryData(
								userAgentDefinitions(orgId).queryKey,
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
			action={action}
			error={definitionsQuery.error}
			isLoading={definitionsQuery.isLoading}
			isRetrying={definitionsQuery.isFetching}
			onRetry={() => {
				void definitionsQuery.refetch();
			}}
			pendingToggles={pendingToggles[organizationId]}
			onToggleEnabled={(name, enabled) =>
				toggleMutation.mutate({ organizationId, name, req: { enabled } })
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
