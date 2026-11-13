export const MockMessageFamilyChatArgs = {
	chat_id: "22222222-2222-4222-8222-222222222222",
	message: "The migration landed on main.\nRebase your branch before the next push.",
};

export const MockMessageFamilyChatResult = {
	chat_id: MockMessageFamilyChatArgs.chat_id,
	status: "waiting",
	title: "Migrate chat sidebar to Tailwind",
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
	truncated: true,
};
