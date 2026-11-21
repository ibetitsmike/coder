// This file is fork code: the save hook of a workflow run
// (fork-workflows experiment). It owns the save dialog's state and the
// create mutation for both tiers.

import type React from "react";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import { checkAuthorization } from "#/api/queries/authCheck";
import { createSavedWorkflow } from "#/api/queries/forkSavedWorkflows";
import type { SkillErrorDisplay } from "#/pages/AgentsPage/components/SkillEditor";
import { ForkWorkflowSaveDialog } from "./ForkWorkflowSaveDialog";

/** Where a saved workflow lands after the save dialog submits. */
type SaveTier = "user" | "organization";

type SaveTarget = {
	/** The run title, which the panel's dialog prefills the name from. */
	title: string;
	/** The run's script, saved as the workflow body. */
	script: string;
};

type ForkWorkflowSaveState = {
	/** The run the Save action would save, when a dialog is open. */
	saveTarget: SaveTarget | undefined;
	/** The name of the workflow the last save created. */
	saved: string | undefined;
	openSave: (target: SaveTarget) => void;
	/** The dialog element, rendered by the page that owns the action. */
	dialog: React.ReactNode;
};

type SaveDialogProps = {
	target: SaveTarget;
	organizationId: string;
	canCreateOrganization: boolean;
	onSaved: (name: string) => void;
	onClose: () => void;
};

const SaveDialog: React.FC<SaveDialogProps> = ({
	target,
	organizationId,
	canCreateOrganization,
	onSaved,
	onClose,
}) => {
	const queryClient = useQueryClient();
	const [submitError, setSubmitError] = useState<SkillErrorDisplay | undefined>(
		undefined,
	);
	const create = useMutation(createSavedWorkflow(queryClient));
	const isSubmitting = create.isPending;
	const submit = (
		tier: SaveTier,
		values: { name: string; description: string },
	) => {
		setSubmitError(undefined);
		create.mutate(
			{
				scope: { tier, organizationId },
				req: {
					name: values.name,
					description: values.description.trim(),
					script: target.script,
				},
			},
			{
				onSuccess: (workflow) => {
					onSaved(workflow.name);
				},
				onError: (error) => {
					setSubmitError({
						message: getErrorMessage(error, "Failed to save workflow."),
						detail: getErrorDetail(error),
					});
				},
			},
		);
	};
	return (
		<ForkWorkflowSaveDialog
			title={target.title}
			script={target.script}
			canCreateOrganization={canCreateOrganization}
			isSubmitting={isSubmitting}
			submitError={submitError}
			onSubmit={submit}
			onClose={onClose}
		/>
	);
};

/**
 * Saves one workflow run's script as a saved workflow. The dialog renders
 * through `dialog` so the page keeps it mounted inside its own providers.
 */
export const useForkWorkflowSave = (options: {
	organizationId: string;
}): ForkWorkflowSaveState => {
	const { organizationId } = options;
	const [saveTarget, setSaveTarget] = useState<SaveTarget | undefined>();
	const [saved, setSaved] = useState<string | undefined>();
	const orgPermissionQuery = useQuery({
		...checkAuthorization<{ canCreateOrganizationSkill: boolean }>({
			checks: {
				canCreateOrganizationSkill: {
					object: {
						resource_type: "organization_skill",
						organization_id: organizationId,
					},
					action: "create",
				},
			},
		}),
		enabled: Boolean(saveTarget),
	});
	const canCreateOrganization =
		orgPermissionQuery.data?.canCreateOrganizationSkill === true;
	const close = () => setSaveTarget(undefined);
	const dialog = saveTarget ? (
		<SaveDialog
			target={saveTarget}
			organizationId={organizationId}
			canCreateOrganization={canCreateOrganization}
			onSaved={(name) => {
				setSaved(name);
				close();
			}}
			onClose={close}
		/>
	) : undefined;
	return { saveTarget, saved, openSave: setSaveTarget, dialog };
};

/** The settings page a saved workflow list lives on, by tier. */
export const savedWorkflowSettingsPath = (
	tier: SaveTier,
	organizationName: string,
): string =>
	tier === "user"
		? "/agents/settings/workflows"
		: `/ai/settings/workflows?org=${encodeURIComponent(organizationName)}`;

/** The organization name of a chat, from the dashboard's organization list. */
export const useForkSavedWorkflowOrganizationName = (
	organizationId: string | undefined,
	organizations: readonly { id: string; name: string }[],
): string | undefined =>
	organizations.find((organization) => organization.id === organizationId)
		?.name;
