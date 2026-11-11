// This file is fork code: the fork-plugin-packs experiment's project plugin
// packs dialog.

import type { ChatProject } from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { ForkPluginPackToggles } from "../ForkPluginPackToggles";

type ForkProjectPluginPacksDialogProps = {
	readonly project: ChatProject;
	readonly open: boolean;
	readonly onOpenChange: (open: boolean) => void;
	readonly onCloseAutoFocus: (event: Event) => void;
};

export const ForkProjectPluginPacksDialog: React.FC<
	ForkProjectPluginPacksDialogProps
> = ({ project, open, onOpenChange, onCloseAutoFocus }) => (
	<Dialog open={open} onOpenChange={onOpenChange}>
		<DialogContent onCloseAutoFocus={onCloseAutoFocus}>
			<DialogHeader>
				<DialogTitle>Project plugin packs</DialogTitle>
				<DialogDescription>
					Every chat in this project gets the skills and agents of the packs
					enabled here.
				</DialogDescription>
			</DialogHeader>
			{open && (
				<ForkPluginPackToggles
					target={{
						kind: "project",
						organizationId: project.organization_id,
						projectId: project.id,
					}}
					organizationId={project.organization_id}
					canEdit={project.permissions.update}
				/>
			)}
			<DialogFooter>
				<Button variant="outline" onClick={() => onOpenChange(false)}>
					Close
				</Button>
			</DialogFooter>
		</DialogContent>
	</Dialog>
);
