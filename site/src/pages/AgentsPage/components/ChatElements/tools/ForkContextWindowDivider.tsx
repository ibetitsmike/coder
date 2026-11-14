// This file is fork code: it renders the context window divider of the
// fork-token-budget experiment. A rollover records a chat_summarized result
// with a context_window field instead of a summary.

import type React from "react";
import { Separator } from "#/components/Separator/Separator";
import { useForkExperiment } from "./forkToolUtils";
import type { toolRenderers } from "./Tool";
import { asRecord, asString, parseArgs } from "./utils";

type ToolRenderer = (typeof toolRenderers)[string];

type ForkContextWindow = {
	id: string;
	trigger: string;
};

/** Returns the context window a chat_summarized result started, or null. */
export const parseForkContextWindow = (
	result: unknown,
): ForkContextWindow | null => {
	const record = asRecord(parseArgs(result)?.context_window);
	const id = asString(record?.id).trim();
	if (!id) {
		return null;
	}
	return { id, trigger: asString(record?.trigger) };
};

const triggerReason = (trigger: string): string | undefined => {
	switch (trigger) {
		case "new_context":
			return "at the agent's request";
		case "context_limit":
			return "at the context limit";
		default:
			return undefined;
	}
};

/** Returns the divider text. A trigger it does not know adds no reason. */
export const forkContextWindowLabel = ({
	id,
	trigger,
}: ForkContextWindow): string => {
	const reason = triggerReason(trigger);
	return reason
		? `Context window ${id} started ${reason}`
		: `Context window ${id} started`;
};

const ForkContextWindowDivider: React.FC<{
	contextWindow: ForkContextWindow;
}> = ({ contextWindow }) => (
	<div className="flex w-full items-center gap-3 py-1 text-xs text-content-secondary">
		<Separator className="flex-1" />
		<span className="shrink-0">{forkContextWindowLabel(contextWindow)}</span>
		<Separator className="flex-1" />
	</div>
);

/**
 * Wraps the upstream chat_summarized renderer. With fork-token-budget on, a
 * result that started a context window shows a divider instead of a summary.
 * Upstream is a parameter, not an import, to avoid an import cycle.
 */
export const withForkContextWindow = (Upstream: ToolRenderer): ToolRenderer => {
	const ContextWindowRenderer: ToolRenderer = (props) => {
		const enabled = useForkExperiment("fork-token-budget");
		const contextWindow = parseForkContextWindow(props.result);
		if (!enabled || props.isError || !contextWindow) {
			return <Upstream {...props} />;
		}
		return <ForkContextWindowDivider contextWindow={contextWindow} />;
	};
	return ContextWindowRenderer;
};
