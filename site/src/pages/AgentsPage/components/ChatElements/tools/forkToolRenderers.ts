// This file is fork code: it is the only place fork features add tool
// renderers, so an upstream sync of Tool.tsx stays clean.

import type React from "react";
import type { toolRenderers } from "./Tool";

type ToolRendererProps = React.ComponentProps<(typeof toolRenderers)[string]>;

/**
 * Tool renderers added by fork features. Tool.tsx spreads this map into
 * toolRenderers last, so every name must use the reserved "fork_" prefix,
 * which forkToolRenderers.test.ts enforces.
 */
export const forkToolRenderers: Record<
	string,
	React.FC<ToolRendererProps>
> = {};
