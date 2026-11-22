// This file is fork code: the save hook of a workflow run
// (fork-workflows experiment). It owns the save dialog's state and the
// create mutation for both tiers.

import type React from "react";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import { checkAuthorization } from "#/api/queries/authCheck";
import type { SavedWorkflowScope } from "#/api/queries/forkSavedWorkflows";
import { createSavedWorkflow } from "#/api/queries/forkSavedWorkflows";
import type { SkillErrorDisplay } from "#/pages/AgentsPage/components/SkillEditor";
import { buildCreateSavedWorkflowRequest } from "#/pages/AISettingsPage/ForkWorkflowsPage/savedWorkflowForm";
import { ForkWorkflowSaveDialog } from "./ForkWorkflowSaveDialog";

/** Where a saved workflow lands after the save dialog submits. */
export type SaveTier = SavedWorkflowScope["tier"];

type SaveTarget = {
	/** The run title, which the panel's dialog prefills the name from. */
	title: string;
	/** The run's script, saved as the workflow body. */
	script: string;
};

type ForkWorkflowSaveState = {
	/** The name and tier of the workflow the last save created. */
	saved: { name: string; tier: SaveTier } | undefined;
	openSave: (target: SaveTarget) => void;
	/** The dialog element, rendered by the page that owns the action. */
	dialog: React.ReactNode;
};

type SaveDialogProps = {
	target: SaveTarget;
	organizationId: string;
	/**
	 * Whether the viewer may create organization workflows. Undefined while
	 * the authorization check is still loading; the dialog then holds the
	 * Personal submission until the answer arrives.
	 */
	canCreateOrganization: boolean | undefined;
	canCreateOrganizationError: unknown;
	/**
	 * Whether the authorization check is in flight, as opposed to failed:
	 * only an in-flight answer holds the Personal submission. Covers a
	 * refetch of a cached answer, not just the first load.
	 */
	isPermissionChecking: boolean;
	savedLinkRef: React.RefObject<HTMLAnchorElement | null>;
	onSaved: (name: string, tier: SaveTier) => void;
	onClose: () => void;
};

const SaveDialog: React.FC<SaveDialogProps> = ({
	target,
	organizationId,
	canCreateOrganization,
	canCreateOrganizationError,
	isPermissionChecking,
	savedLinkRef,
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
				// The request builder trims the fields, so the request matches
				// what the shared validation accepted.
				req: buildCreateSavedWorkflowRequest({
					name: values.name,
					description: values.description,
					script: target.script,
					argsSchema: "",
					phases: "",
				}),
			},
			{
				onSuccess: (workflow) => {
					onSaved(workflow.name, tier);
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
			canCreateOrganizationError={canCreateOrganizationError}
			isPermissionChecking={isPermissionChecking}
			isSubmitting={isSubmitting}
			submitError={submitError}
			// The link only exists after a save, so closing without one
			// keeps the dialog's default focus move. Closing after one
			// redirects focus to the link instead of the dead Save button's
			// position.
			onCloseAutoFocus={(event) => {
				if (savedLinkRef.current) {
					event.preventDefault();
					savedLinkRef.current.focus();
				}
			}}
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
	/** Receives focus when the dialog closes after a successful save. */
	savedLinkRef: React.RefObject<HTMLAnchorElement | null>;
}): ForkWorkflowSaveState => {
	const { organizationId, savedLinkRef } = options;
	const [saveTarget, setSaveTarget] = useState<SaveTarget | undefined>();
	const [saved, setSaved] = useState<ForkWorkflowSaveState["saved"]>(undefined);
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
	// An in-flight check is as unresolved as a first load: the grant it
	// re-checks may have been revoked while the cached answer looked fresh.
	// isFetching covers both, including a refetch of a cached answer.
	const isPermissionChecking = orgPermissionQuery.isFetching;
	const canCreateOrganization =
		orgPermissionQuery.data?.canCreateOrganizationSkill === true
			? true
			: orgPermissionQuery.data
				? false
				: undefined;
	const close = () => setSaveTarget(undefined);
	const dialog = saveTarget ? (
		<SaveDialog
			target={saveTarget}
			organizationId={organizationId}
			canCreateOrganization={canCreateOrganization}
			canCreateOrganizationError={orgPermissionQuery.error}
			isPermissionChecking={isPermissionChecking}
			savedLinkRef={savedLinkRef}
			onSaved={(name, tier) => {
				setSaved({ name, tier });
				close();
			}}
			onClose={close}
		/>
	) : undefined;
	return { saved, openSave: setSaveTarget, dialog };
};

/** The settings page a saved workflow list lives on, by tier. */
export const savedWorkflowSettingsPath = (
	tier: SaveTier,
	organizationName: string,
): string =>
	// Personal workflows are organization scoped too, so the personal link
	// carries the organization as well and the settings page selects it.
	`/${tier === "user" ? "agents" : "ai"}/settings/workflows?org=${encodeURIComponent(organizationName)}`;
