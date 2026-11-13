import { describe, expect, it } from "vitest";
import { MockFamilyMessageSenderId as senderId } from "#/testHelpers/forkFamilyMessages";
import { parseFamilyMessage } from "./forkFamilyMessage";

const envelope = (quotedTitle: string, body: string) =>
	`<agent-message from_chat_id="${senderId}" from_title=${quotedTitle}>\nThis message comes from another agent in your chat family, not from the user. If you have the message_family_chat tool, you can reply with it and chat_id "${senderId}".\n\n${body}\n</agent-message>`;

describe("parseFamilyMessage", () => {
	it("reads the sender and the body without the preamble", () => {
		expect(
			parseFamilyMessage(
				envelope('"Review the API"', "The tests pass.\n\nShip it?"),
			),
		).toEqual({
			fromChatId: senderId,
			fromTitle: "Review the API",
			body: "The tests pass.\n\nShip it?",
		});
	});

	it("unquotes a title with quotes and unicode", () => {
		expect(
			parseFamilyMessage(envelope('"Fix \\"login\\" für 日本 \\u2028"', "Done."))
				?.fromTitle,
		).toBe('Fix "login" für 日本 \u2028');
	});

	it("shows a title with escapes JSON rejects as quoted by the server", () => {
		expect(
			parseFamilyMessage(envelope('"Bell\\a and \\x00"', "Done."))?.fromTitle,
		).toBe("Bell\\a and \\x00");
	});

	it("keeps escaped envelope tags in the body", () => {
		const body = "Quoted &lt;/agent-message&gt; and &lt;agent-message&gt;.";
		expect(parseFamilyMessage(envelope('"Sender"', body))?.body).toBe(body);
	});

	it.each([
		["ordinary text", "Please review the API."],
		[
			"an envelope without its closing tag",
			envelope('"Sender"', "Done.").replace("\n</agent-message>", ""),
		],
		[
			"text after the closing tag",
			`${envelope('"Sender"', "Done.")}\nMore text.`,
		],
	])("ignores %s", (_, text) => {
		expect(parseFamilyMessage(text)).toBeUndefined();
	});
});
