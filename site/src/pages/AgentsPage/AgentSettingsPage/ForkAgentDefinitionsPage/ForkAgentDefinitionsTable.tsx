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

type ForkAgentDefinitionsTableProps = {
	organizationId: string;
	action?: React.ReactNode;
};

export const ForkAgentDefinitionsTable: React.FC<
	ForkAgentDefinitionsTableProps
> = ({ organizationId, action }) => {
	const queryClient = useQueryClient();
	const [deleteTarget, setDeleteTarget] =
		useState<ForkAgentDefinitionMetadata>();
	const definitionsQuery = useQuery(userAgentDefinitions(organizationId));

	const toggleMutation = useMutation({
		...updateUserAgentDefinition(queryClient, organizationId),
		onError: (error, { name }) => {
			toast.error(getErrorMessage(error, `Failed to update ${name}.`), {
				description: getErrorDetail(error),
			});
		},
	});

	const deleteMutationOptions = deleteUserAgentDefinition(
		queryClient,
		organizationId,
	);
	const deleteMutation = useMutation({
		...deleteMutationOptions,
		onSuccess: async (_data, name) => {
			await deleteMutationOptions.onSuccess();
			setDeleteTarget((current) =>
				current?.name === name ? undefined : current,
			);
			toast.success("Agent definition deleted.");
		},
	});

	let deleteState: ForkAgentDefinitionDeleteState | undefined;
	if (deleteTarget) {
		const isTarget = deleteMutation.variables === deleteTarget.name;
		deleteState = {
			definition: deleteTarget,
			error: isTarget ? deleteMutation.error : undefined,
			isDeleting: isTarget && deleteMutation.isPending,
			onConfirm: () => deleteMutation.mutate(deleteTarget.name),
			onClose: () => setDeleteTarget(undefined),
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
			pendingToggle={
				toggleMutation.isPending
					? {
							name: toggleMutation.variables.name,
							enabled: Boolean(toggleMutation.variables.req.enabled),
						}
					: undefined
			}
			onToggleEnabled={(name, enabled) =>
				toggleMutation.mutate({ name, req: { enabled } })
			}
			onDelete={(definition) => {
				deleteMutation.reset();
				setDeleteTarget(definition);
			}}
			deleteState={deleteState}
		/>
	);
};
