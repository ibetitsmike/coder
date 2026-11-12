import { describe, expect, it } from "vitest";
import { isNewer } from "./forkListCache";

describe("isNewer", () => {
	it.each([
		[
			"microseconds",
			"2030-01-01T00:00:00.497900Z",
			"2030-01-01T00:00:00.497450Z",
		],
		[
			"trimmed zeros",
			"2030-01-01T00:00:00.49745Z",
			"2030-01-01T00:00:00.4974Z",
		],
		["no fraction", "2030-01-01T00:00:01Z", "2030-01-01T00:00:00.999999Z"],
		["offsets", "2030-01-01T01:00:00.5+01:00", "2030-01-01T00:00:00.4Z"],
	])("orders timestamps with %s", (_case, later, earlier) => {
		expect(isNewer({ updated_at: later }, { updated_at: earlier })).toBe(true);
		expect(isNewer({ updated_at: earlier }, { updated_at: later })).toBe(false);
		expect(isNewer({ updated_at: later }, { updated_at: later })).toBe(false);
	});
});
