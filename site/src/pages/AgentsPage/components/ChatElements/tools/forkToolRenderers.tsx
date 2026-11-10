// This file is fork code: it is the only place fork features add tool
// renderers, so an upstream sync of Tool.tsx stays clean.

import type React from "react";
import type { Experiment } from "#/api/typesGenerated";
import { ForkChatTranscriptToolRenderer } from "./ForkChatTranscriptTool";
import { ForkNotifyUserToolRenderer } from "./ForkNotifyUserTool";
import { ForkScheduleWakeupToolRenderer } from "./ForkScheduleWakeupTool";
import { ForkSearchChatsToolRenderer } from "./ForkSearchChatsTool";
import { ForkTodoToolRenderer } from "./ForkTodoTool";
import { ForkWaitForEventsToolRenderer } from "./ForkWaitForEventsTool";
import { useForkExperiment } from "./forkToolUtils";
import type { toolRenderers } from "./Tool";

type ToolRendererProps = React.ComponentProps<(typeof toolRenderers)[string]>;

type ToolRenderer = React.FC<ToolRendererProps>;

/** A renderer shows fallback for output it cannot parse. */
export type ForkToolRendererProps = ToolRendererProps & {
	fallback: ToolRenderer;
};

const gate = (
	experiment: Experiment,
	Renderer: React.FC<ForkToolRendererProps>,
	Fallback: ToolRenderer,
): ToolRenderer => {
	const GatedRenderer: ToolRenderer = (props) => {
		if (!useForkExperiment(experiment)) {
			return <Fallback {...props} />;
		}
		return <Renderer {...props} fallback={Fallback} />;
	};
	return GatedRenderer;
};

/**
 * A name must not match an upstream renderer: Tool.tsx spreads these last,
 * so a match would silently replace it. fallback is a parameter, not an
 * import, so this file avoids a runtime import cycle with Tool.tsx.
 */
export const createForkToolRenderers = (
	fallback: ToolRenderer,
): Record<string, ToolRenderer> => {
	const todoRenderer = gate("fork-chat-todos", ForkTodoToolRenderer, fallback);
	return {
		todo_write: todoRenderer,
		todo_read: todoRenderer,
		search_chats: gate(
			"fork-chat-todos",
			ForkSearchChatsToolRenderer,
			fallback,
		),
		read_chat_transcript: gate(
			"fork-chat-todos",
			ForkChatTranscriptToolRenderer,
			fallback,
		),
		schedule_wakeup: gate(
			"fork-chat-wakeups",
			ForkScheduleWakeupToolRenderer,
			fallback,
		),
		wait_for_events: gate(
			"fork-chat-wakeups",
			ForkWaitForEventsToolRenderer,
			fallback,
		),
		notify_user: gate(
			"fork-chat-wakeups",
			ForkNotifyUserToolRenderer,
			fallback,
		),
	};
};
