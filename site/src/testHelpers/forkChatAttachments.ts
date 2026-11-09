import type { ChatFilePart } from "#/api/typesGenerated";
import type { MessageAttachment } from "#/pages/AgentsPage/components/ChatConversation/messageHelpers";
import { getChatFileURL } from "#/pages/AgentsPage/utils/chatAttachments";
import { MockChatAttachment, MockChatFileMetadata } from "./chatEntities";

/** An MP4 the agent attached with attach_file, such as a review video. */
export const MockChatVideoFilePart: ChatFilePart & { file_id: string } = {
	...MockChatAttachment.file,
	file_id: MockChatFileMetadata.id,
	media_type: "video/mp4",
	name: "review.mp4",
};

/** The MP4 above as an entry in its turn's Attachments menu. */
export const MockChatVideoAttachment: MessageAttachment = {
	key: "1:0",
	role: "assistant",
	file: MockChatVideoFilePart,
	href: getChatFileURL(MockChatVideoFilePart.file_id),
};
