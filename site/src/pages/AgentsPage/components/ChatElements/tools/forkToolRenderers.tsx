// This file is fork code: it is the only place fork features add tool
// renderers, so an upstream sync of Tool.tsx stays clean.

import type React from "react";
import { useContext } from "react";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { ForkTodoToolRenderer } from "./ForkTodoTool";
import type { toolRenderers } from "./Tool";

type ToolRendererProps = React.ComponentProps<(typeof toolRenderers)[string]>;

type ToolRenderer = React.FC<ToolRendererProps>;

/**
 * Props of a fork tool renderer. A renderer shows fallback for output it
 * cannot parse.
 */
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
 * Returns the tool renderers added by fork features. Tool.tsx spreads them
 * into toolRenderers last, so a name must not match an upstream renderer,
 * which forkToolRenderers.test.tsx enforces. While a renderer's experiment
 * is off, fallback renders the tool, so the row matches upstream. Tool.tsx
 * passes its generic renderer as fallback, which keeps this file free of a
 * runtime import of Tool.tsx.
 */
export const createForkToolRenderers = (
	fallback: ToolRenderer,
): Record<string, ToolRenderer> => {
	const todoRenderer = gate("fork-chat-todos", ForkTodoToolRenderer, fallback);
	return {
		todo_write: todoRenderer,
		todo_read: todoRenderer,
	};
};
