import { describe, expect, it } from "vitest";
import { forkToolRenderers } from "./forkToolRenderers";
import { toolRenderers } from "./Tool";

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

	it("is the only source of fork_ prefixed names in toolRenderers", () => {
		const mergedForkNames = Object.keys(toolRenderers)
			.filter((name) => name.startsWith(forkNamePrefix))
			.sort();
		expect(mergedForkNames).toEqual(Object.keys(forkToolRenderers).sort());
	});
});
