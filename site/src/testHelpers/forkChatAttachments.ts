import type { ChatFilePart } from "#/api/typesGenerated";
import { MockChatAttachment, MockChatFileMetadata } from "./chatEntities";

/** An MP4 the agent attached with attach_file, such as a review video. */
export const MockChatVideoFilePart: ChatFilePart & { file_id: string } = {
	...MockChatAttachment.file,
	file_id: MockChatFileMetadata.id,
	media_type: "video/mp4",
	name: "review.mp4",
};
