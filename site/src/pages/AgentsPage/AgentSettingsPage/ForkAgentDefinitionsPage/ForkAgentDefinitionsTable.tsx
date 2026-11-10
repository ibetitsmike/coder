// This file is fork code: wires the fork-agent-definitions table to the API.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	createUserAgentDefinition,
	deleteUserAgentDefinition,
	updateUserAgentDefinition,
	userAgentDefinition,
	userAgentDefinitions,
} from "#/api/queries/forkAgentDefinitions";
import type {
	ForkAgentDefinition,
	ForkAgentDefinitionMetadata,
} from "#/api/typesGenerated";
import { toast } from "#/components/Toaster/toast";
import type { SkillErrorDisplay } from "../../components/SkillEditor";
import {
	type AgentDefinitionFormValues,
	emptyAgentDefinitionFormValues,
	parseAgentDefinitionMarkdown,
} from "./agentDefinitionMarkdown";
import type { ForkAgentDefinitionEditorState } from "./ForkAgentDefinitionEditorDialog";
import {
	type ForkAgentDefinitionDeleteState,
	ForkAgentDefinitionsTableView,
} from "./ForkAgentDefinitionsTableView";

type EditorTarget =
	| { mode: "create" }
	| { mode: "edit"; name: string; values?: AgentDefinitionFormValues };

const submitError = (
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
	definition: ForkAgentDefinition,
): { values?: AgentDefinitionFormValues; error?: unknown } => {
	try {
		return {
			// The API only accepts content that keeps the stored name.
			values: {
				...parseAgentDefinitionMarkdown(definition.content),
				name: definition.name,
			},
		};
	} catch (error) {
		return { error };
	}
};

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
	const [editorTarget, setEditorTarget] = useState<EditorTarget>();
	const definitionsQuery = useQuery(userAgentDefinitions(organizationId));
	const editName = editorTarget?.mode === "edit" ? editorTarget.name : "";
	const definitionQuery = useQuery({
		...userAgentDefinition(organizationId, editName),
		enabled: Boolean(editName),
	});

	const createMutation = useMutation(createUserAgentDefinition(queryClient));
	const saveMutation = useMutation(updateUserAgentDefinition(queryClient));

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

	// A failed request reports its error in the editor, so the editor stays
	// open, with the user's input, until the request settles.
	const closeEditor = (isSubmitting: boolean) => {
		if (!isSubmitting) {
			setEditorTarget(undefined);
		}
	};

	let editorState: ForkAgentDefinitionEditorState | undefined;
	if (editorTarget?.mode === "create") {
		editorState = {
			mode: "create",
			initialValues: emptyAgentDefinitionFormValues,
			submitError: submitError(
				createMutation.error,
				"Failed to create agent definition.",
			),
			isSubmitting: createMutation.isPending,
			onSubmit: ({ name, content }, onSettled) => {
				const listKey = userAgentDefinitions(organizationId).queryKey;
				const isListed = () =>
					queryClient
						.getQueryData(listKey)
						?.some((definition) => definition.name === name);
				// A lost response can hide a create that committed, which the
				// refetched list then shows. A name listed before the request,
				// or an unknown list, means the failure is real.
				const existedBefore = isListed() ?? true;
				createMutation.mutate(
					{ organizationId, req: { content } },
					{
						onSettled: (_data, error) => {
							if (!error || (!existedBefore && isListed())) {
								setEditorTarget(undefined);
								toast.success("Agent definition created.");
							}
							onSettled();
						},
					},
				);
			},
			onClose: () => closeEditor(createMutation.isPending),
		};
	} else if (editorTarget?.mode === "edit") {
		const detail = definitionQuery.data;
		const listed = definitionsQuery.data?.find(
			(definition) => definition.name === editorTarget.name,
		);
		// Cached content can predate a save, delete, or recreate that the
		// list already shows, so it seeds the editor only when it is as new
		// as the listed definition or a fetch just returned it.
		const isCurrent =
			detail !== undefined &&
			(!listed ||
				(detail.id === listed.id &&
					new Date(detail.updated_at) >= new Date(listed.updated_at)) ||
				(definitionQuery.isSuccess && !definitionQuery.isFetching));
		const loaded = isCurrent ? editFormValues(detail) : undefined;
		// The editor keeps the first values it gets, cached or fetched, so a
		// later refetch cannot reset the user's edits.
		if (loaded?.values && !editorTarget.values) {
			setEditorTarget({ ...editorTarget, values: loaded.values });
		}
		const { name, values } = editorTarget;
		const loadError = values
			? undefined
			: (loaded?.error ?? definitionQuery.error);
		editorState = {
			mode: "edit",
			initialValues: values,
			isLoading: !values && !loadError,
			loadError,
			onRetry: definitionQuery.error
				? () => {
						void definitionQuery.refetch();
					}
				: undefined,
			isRetrying: definitionQuery.isFetching,
			submitError: submitError(
				saveMutation.error,
				"Failed to save agent definition.",
			),
			isSubmitting: saveMutation.isPending,
			onSubmit: ({ content }, onSettled) =>
				saveMutation.mutate(
					{ organizationId, name, req: { content } },
					{
						onSuccess: () => {
							setEditorTarget(undefined);
							toast.success("Agent definition saved.");
						},
						onSettled,
					},
				),
			onClose: () => closeEditor(saveMutation.isPending),
		};
	}

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
			onCreate={() => {
				createMutation.reset();
				setEditorTarget({ mode: "create" });
			}}
			onEdit={(definition) => {
				saveMutation.reset();
				setEditorTarget({ mode: "edit", name: definition.name });
			}}
			editorState={editorState}
			onDelete={(definition) => {
				deleteMutation.reset();
				setDeleteTarget(definition);
			}}
			deleteState={deleteState}
		/>
	);
};
