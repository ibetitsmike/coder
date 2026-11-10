// This file is fork code: it is the only place fork features add tool
// renderers, so an upstream sync of Tool.tsx stays clean.

import type React from "react";
import { useContext } from "react";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { ForkChatTranscriptToolRenderer } from "./ForkChatTranscriptTool";
import { ForkSearchChatsToolRenderer } from "./ForkSearchChatsTool";
import { ForkTodoToolRenderer } from "./ForkTodoTool";
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
		// Tool also renders outside DashboardProvider, as in upstream's tool
		// stories, where fork renderers stay off.
		const dashboard = useContext(DashboardContext);
		if (!dashboard?.experiments.includes(experiment)) {
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
	const searchChatsRenderer = gate(
		"fork-chat-todos",
		ForkSearchChatsToolRenderer,
		fallback,
	);
	const chatTranscriptRenderer = gate(
		"fork-chat-todos",
		ForkChatTranscriptToolRenderer,
		fallback,
	);
	return {
		todo_write: todoRenderer,
		todo_read: todoRenderer,
		search_chats: searchChatsRenderer,
		read_chat_transcript: chatTranscriptRenderer,
	};
};
