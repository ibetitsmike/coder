// This file is fork code: wires the fork-workflows saved workflows table to
// the API.

import { isAxiosError } from "axios";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import {
	deleteSavedWorkflow,
	isSameScope,
	SavedWorkflowChangedError,
	type SavedWorkflowScope,
	savedWorkflow,
	savedWorkflowKey,
	savedWorkflows,
} from "#/api/queries/forkSavedWorkflows";
import type {
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
} from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import type { SkillAccess } from "#/pages/AgentsPage/utils/skills";
import {
	type ForkSavedWorkflowDeleteState,
	ForkSavedWorkflowsTableView,
} from "./ForkSavedWorkflowsTableView";
import { useForkSavedWorkflowEditor } from "./useForkSavedWorkflowEditor";

const copy = {
	user: {
		title: "Personal workflows",
		description:
			"Starlark workflows you own in this organization. Your chats run one with run_workflow.",
		emptyMessage: "No personal workflows in this organization yet.",
		deleteMessage: (name: string) =>
			`Your personal workflow ${name} will be deleted. Organization workflows with the same name are not affected. This action cannot be undone.`,
	},
	organization: {
		title: "Organization workflows",
		description:
			"Starlark workflows owned by this organization. Members can run one with run_workflow when its permissions allow.",
		emptyMessage: "No organization workflows yet.",
		deleteMessage: (name: string) =>
			`The organization workflow ${name} will be deleted. Personal workflows with the same name are not affected. This action cannot be undone.`,
	},
} satisfies Record<
	SavedWorkflowScope["tier"],
	{
		title: string;
		description: string;
		emptyMessage: string;
		deleteMessage: (name: string) => string;
	}
>;

type ForkSavedWorkflowsTableProps = {
	scope: SavedWorkflowScope;
	access?: SkillAccess;
	onManagePermissions?: (
		workflow: ForkSavedWorkflowMetadata,
		onCloseAutoFocus: (event: Event) => void,
	) => void;
};

/** Wires the saved workflows table of one scope to the API. */
export const ForkSavedWorkflowsTable: React.FC<
	ForkSavedWorkflowsTableProps
> = ({ scope, access, onManagePermissions }) => {
	const queryClient = useQueryClient();
	const [deleteTarget, setDeleteTarget] = useState<{
		workflow: ForkSavedWorkflowMetadata;
		scope: SavedWorkflowScope;
	}>();
	// Targets keep the scope they were opened in, so a change of the
	// selected organization closes them instead of acting there.
	const activeDeleteTarget =
		deleteTarget && isSameScope(deleteTarget.scope, scope)
			? deleteTarget.workflow
			: undefined;
	if (deleteTarget && !activeDeleteTarget) {
		setDeleteTarget(undefined);
	}
	const [detailTarget, setDetailTarget] = useState<{
		name: string;
		scope: SavedWorkflowScope;
	}>();
	const activeDetailName =
		detailTarget && isSameScope(detailTarget.scope, scope)
			? detailTarget.name
			: undefined;
	if (detailTarget && !activeDetailName) {
		setDetailTarget(undefined);
	}
	const detailQuery = useQuery({
		...savedWorkflow(queryClient, scope, activeDetailName ?? ""),
		enabled: Boolean(activeDetailName),
	});
	const workflowsQuery = useQuery(savedWorkflows(scope));
	const { editorState, openCreate, openEdit } =
		useForkSavedWorkflowEditor(scope);

	const deleteMutation = useMutation(deleteSavedWorkflow(queryClient));

	let deleteState: ForkSavedWorkflowDeleteState | undefined;
	if (activeDeleteTarget) {
		const isTarget =
			deleteMutation.variables !== undefined &&
			isSameScope(deleteMutation.variables.scope, scope) &&
			deleteMutation.variables.id === activeDeleteTarget.id;
		const isDeleting = isTarget && deleteMutation.isPending;
		deleteState = {
			workflow: activeDeleteTarget,
			message: copy[scope.tier].deleteMessage(activeDeleteTarget.name),
			error: isTarget ? deleteMutation.error : undefined,
			isDeleting,
			onConfirm: () =>
				deleteMutation.mutate(
					{
						scope,
						name: activeDeleteTarget.name,
						id: activeDeleteTarget.id,
					},
					{
						onSuccess: () => {
							setDeleteTarget(undefined);
							toast.success("Workflow deleted.");
						},
						// The list refetch the mutation starts shows the user what
						// the name holds now, so the stale confirmation closes.
						onError: (error) => {
							if (error instanceof SavedWorkflowChangedError) {
								setDeleteTarget(undefined);
								toast.info(
									`${activeDeleteTarget.name} was deleted or replaced since you picked it, so nothing was deleted.`,
								);
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
		<ForkSavedWorkflowsTableView
			title={copy[scope.tier].title}
			workflows={workflowsQuery.data ?? []}
			description={copy[scope.tier].description}
			emptyMessage={copy[scope.tier].emptyMessage}
			access={access}
			error={workflowsQuery.error}
			isLoading={workflowsQuery.isLoading}
			isRetrying={workflowsQuery.isFetching}
			onRetry={() => {
				void workflowsQuery.refetch();
			}}
			onCreate={openCreate}
			onView={(workflow) => {
				// The detail cache is keyed by name, so it may hold an earlier
				// workflow of that name, which must not show for this row.
				const detailKey = savedWorkflowKey(scope, workflow.name);
				const cached = queryClient.getQueryData<ForkSavedWorkflow>(detailKey);
				if (cached && cached.id !== workflow.id) {
					queryClient.removeQueries({ queryKey: detailKey, exact: true });
				}
				setDetailTarget({ name: workflow.name, scope });
			}}
			onEdit={openEdit}
			onDelete={(workflow) => {
				deleteMutation.reset();
				setDeleteTarget({ workflow, scope });
			}}
			onManagePermissions={onManagePermissions}
			editorState={editorState}
			deleteState={deleteState}
			detailState={
				activeDetailName
					? {
							workflow: detailQuery.data,
							isLoading: detailQuery.isLoading,
							error: detailQuery.error,
							// A 404 has already dropped the row from the list, so a
							// retry can only fail again.
							onRetry:
								detailQuery.isError &&
								!(
									isAxiosError(detailQuery.error) &&
									detailQuery.error.response?.status === 404
								)
									? () => {
											void detailQuery.refetch();
										}
									: undefined,
							isRetrying: detailQuery.isFetching,
							onClose: () => setDetailTarget(undefined),
						}
					: undefined
			}
		/>
	);
};
