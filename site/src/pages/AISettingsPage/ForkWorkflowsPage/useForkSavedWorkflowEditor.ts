// This file is fork code: drives the saved workflow editor dialog
// (fork-workflows experiment).

import { isAxiosError } from "axios";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import { isNewer } from "#/api/queries/forkListCache";
import {
	createSavedWorkflow,
	isSameScope,
	type SavedWorkflowScope,
	savedWorkflow,
	savedWorkflowKey,
	savedWorkflows,
	savedWorkflowsKey,
	updateSavedWorkflow,
} from "#/api/queries/forkSavedWorkflows";
import type {
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
} from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import type {
	SkillEditorMode,
	SkillErrorDisplay,
} from "#/pages/AgentsPage/components/SkillEditor";
import {
	buildCreateSavedWorkflowRequest,
	buildUpdateSavedWorkflowRequest,
	emptySavedWorkflowFormValues,
	formatSavedWorkflowPhases,
	type SavedWorkflowFormValues,
} from "./savedWorkflowForm";

type EditorTarget = { scope: SavedWorkflowScope } & (
	| { mode: "create" }
	| {
			mode: "edit";
			name: string;
			/** When the editor opened, so only data fetched since seeds it. */
			openedAt: number;
			values?: SavedWorkflowFormValues;
			loadedFrom?: Pick<ForkSavedWorkflow, "id" | "updated_at">;
	  }
);

const toErrorDisplay = (
	error: unknown,
	fallback: string,
): SkillErrorDisplay | undefined =>
	error
		? {
				message: getErrorMessage(error, fallback),
				detail: getErrorDetail(error),
			}
		: undefined;

const editFormValues = (
	workflow: ForkSavedWorkflow,
): SavedWorkflowFormValues => ({
	name: workflow.name,
	description: workflow.description,
	script: workflow.script,
	argsSchema: workflow.args_schema
		? JSON.stringify(workflow.args_schema, null, 2)
		: "",
	phases: formatSavedWorkflowPhases(workflow.phases),
});

/**
 * Each editor stays bound to the scope it opened in and closes when the
 * selected scope changes. A failed request keeps the editor open with its
 * error and the user's draft, so the user can retry.
 */
