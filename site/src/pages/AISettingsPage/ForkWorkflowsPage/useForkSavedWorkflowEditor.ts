// This file is fork code: drives the saved workflow editor dialog
// (fork-workflows experiment).

import { isAxiosError } from "axios";
import { useState } from "react";
import {
	type QueryClient,
	useMutation,
	useQuery,
	useQueryClient,
} from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import { isNewer, isResponseLost } from "#/api/queries/forkListCache";
import {
	createSavedWorkflow,
	isSameScope,
	type SavedWorkflowScope,
	savedWorkflow,
	savedWorkflows,
	updateSavedWorkflow,
} from "#/api/queries/forkSavedWorkflows";
import type {
	CreateForkSavedWorkflowRequest,
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
	equalsSavedWorkflowArgsSchema,
	formatSavedWorkflowPhases,
	type SavedWorkflowFormValues,
} from "./savedWorkflowForm";

type EditorTarget = { scope: SavedWorkflowScope } & (
	| { mode: "create" }
	| {
			mode: "edit";
			name: string;
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

// After a lost response, the create counts as done only when the name was
// not listed before, is listed after the refetch, and the fetched script
// matches what the user submitted.
const confirmCreated = async (
	queryClient: QueryClient,
	scope: SavedWorkflowScope,
	request: CreateForkSavedWorkflowRequest,
	existedBefore: boolean,
	error: unknown,
): Promise<boolean> => {
	if (!error) {
		return true;
	}
	if (!isResponseLost(error) || existedBefore) {
		return false;
	}
	if (
		!queryClient
			.getQueryData(savedWorkflows(scope).queryKey)
			?.some((workflow) => workflow.name === request.name)
	) {
		return false;
	}
	try {
		const created = await queryClient.fetchQuery(
			savedWorkflow(queryClient, scope, request.name),
		);
		return created.script === request.script;
	} catch {
		return false;
	}
};

/**
 * Each editor stays bound to the scope it opened in and closes when the
 * selected scope changes. A request whose response is lost counts as
 * successful when the refetched workflow shows the submitted script.
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
				const request = buildCreateSavedWorkflowRequest(values);
				const existedBefore =
					queryClient
						.getQueryData(savedWorkflows(scope).queryKey)
						?.some((workflow) => workflow.name === request.name) ?? false;

				createMutation.mutate(
					{ scope, req: request },
					{
						onSettled: async (_data, error) => {
							if (
								await confirmCreated(
									queryClient,
									scope,
									request,
									existedBefore,
									error,
								)
							) {
								closeEditorTarget(editorTarget);
								toast.success("Workflow created.");
							}
						},
					},
				);
			},
			onClose: () => closeEditor(createMutation.isPending),
		};
	} else if (editorTarget?.mode === "edit") {
		const detail = workflowQuery.data;
		const listed = workflowsQuery.data?.find(
			(workflow) => workflow.name === editorTarget.name,
		);
		const isGone = workflowsQuery.data !== undefined && !listed;
		// Content, cached or just fetched, can predate a save, delete, or
		// recreate that the list already shows, so it seeds the editor only
		// when it is as new as the listed workflow.
		const isCurrent =
			!isGone &&
			detail !== undefined &&
			(!listed ||
				(detail.id === listed.id
					? !isNewer(listed, detail)
					: isNewer(detail, listed)));
		const isOutdated =
			detail !== undefined &&
			!isGone &&
			!isCurrent &&
			workflowQuery.isSuccess &&
			!workflowQuery.isFetching;
		const loaded = isCurrent ? editFormValues(detail) : undefined;
		// The editor keeps the first values it gets, cached or fetched, so a
		// later refetch cannot reset the user's edits.
		if (loaded && !editorTarget.values) {
			setEditorTarget({
				...editorTarget,
				values: loaded,
				loadedFrom: detail,
			});
		}
		const { name, values, loadedFrom } = editorTarget;
		// Saving by name would overwrite a workflow deleted, edited, or
		// recreated since the editor loaded it, so the user keeps the draft.
		const isChanged =
			loadedFrom !== undefined &&
			(isGone ||
				[listed, detail].some(
					(current) => current !== undefined && isNewer(current, loadedFrom),
				));
		let staleError: Error | undefined;
		if (isGone) {
			staleError = new Error("This saved workflow no longer exists.");
		} else if (isOutdated) {
			staleError = new Error("The saved workflow changed while it loaded.");
		}
		const isNotFound =
			isAxiosError(workflowQuery.error) &&
			workflowQuery.error.response?.status === 404;
		const loadError = values ? undefined : (workflowQuery.error ?? staleError);
		editorState = {
			mode: "edit",
			initialValues: values,
			isLoading: !values && !loadError,
			loadError,
			onRetry:
				(workflowQuery.error && !isNotFound) || isOutdated
					? () => {
							void workflowQuery.refetch();
						}
					: undefined,
			isRetrying: workflowQuery.isFetching,
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
				const request = buildUpdateSavedWorkflowRequest(formValues);
				saveMutation.mutate(
					{ scope, name, req: request },
					{
						// A lost response can hide a save that committed, which the
						// refetched workflow then shows. Every submitted field must
						// match, because an older workflow can hold the script alone.
						onSettled: (_data, error) => {
							const saved = queryClient.getQueryData(
								savedWorkflow(queryClient, scope, name).queryKey,
							);
							if (
								!error ||
								(isResponseLost(error) &&
									saved !== undefined &&
									saved.script === request.script &&
									saved.description === request.description &&
									saved.phases.join("\n") ===
										formValues.phases
											.split("\n")
											.map((phase) => phase.trim())
											.filter((phase) => phase !== "")
											.join("\n") &&
									equalsSavedWorkflowArgsSchema(
										saved.args_schema,
										request.args_schema,
									))
							) {
								closeEditorTarget(editorTarget);
								toast.success("Workflow saved.");
							}
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
			});
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
