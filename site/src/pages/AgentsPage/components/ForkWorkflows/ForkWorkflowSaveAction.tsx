// This file is fork code: the Save action of a workflow run
// (fork-workflows experiment). It puts the Save button next to the run's
// other controls and turns into a link to the saved workflow once saved.

import { BookmarkCheckIcon } from "lucide-react";
import type React from "react";
import { useContext, useEffect, useRef } from "react";
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
	const savedTextRef = useRef<HTMLSpanElement>(null);
	const { saved, openSave, dialog } = useForkWorkflowSave({
		organizationId,
		savedLinkRef,
	});
	const organizationName = organizations.find(
		(organization) => organization.id === organizationId,
	)?.name;
	// The plain-text saved state takes focus after the dialog closes, the
	// same move the link branch makes through the dialog's savedLinkRef.
	useEffect(() => {
		if (saved && !organizationName && !dialog) {
			savedTextRef.current?.focus();
		}
	}, [saved, organizationName, dialog]);
	if (saved) {
		if (!organizationName) {
			// Without the organization's name the link could not select it,
			// so the saved state renders as plain text with focus, not as a
			// link to a wrong or empty ?org= parameter and not as nothing.
			return (
				<>
					{dialog}
					<span
						ref={savedTextRef}
						tabIndex={-1}
						className="inline-flex min-w-0 shrink items-center gap-1.5 text-sm text-content-secondary"
					>
						<BookmarkCheckIcon className="size-4 shrink-0" />
						Saved {saved.name}
					</span>
				</>
			);
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
