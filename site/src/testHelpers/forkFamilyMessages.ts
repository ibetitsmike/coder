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

export const MockMessageFamilyChatArgs = {
	chat_id: "22222222-2222-4222-8222-222222222222",
	message:
		"The migration landed on main.\nRebase your branch before the next push.",
};

export const MockMessageFamilyChatResult = {
	chat_id: MockMessageFamilyChatArgs.chat_id,
	status: "waiting",
	title: "Migrate chat sidebar to Tailwind",
};

export const MockQueuedMessageFamilyChatResult = {
	...MockMessageFamilyChatResult,
	queued: true,
};

export const MockListFamilyChatsResult = {
	chats: [
		{
			chat_id: "11111111-1111-4111-8111-111111111111",
			relation: "parent",
			status: "running",
			title: "Ship family messages",
		},
		{
			chat_id: "22222222-2222-4222-8222-222222222222",
			parent_chat_id: "11111111-1111-4111-8111-111111111111",
			relation: "sibling",
			status: "waiting",
			title: "Migrate chat sidebar to Tailwind",
		},
		{
			chat_id: "33333333-3333-4333-8333-333333333333",
			parent_chat_id: "11111111-1111-4111-8111-111111111111",
			relation: "self",
			status: "running",
			title: "Write the tool cards",
		},
	],
};

export const MockTruncatedListFamilyChatsResult = {
	...MockListFamilyChatsResult,
	truncated: true,
};
