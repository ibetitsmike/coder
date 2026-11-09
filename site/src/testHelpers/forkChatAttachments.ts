import type { ChatFilePart } from "#/api/typesGenerated";
import { MockChatAttachment } from "./chatEntities";

/** An MP4 the agent attached with attach_file, such as a review video. */
export const MockChatVideoFilePart: ChatFilePart = {
	...MockChatAttachment.file,
	media_type: "video/mp4",
	name: "review.mp4",
};
