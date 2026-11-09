import { describe, expect, it, vi } from "vitest";
import { forkToolRenderers } from "./forkToolRenderers";

// Tool.tsx spreads forkToolRenderers last, so a fork name that matches an
// upstream name would take over that tool row.
const forkNamePrefix = "fork_";

describe("forkToolRenderers", () => {
	it("registers only names in the reserved fork_ namespace", () => {
		const outsideNamespace = Object.keys(forkToolRenderers).filter(
			(name) => !name.startsWith(forkNamePrefix),
		);
		expect(outsideNamespace).toEqual([]);
	});

	it("leaves the fork_ namespace unused by upstream renderers", async () => {
		vi.resetModules();
		vi.doMock("./forkToolRenderers", () => ({ forkToolRenderers: {} }));
		const { toolRenderers } = await import("./Tool");
		vi.doUnmock("./forkToolRenderers");

		const upstreamForkNames = Object.keys(toolRenderers).filter((name) =>
			name.startsWith(forkNamePrefix),
		);
		expect(upstreamForkNames).toEqual([]);
	});
});
