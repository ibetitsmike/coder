import { describe, expect, it, vi } from "vitest";
import { createForkToolRenderers } from "./forkToolRenderers";
import { isSubagentToolName } from "./subagentDescriptor";

describe("createForkToolRenderers", () => {
	// Tool.tsx spreads the fork renderers last and dispatches subagent names
	// before the map, so a colliding name would replace an upstream row or
	// never render.
	it("uses no upstream tool name", async () => {
		vi.resetModules();
		vi.doMock("./forkToolRenderers", () => ({
			createForkToolRenderers: () => ({}),
		}));
		const { toolRenderers } = await import("./Tool");
		vi.doUnmock("./forkToolRenderers");

		const forkToolNames = Object.keys(createForkToolRenderers(() => null));
		const collisions = forkToolNames.filter(
			(name) => name in toolRenderers || isSubagentToolName(name),
		);
		expect(forkToolNames).not.toEqual([]);
		expect(collisions).toEqual([]);
	});
});
