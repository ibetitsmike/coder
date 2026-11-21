// This file is fork code: the Save action of a workflow run
// (fork-workflows experiment). It puts the Save button next to the run's
// other controls and turns into a link to the saved workflow once saved.

import type React from "react";
import { useContext, useRef } from "react";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { useForkExperiment } from "../ChatElements/tools/forkToolUtils";
import {
	ForkWorkflowSaveButton,
	ForkWorkflowSavedLink,
} from "./ForkWorkflowSaveControls";
import {
	savedWorkflowSettingsPath,
	useForkWorkflowSave,
} from "./useForkWorkflowSave";

type ForkWorkflowSaveActionProps = {
	/** The run title, which the panel's dialog prefills the name from. */
	title: string;
	/** The run's script, saved as the workflow body. */
	script: string;
	/** The organization the chat belongs to, which both tiers save into. */
	organizationId: string | undefined;
};

/**
 * The Save action of one run. It renders nothing without the experiment,
 * a script, or a known organization, so a run read outside its chat keeps
 * no save path.
 */
export const ForkWorkflowSaveAction: React.FC<ForkWorkflowSaveActionProps> = ({
	title,
	script,
	organizationId,
}) => {
	const isEnabled = useForkExperiment("fork-workflows");
	const dashboard = useContext(DashboardContext);
	if (!isEnabled || script.trim() === "" || !organizationId || !dashboard) {
		return null;
	}
	return (
		<ForkWorkflowSaveActionInner
			title={title}
			script={script}
			organizationId={organizationId}
			organizations={dashboard.organizations}
		/>
	);
};

type ForkWorkflowSaveActionInnerProps = {
	title: string;
	script: string;
	organizationId: string;
	organizations: readonly { id: string; name: string }[];
};

const ForkWorkflowSaveActionInner: React.FC<
	ForkWorkflowSaveActionInnerProps
> = ({ title, script, organizationId, organizations }) => {
	const savedLinkRef = useRef<HTMLAnchorElement>(null);
	const { saved, openSave, dialog } = useForkWorkflowSave({
		organizationId,
		savedLinkRef,
	});
	const organizationName = organizations.find(
		(organization) => organization.id === organizationId,
	)?.name;
	if (saved) {
		// Without the organization's name the link could not select it, so
		// it renders as plain text rather than a link to a wrong or empty
		// ?org= parameter.
		if (!organizationName) {
			return dialog;
		}
		return (
			<>
				{dialog}
				<ForkWorkflowSavedLink
					ref={savedLinkRef}
					name={saved.name}
					href={savedWorkflowSettingsPath(saved.tier, organizationName)}
				/>
			</>
		);
	}
	return (
		<>
			{dialog}
			<ForkWorkflowSaveButton onClick={() => openSave({ title, script })} />
		</>
	);
};
