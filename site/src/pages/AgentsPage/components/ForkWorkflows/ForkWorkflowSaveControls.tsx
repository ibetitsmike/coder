// This file is fork code: the Save control of a workflow run
// (fork-workflows experiment), shared by the live tool card and the
// Workflows panel.

import { BookmarkCheckIcon, BookmarkPlusIcon } from "lucide-react";
import type React from "react";
import { Button } from "#/components/Button/Button";

export const ForkWorkflowSaveButton: React.FC<{
	onClick: () => void;
}> = ({ onClick }) => (
	<Button variant="outline" size="sm" onClick={onClick}>
		<BookmarkPlusIcon />
		Save workflow
	</Button>
);

export const ForkWorkflowSavedLink: React.FC<{
	name: string;
	href: string;
	ref?: React.Ref<HTMLAnchorElement>;
}> = ({ name, href, ref }) => (
	<Button variant="outline" size="sm" asChild>
		<a href={href} ref={ref}>
			<BookmarkCheckIcon />
			Saved {name}
		</a>
	</Button>
);
