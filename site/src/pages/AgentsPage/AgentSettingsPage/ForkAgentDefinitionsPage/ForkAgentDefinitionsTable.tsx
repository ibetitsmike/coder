// This file is fork code: wires the fork-agent-definitions table to the API.

import { isAxiosError } from "axios";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	createUserAgentDefinition,
	deleteUserAgentDefinition,
	isNewer,
	isResponseLost,
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

// Targets keep the organization they were opened in, so a change of the
// selected organization closes them instead of sending their input there.
type EditorTarget = { organizationId: string } & (
	| { mode: "create" }
	| {
			mode: "edit";
			name: string;
			values?: AgentDefinitionFormValues;
			loadedFrom?: Pick<ForkAgentDefinition, "id" | "updated_at">;
	  }
);

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
	const [selectedDeleteTarget, setDeleteTarget] =
		useState<ForkAgentDefinitionMetadata>();
	const deleteTarget =
		selectedDeleteTarget?.organization_id === organizationId
			? selectedDeleteTarget
			: undefined;
	if (selectedDeleteTarget && !deleteTarget) {
		setDeleteTarget(undefined);
	}
	const [selectedEditorTarget, setEditorTarget] = useState<EditorTarget>();
	const editorTarget =
		selectedEditorTarget?.organizationId === organizationId
			? selectedEditorTarget
			: undefined;
	if (selectedEditorTarget && !editorTarget) {
		setEditorTarget(undefined);
	}
	const definitionsQuery = useQuery(userAgentDefinitions(organizationId));
	const editName = editorTarget?.mode === "edit" ? editorTarget.name : "";
	const definitionQuery = useQuery({
		...userAgentDefinition(queryClient, organizationId, editName),
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

	// A request that settles after its editor was replaced, for example by
	// one opened in another organization, leaves the newer editor open.
	const closeEditorTarget = (target: EditorTarget) =>
		setEditorTarget((current) => (current === target ? undefined : current));

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
				// refetched list then shows with this content. A name listed
				// before the request means it did not. With the list unknown,
				// the content match decides: a definition that already had
				// this content holds what the user submitted.
				const existedBefore = isListed() ?? false;
				const isCreated = async (error: unknown) => {
					if (!error) {
						return true;
					}
					if (!isResponseLost(error) || existedBefore || !isListed()) {
						return false;
					}
					try {
						const created = await queryClient.fetchQuery(
							userAgentDefinition(queryClient, organizationId, name),
						);
						return created.content === content;
					} catch {
						return false;
					}
				};
				createMutation.mutate(
					{ organizationId, req: { content } },
					{
						onSettled: async (_data, error) => {
							if (await isCreated(error)) {
								closeEditorTarget(editorTarget);
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
		const isGone = definitionsQuery.data !== undefined && !listed;
		// Content, cached or just fetched, can predate a save, delete, or
		// recreate that the list already shows, so it seeds the editor only
		// when it is as new as the listed definition.
		const isCurrent =
			detail !== undefined &&
			!isGone &&
			(!listed ||
				(detail.id === listed.id
					? !isNewer(listed, detail)
					: isNewer(detail, listed)));
		const isOutdated =
			detail !== undefined &&
			!isGone &&
			!isCurrent &&
			definitionQuery.isSuccess &&
			!definitionQuery.isFetching;
		const loaded = isCurrent ? editFormValues(detail) : undefined;
		// The editor keeps the first values it gets, cached or fetched, so a
		// later refetch cannot reset the user's edits.
		if (loaded?.values && !editorTarget.values) {
			setEditorTarget({
				...editorTarget,
				values: loaded.values,
				loadedFrom: detail,
			});
		}
		const { name, values, loadedFrom } = editorTarget;
		// Saving by name would overwrite a definition deleted, edited, or
		// recreated since the editor loaded it, so the user keeps the draft
		// instead. An older listed row, such as one a recreate replaced before
		// the editor opened, is not a change.
		const isChanged =
			loadedFrom !== undefined &&
			(isGone ||
				[listed, detail].some(
					(current) => current !== undefined && isNewer(current, loadedFrom),
				));
		const loadError = values
			? undefined
			: (loaded?.error ??
				definitionQuery.error ??
				(isGone
					? new Error("This agent definition no longer exists.")
					: isOutdated
						? new Error("The agent definition changed while it loaded.")
						: undefined));
		editorState = {
			mode: "edit",
			initialValues: values,
			isLoading: !values && !loadError,
			loadError,
			onRetry:
				(definitionQuery.error &&
					!(
						isAxiosError(definitionQuery.error) &&
						definitionQuery.error.response?.status === 404
					)) ||
				isOutdated
					? () => {
							void definitionQuery.refetch();
						}
					: undefined,
			isRetrying: definitionQuery.isFetching,
			submitError: isChanged
				? {
						message:
							"This agent definition changed or was deleted after you opened it.",
						detail:
							"Saving would overwrite the newer version. Copy your changes, then close and reopen the editor.",
					}
				: submitError(saveMutation.error, "Failed to save agent definition."),
			isSubmitting: saveMutation.isPending,
			onSubmit: ({ content }, onSettled) => {
				if (isChanged) {
					onSettled();
					return;
				}
				saveMutation.mutate(
					{ organizationId, name, req: { content } },
					{
						// A lost response can hide a save that committed, which the
						// refetched definition then shows.
						onSettled: (_data, error) => {
							const saved = queryClient.getQueryData(
								userAgentDefinition(queryClient, organizationId, name).queryKey,
							);
							if (
								!error ||
								(isResponseLost(error) && saved?.content === content)
							) {
								closeEditorTarget(editorTarget);
								toast.success("Agent definition saved.");
							}
							onSettled();
						},
					},
				);
			},
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
				setEditorTarget({ mode: "create", organizationId });
			}}
			onEdit={(definition) => {
				saveMutation.reset();
				setEditorTarget({
					mode: "edit",
					organizationId,
					name: definition.name,
				});
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
