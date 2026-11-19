// This file is fork code: unit tests for the lossless integer parsing of
// the run detail response and for how the display layer stringifies the
// wrapped values. The parse path matters because JSON.parse rounds any
// integer outside the safe range before the reviver sees the value, and
// the display path matters because a wrapped value must print as bare
// digits, not as a quoted string a real string value would also print.

import { describe, expect, it } from "vitest";
import { formatForkWorkflowValue } from "#/pages/AgentsPage/components/ForkWorkflows/forkWorkflows";
import {
	forkWorkflowReviver,
	isUnsafeInteger,
	unsafeIntegerText,
} from "./forkWorkflows";

// Production parses through axios's parseReviver, which is
// forkWorkflowReviver itself, so the tests call JSON.parse with it the
// same way.
const parse = (text: string): unknown => JSON.parse(text, forkWorkflowReviver);

describe("the lossless JSON parse", () => {
	it("keeps an unsafe integer exactly as the server sent it", () => {
		const parsed = parse('{"args":{"count":9007199254740993}}');
		const value = (parsed as { args: { count: unknown } }).args.count;
		expect(isUnsafeInteger(value)).toBe(true);
		expect(value).toEqual({ forkUnsafeInteger: "9007199254740993" });
	});

	it("wraps a negative integer outside the safe range", () => {
		const parsed = parse('{"n":-9007199254740993}');
		expect((parsed as { n: unknown }).n).toEqual({
			forkUnsafeInteger: "-9007199254740993",
		});
	});

	it("keeps an integer just inside the safe range a plain number", () => {
		const parsed = parse('{"n":9007199254740991}');
		expect((parsed as { n: unknown }).n).toBe(9007199254740991);
	});

	it("falls back to the rounded number when the source text is missing", () => {
		// A runtime without the literal-source feature hands the reviver no
		// context, so the value the parser already rounded stays.
		expect(forkWorkflowReviver("", 9007199254740992, undefined)).toBe(
			9007199254740992,
		);
		expect(forkWorkflowReviver("", 9007199254740992, {})).toBe(
			9007199254740992,
		);
	});

	it("does not wrap floats, strings or decimal sources", () => {
		// 9007199254740993.5 rounds to an unsafe integer, but its source is
		// a decimal literal, so it stays a plain rounded number.
		const parsed = parse('{"f":9007199254740993.5,"s":"9007199254740993"}');
		const body = parsed as { f: unknown; s: unknown };
		expect(body.f).toBe(9007199254740994);
		expect(body.s).toBe("9007199254740993");
	});

	it("wraps unsafe integers nested in objects and arrays", () => {
		const parsed = parse('{"steps":[{"value":[9007199254740993]}]}');
		const step = (parsed as { steps: { value: unknown[] }[] }).steps[0]
			.value[0];
		expect(step).toEqual({ forkUnsafeInteger: "9007199254740993" });
	});

	it("wraps an unsafe integer used as an object key's value only", () => {
		const parsed = parse(
			'{"result":{"output":{"total":18446744073709551615}}}',
		);
		const total = (parsed as { result: { output: { total: unknown } } }).result
			.output.total;
		expect(total).toEqual({ forkUnsafeInteger: "18446744073709551615" });
	});

	it("wraps an integer literal that overflows the number format", () => {
		// JSON.parse hands the reviver Infinity for a literal this large,
		// which Number.isSafeInteger rejects, and the source text still
		// holds the exact digits. A literal that merely rounds to a finite
		// unsafe integer (9007199254740993) is a separate case above.
		const digits = `1${"0".repeat(400)}`;
		const parsed = parse(`{"n":${digits}}`);
		expect((parsed as { n: unknown }).n).toEqual({
			forkUnsafeInteger: digits,
		});
		// The digits show as bare JSON, like any other wrapped integer.
		expect(
			JSON.stringify({
				v: unsafeIntegerText((parsed as { n: unknown }).n as never),
			}),
		).toBe(`{"v":${digits}}`);
	});
});

describe("unsafeIntegerText", () => {
	it("stringifies as bare digits, distinct from a quoted string", () => {
		const wrapped = unsafeIntegerText({
			forkUnsafeInteger: "9007199254740993",
		});
		expect(JSON.stringify({ v: wrapped })).toBe('{"v":9007199254740993}');
	});
});

describe("forkWorkflowReviver as axios parseReviver", () => {
	it("parses a whole body through the reviver the way axios calls it", () => {
		// axios hands the reviver straight to JSON.parse, so the first
		// value it visits is a property value, not a document.
		const parsed = JSON.parse(
			'{"id":"run-1","args":{"count":9007199254740993},"steps":[{"value":[18446744073709551615]}]}',
			forkWorkflowReviver,
		);
		const body = parsed as {
			id: string;
			args: { count: unknown };
			steps: { value: unknown[] }[];
		};
		expect(body.id).toBe("run-1");
		expect(body.args.count).toEqual({ forkUnsafeInteger: "9007199254740993" });
		expect(body.steps[0].value[0]).toEqual({
			forkUnsafeInteger: "18446744073709551615",
		});
	});
});

describe("formatForkWorkflowValue", () => {
	it("prints an unsafe integer as bare digits", () => {
		const text = formatForkWorkflowValue({
			count: { forkUnsafeInteger: "9007199254740993" },
		});
		expect(text).toContain('"count": 9007199254740993');
	});

	it("keeps a real string value quoted", () => {
		const text = formatForkWorkflowValue({
			count: "9007199254740993",
		});
		expect(text).toContain('"count": "9007199254740993"');
	});

	it("prints a plain safe number as a number", () => {
		const text = formatForkWorkflowValue({ count: 42 });
		expect(text).toContain('"count": 42');
	});
});
