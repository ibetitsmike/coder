// This file is fork code: the status badge of Starlark workflow runs and
// steps (fork-workflows experiment).

import { LoaderIcon } from "lucide-react";
import type React from "react";
import { Badge, type BadgeProps } from "#/components/Badge/Badge";
import type { ForkWorkflowTone } from "./forkWorkflows";

const toneVariant: Record<ForkWorkflowTone, BadgeProps["variant"]> = {
	running: "info",
	success: "green",
	warning: "warning",
	destructive: "destructive",
	muted: "default",
};

type ForkWorkflowStatusBadgeProps = {
	label: string;
	tone: ForkWorkflowTone;
};

export const ForkWorkflowStatusBadge: React.FC<
	ForkWorkflowStatusBadgeProps
> = ({ label, tone }) => (
	// A span, because the badge also sits inside tool-call header buttons.
	<Badge asChild size="sm" variant={toneVariant[tone]} className="shrink-0">
		<span>
			{tone === "running" && (
				<LoaderIcon
					aria-hidden
					className="animate-spin motion-reduce:animate-none"
				/>
			)}
			{label}
		</span>
	</Badge>
);
