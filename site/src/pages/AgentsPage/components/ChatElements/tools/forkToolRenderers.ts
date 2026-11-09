// This file is fork code: it is the only place fork features add tool
// renderers, so an upstream sync of Tool.tsx stays clean. Tool.tsx spreads
// forkToolRenderers into toolRenderers last, so fork names must stay inside the
// reserved "fork_" prefix, which forkToolRenderers.test.ts enforces.

import type React from "react";
import type { ToolRendererProps } from "./Tool";

export const forkToolRenderers: Record<
	string,
	React.FC<ToolRendererProps>
> = {};
