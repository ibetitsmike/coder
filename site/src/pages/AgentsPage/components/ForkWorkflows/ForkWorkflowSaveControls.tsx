// This file is fork code: the Save control of a workflow run
// (fork-workflows experiment), shared by the live tool card and the
// Workflows panel.

import { BookmarkCheckIcon, BookmarkPlusIcon } from "lucide-react";
import type React from "react";
import { Button } from "#/components/Button/Button";

/** The button that opens the run's save dialog. */
export const ForkWorkflowSaveButton: React.FC<{
	onClick: () => void;
}> = ({ onClick }) => (
	<Button variant="outline" size="sm" onClick={onClick}>
		<BookmarkPlusIcon />
		Save workflow
	</Button>
);

/**
 * The link that replaces the save button once a save succeeds. It takes
 * the button's place and receives its focus after the dialog closes.
 * Long names shrink with the control and truncate visually while the
 * accessible name keeps the full name.
 */
export const ForkWorkflowSavedLink: React.FC<{
	name: string;
	href: string;
	ref?: React.Ref<HTMLAnchorElement>;
}> = ({ name, href, ref }) => (
	<Button
		variant="outline"
		size="sm"
		asChild
		className="max-w-full min-w-0 shrink"
	>
		<a href={href} ref={ref} className="min-w-0">
			<BookmarkCheckIcon className="shrink-0" />
			<span className="truncate">Saved {name}</span>
		</a>
	</Button>
);
