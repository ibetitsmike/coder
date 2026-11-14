import type { ChatFilePart, ChatMessage } from "#/api/typesGenerated";
import type { MessageAttachment } from "#/pages/AgentsPage/components/ChatConversation/messageHelpers";
import { getChatFileURL } from "#/pages/AgentsPage/utils/chatAttachments";
import {
	MockChatAttachment,
	MockChatFileMetadata,
	MockChatMessage,
} from "./chatEntities";

/** An MP4 the agent attached with attach_file, such as a review video. */
export const MockChatVideoFilePart: ChatFilePart & { file_id: string } = {
	...MockChatAttachment.file,
	file_id: MockChatFileMetadata.id,
	media_type: "video/mp4",
	name: "review.mp4",
};

/** {@link MockChatVideoFilePart} with its chat file URL. */
export const MockChatVideoAttachment: MessageAttachment = {
	key: "1:0",
	role: "assistant",
	file: MockChatVideoFilePart,
	href: getChatFileURL(MockChatVideoFilePart.file_id),
};

/** An HTML page the agent attached with attach_file. The server stores it as text/plain. */
export const MockChatHtmlFilePart: ChatFilePart & {
	file_id: string;
	name: string;
} = {
	...MockChatAttachment.file,
	file_id: "chat-file-html-2",
	media_type: "text/plain",
	name: "choices.html",
};

/** An earlier page with the name of {@link MockChatHtmlFilePart}. */
export const MockChatReplacedHtmlFilePart: ChatFilePart & {
	file_id: string;
	name: string;
} = {
	...MockChatHtmlFilePart,
	file_id: "chat-file-html-1",
};

/** Two assistant messages that attach the same page name, oldest first. */
export const MockChatHtmlMessages: readonly { message: ChatMessage }[] = [
	{
		message: {
			...MockChatMessage,
			id: 1,
			role: "assistant",
			content: [MockChatReplacedHtmlFilePart],
		},
	},
	{
		message: {
			...MockChatMessage,
			id: 2,
			role: "assistant",
			content: [MockChatHtmlFilePart],
		},
	},
];

/** {@link MockChatHtmlFilePart} with its chat file URL. */
export const MockChatHtmlAttachment: MessageAttachment = {
	key: "2:0",
	role: "assistant",
	file: MockChatHtmlFilePart,
	href: getChatFileURL(MockChatHtmlFilePart.file_id),
};
