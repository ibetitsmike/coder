// This file is fork code: it reads the envelope that message_family_chat, of
// the fork-family-messages experiment, wraps around a message it delivers.

/** A message that another chat in the family sent with message_family_chat. */
export type FamilyMessage = {
	fromChatId: string;
	/** "Untitled" when the sender chat has no title. */
	fromTitle: string;
	body: string;
};

// The server escapes agent-message tags in the title and the body, so only
// the closing tag at the end of the text can end the envelope.
const familyMessagePattern =
	/^<agent-message from_chat_id="([^"\n]+)" from_title=("(?:[^"\\\n]|\\.)*")>\n[\s\S]*?\n\n([\s\S]*)\n<\/agent-message>$/;

// The server quotes the title with Go's strconv.Quote, which can emit escapes
// such as \x00 and \a that JSON rejects.
const parseQuotedTitle = (quoted: string): string => {
	const unquoted = quoted.slice(1, -1);
	try {
		const title: unknown = JSON.parse(quoted);
		return typeof title === "string" ? title : unquoted;
	} catch {
		return unquoted;
	}
};

// Mirrors escapeAgentMessageTags on the server, which runs before the title is
// quoted. The header shows the title as plain text, unlike the Markdown body.
const escapedAgentMessageTagPattern =
	/&lt;(\s*\/?\s*agent-message\b[^>]*?)&gt;/gi;

const unescapeAgentMessageTags = (text: string): string =>
	text.replace(escapedAgentMessageTagPattern, "<$1>");

/** Returns undefined unless the text is a whole family message envelope. */
export const parseFamilyMessage = (text: string): FamilyMessage | undefined => {
	const match = familyMessagePattern.exec(text);
	if (!match) {
		return undefined;
	}
	return {
		fromChatId: match[1],
		fromTitle:
			unescapeAgentMessageTags(parseQuotedTitle(match[2])) || "Untitled",
		body: match[3],
	};
};
