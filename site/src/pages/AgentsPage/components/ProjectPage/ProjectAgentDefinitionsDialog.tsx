// This file is fork code: the fork-agent-definitions experiment's project
// agent definitions dialog.

import type { ChatProject } from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogFooter,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { ForkAgentDefinitionsTable } from "../../AgentSettingsPage/ForkAgentDefinitionsPage/ForkAgentDefinitionsTable";
import { fullSkillAccess, readOnlySkillAccess } from "../../utils/skills";

type ProjectAgentDefinitionsDialogProps = {
	readonly project: ChatProject;
	readonly open: boolean;
	readonly onOpenChange: (open: boolean) => void;
	readonly canEdit: boolean;
	readonly onCloseAutoFocus: (event: Event) => void;
};

/** Manages a project's agent definitions, read-only without canEdit; onCloseAutoFocus restores focus. */
export const ProjectAgentDefinitionsDialog: React.FC<
	ProjectAgentDefinitionsDialogProps
> = ({ project, open, onOpenChange, canEdit, onCloseAutoFocus }) => (
	<Dialog open={open} onOpenChange={onOpenChange}>
		<DialogContent
			className="flex max-w-2xl flex-col gap-0 overflow-hidden p-0"
			aria-describedby={undefined}
			onCloseAutoFocus={onCloseAutoFocus}
		>
			{/* The table's own heading and description label the content. */}
			<DialogTitle className="sr-only">Project agent definitions</DialogTitle>
			<div className="min-h-0 flex-1 overflow-y-auto p-6">
				<ForkAgentDefinitionsTable
					scope={{
						tier: "project",
						organizationId: project.organization_id,
						projectId: project.id,
					}}
					access={canEdit ? fullSkillAccess : readOnlySkillAccess}
				/>
			</div>
			<DialogFooter className="border-t border-border px-6 py-4">
				<Button variant="outline" onClick={() => onOpenChange(false)}>
					Close
				</Button>
			</DialogFooter>
		</DialogContent>
	</Dialog>
);
