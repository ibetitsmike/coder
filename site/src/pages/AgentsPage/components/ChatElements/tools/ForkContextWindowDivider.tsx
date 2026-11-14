// This file is fork code: it renders the context window divider of the
// fork-token-budget experiment. A rollover's chat_summarized result keeps its
// summary and adds a context_window field, which replaces the summary card.

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

/**
 * Reads context_window from a result object or its JSON. The id is trimmed
 * and required; a trigger that is not a string becomes "".
 */
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

/** A trigger it does not know adds no reason to the text. */
export const forkContextWindowLabel = ({
	id,
	trigger,
}: ForkContextWindow): string => {
	const reason = triggerReason(trigger);
	return reason
		? `Context window ${id} started ${reason}`
		: `Context window ${id} started`;
};

type ForkContextWindowDividerProps = {
	contextWindow: ForkContextWindow;
};

const ForkContextWindowDivider: React.FC<ForkContextWindowDividerProps> = ({
	contextWindow,
}) => (
	<div className="flex w-full items-center gap-3 py-1 text-xs text-content-secondary">
		<Separator className="min-w-4 flex-1" />
		<span className="min-w-0 text-center">
			{forkContextWindowLabel(contextWindow)}
		</span>
		<Separator className="min-w-4 flex-1" />
	</div>
);

/** Upstream is a parameter, not an import, to avoid an import cycle. */
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