export const useForkSavedWorkflowEditor = (scope: SavedWorkflowScope) => {
	const queryClient = useQueryClient();
	const [selectedEditorTarget, setEditorTarget] = useState<EditorTarget>();
	const editorTarget =
		selectedEditorTarget && isSameScope(selectedEditorTarget.scope, scope)
			? selectedEditorTarget
			: undefined;
	if (selectedEditorTarget && !editorTarget) {
		setEditorTarget(undefined);
	}
	const workflowsQuery = useQuery(savedWorkflows(scope));
	const editName = editorTarget?.mode === "edit" ? editorTarget.name : "";
	const workflowQuery = useQuery({
		...savedWorkflow(queryClient, scope, editName),
		enabled: Boolean(editName),
	});

	const createMutation = useMutation(createSavedWorkflow(queryClient));
	const saveMutation = useMutation(updateSavedWorkflow(queryClient));

	const closeEditor = (isSubmitting: boolean) => {
		if (!isSubmitting) {
			setEditorTarget(undefined);
		}
	};

	// A request that settles after its editor was replaced, for example by
	// one opened in another organization, leaves the newer editor open.
	const closeEditorTarget = (target: EditorTarget) =>
		setEditorTarget((current) => (current === target ? undefined : current));

	// Cached rows and content can predate a change made elsewhere, so an
	// editor opens on a fresh fetch of both.
	const refetchForEdit = (name: string) =>
		Promise.all([
			queryClient.invalidateQueries({
				queryKey: savedWorkflowsKey(scope),
				exact: true,
			}),
			queryClient.invalidateQueries({
				queryKey: savedWorkflowKey(scope, name),
				exact: true,
				refetchType: "all",
			}),
		]);

	let editorState: ForkSavedWorkflowEditorState | undefined;
	if (editorTarget?.mode === "create") {
		editorState = {
			mode: "create",
			initialValues: emptySavedWorkflowFormValues,
			submitError: toErrorDisplay(
				createMutation.error,
				"Failed to create saved workflow.",
			),
			isSubmitting: createMutation.isPending,
			onSubmit: (values) => {
				createMutation.mutate(
					{ scope, req: buildCreateSavedWorkflowRequest(values) },
					{
						onSuccess: () => {
							closeEditorTarget(editorTarget);
							toast.success("Workflow created.");
						},
					},
				);
			},
			onClose: () => closeEditor(createMutation.isPending),
		};
	} else if (editorTarget?.mode === "edit") {
		const { name, openedAt, values, loadedFrom } = editorTarget;
		const isListFresh =
			workflowsQuery.dataUpdatedAt >= openedAt && !workflowsQuery.isFetching;
		const isDetailFresh =
			workflowQuery.dataUpdatedAt >= openedAt && !workflowQuery.isFetching;
		const listError =
			workflowsQuery.isError &&
			!workflowsQuery.isFetching &&
			workflowsQuery.errorUpdatedAt >= openedAt
				? workflowsQuery.error
				: undefined;
		const detailError =
			workflowQuery.isError && !workflowQuery.isFetching
				? workflowQuery.error
				: undefined;
		const detail = workflowQuery.data;
		const listed = workflowsQuery.data?.find(
			(workflow) => workflow.name === name,
		);
		const isGone = isListFresh && !listed;
		// Both fetches finished since the editor opened, but one can still
		// see a change the other missed, so the content seeds the editor only
		// when it is the listed workflow at its listed revision.
		const isCurrent =
			isListFresh &&
			isDetailFresh &&
			detail !== undefined &&
			listed !== undefined &&
			detail.id === listed.id &&
			!isNewer(listed, detail);
		const isOutdated = isListFresh && isDetailFresh && !isGone && !isCurrent;
		if (isCurrent && !values) {
			setEditorTarget({
				...editorTarget,
				values: editFormValues(detail),
				loadedFrom: detail,
			});
		}
		// Saving by name would overwrite a workflow deleted, edited, or
		// recreated since the editor loaded it, so the user keeps the draft.
		const isChanged =
			loadedFrom !== undefined &&
			(isGone ||
				[listed, detail].some(
					(current) =>
						current !== undefined &&
						(current.id !== loadedFrom.id || isNewer(current, loadedFrom)),
				));
		let staleError: Error | undefined;
		if (isGone) {
			staleError = new Error("This saved workflow no longer exists.");
		} else if (isOutdated) {
			staleError = new Error("The saved workflow changed while it loaded.");
		}
		const isNotFound =
			isAxiosError(detailError) && detailError.response?.status === 404;
		const loadError = values
			? undefined
			: (listError ?? detailError ?? staleError);
		editorState = {
			mode: "edit",
			initialValues: values,
			isLoading: !values && !loadError,
			loadError,
			onRetry:
				(loadError && !isGone && !isNotFound) || isOutdated
					? () => {
							void refetchForEdit(name);
						}
					: undefined,
			isRetrying: workflowsQuery.isFetching || workflowQuery.isFetching,
			submitError: isChanged
				? {
						message:
							"This saved workflow changed or was deleted after you opened it.",
						detail:
							"Saving would overwrite the newer version. Copy your changes, then close and reopen the editor.",
					}
				: toErrorDisplay(saveMutation.error, "Failed to save workflow."),
			isSubmitting: saveMutation.isPending,
			onSubmit: (formValues) => {
				if (isChanged) {
					return;
				}
				saveMutation.mutate(
					{ scope, name, req: buildUpdateSavedWorkflowRequest(formValues) },
					{
						onSuccess: () => {
							closeEditorTarget(editorTarget);
							toast.success("Workflow saved.");
						},
					},
				);
			},
			onClose: () => closeEditor(saveMutation.isPending),
		};
	}

	return {
		editorState,
		openCreate: () => {
			createMutation.reset();
			setEditorTarget({ mode: "create", scope });
		},
		openEdit: (workflow: ForkSavedWorkflowMetadata) => {
			saveMutation.reset();
			setEditorTarget({
				mode: "edit",
				scope,
				name: workflow.name,
				openedAt: Date.now(),
			});
			void refetchForEdit(workflow.name);
		},
	};
};

export type ForkSavedWorkflowEditorState = {
	/** Everything the editor dialog renders one saved workflow form with. */
	mode: SkillEditorMode;
	/** Unset while an edited workflow loads or after it fails to load. */
	initialValues?: SavedWorkflowFormValues;
	isLoading?: boolean;
	loadError?: unknown;
	/** Shown with a load error that a refetch can fix. */
	onRetry?: () => void;
	isRetrying?: boolean;
	submitError?: SkillErrorDisplay;
	isSubmitting: boolean;
	onSubmit: (values: SavedWorkflowFormValues) => void;
	/**
	 * While isSubmitting, this should keep the dialog open, because a failed
	 * save is reported only as submitError here.
	 */
	onClose: () => void;
};
