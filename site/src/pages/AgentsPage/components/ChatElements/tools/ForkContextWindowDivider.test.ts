import { describe, expect, it } from "vitest";
import {
	forkContextWindowLabel,
	parseForkContextWindow,
} from "./ForkContextWindowDivider";

describe("parseForkContextWindow", () => {
	it("reads the window of a result object", () => {
		expect(
			parseForkContextWindow({
				summary: "",
				context_window: { id: "w:2", trigger: "new_context" },
			}),
		).toEqual({ id: "w:2", trigger: "new_context" });
	});

	it("reads the window of a result in JSON", () => {
		expect(
			parseForkContextWindow(
				JSON.stringify({
					context_window: { id: "w:3", trigger: "context_limit" },
				}),
			),
		).toEqual({ id: "w:3", trigger: "context_limit" });
	});

	it("returns null for a summary without a window", () => {
		expect(parseForkContextWindow({ summary: "Earlier work." })).toBeNull();
		expect(parseForkContextWindow("Earlier work.")).toBeNull();
		expect(parseForkContextWindow(undefined)).toBeNull();
	});

	it("returns null for a window without an id", () => {
		expect(
			parseForkContextWindow({ context_window: { trigger: "new_context" } }),
		).toBeNull();
		expect(
			parseForkContextWindow({
				context_window: { id: "  ", trigger: "new_context" },
			}),
		).toBeNull();
		expect(parseForkContextWindow({ context_window: "w:2" })).toBeNull();
	});

	it("keeps a trigger it does not know", () => {
		expect(
			parseForkContextWindow({
				context_window: { id: "w:4", trigger: "other" },
			}),
		).toEqual({ id: "w:4", trigger: "other" });
	});

	it("drops a trigger that is not a string", () => {
		expect(
			parseForkContextWindow({ context_window: { id: "w:4", trigger: 7 } }),
		).toEqual({ id: "w:4", trigger: "" });
	});
});

describe("forkContextWindowLabel", () => {
	it("names the trigger of the window", () => {
		expect(forkContextWindowLabel({ id: "w:2", trigger: "new_context" })).toBe(
			"Context window w:2 started after a checkpoint",
		);
		expect(
			forkContextWindowLabel({ id: "w:3", trigger: "context_limit" }),
		).toBe("Context window w:3 started at the context limit");
	});

	it("omits a trigger it does not know", () => {
		expect(forkContextWindowLabel({ id: "w:4", trigger: "other" })).toBe(
			"Context window w:4 started",
		);
	});
});
