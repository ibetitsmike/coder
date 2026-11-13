import type { ChatMessage } from "#/api/typesGenerated";
import { MockChatMessage } from "./chatEntities";

/** The chat that sent MockFamilyMessage with message_family_chat. */
export const MockFamilyMessageSenderId = "5f8b6a52-3c1e-4c6f-9d6e-2b7a1c0e4f11";

/** A message that a sibling chat delivered, as the recipient chat stores it. */
export const MockFamilyMessage: ChatMessage = {
	...MockChatMessage,
	role: "user",
	content: [
		{
			type: "text",
			text: `<agent-message from_chat_id="${MockFamilyMessageSenderId}" from_title="Review the API">\nThis message comes from another agent in your chat family, not from the user. If you have the message_family_chat tool, you can reply with it and chat_id "${MockFamilyMessageSenderId}".\n\nThe tests pass, so the **API review** is done.\n\nShould I open the PR?\n</agent-message>`,
		},
	],
};
