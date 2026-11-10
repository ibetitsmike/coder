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

	// Pending toggles are keyed by organization, then name, so a second
	// toggle, or a switch to another organization with the same names, does
	// not hide or borrow a pending value while its request runs.
	const [pendingToggles, setPendingToggles] = useState<
		Readonly<Record<string, Readonly<Record<string, boolean>>>>
	>({});
	const updateOptions = updateUserAgentDefinition(queryClient, organizationId);
	const toggleMutation = useMutation({
		...updateOptions,
		onMutate: ({ name, req }) => {
			setPendingToggles((current) => ({
				...current,
				[organizationId]: {
					...current[organizationId],
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
			const { name } = args[2];
			setPendingToggles((current) => ({
				...current,
				[organizationId]: Object.fromEntries(
					Object.entries(current[organizationId] ?? {}).filter(
						([key]) => key !== name,
					),
				),
			}));
		},
	});

	const deleteMutation = useMutation(
		deleteUserAgentDefinition(queryClient, organizationId),
	);

	let deleteState: ForkAgentDefinitionDeleteState | undefined;
	if (deleteTarget) {
		const isTarget = deleteMutation.variables === deleteTarget.name;
		const isDeleting = isTarget && deleteMutation.isPending;
		deleteState = {
			definition: deleteTarget,
			error: isTarget ? deleteMutation.error : undefined,
			isDeleting,
			onConfirm: () =>
				deleteMutation.mutate(deleteTarget.name, {
					onSuccess: () => {
						setDeleteTarget(undefined);
						toast.success("Agent definition deleted.");
					},
				}),
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
