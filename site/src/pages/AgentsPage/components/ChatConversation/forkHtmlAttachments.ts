// This file is fork code: the helpers of the fork-html-attachments viewer.

import type * as TypesGen from "#/api/typesGenerated";
import type { ParsedMessageEntry, RenderBlock } from "./types";

/**
 * Where the viewer loads a page from. The route serves it as text/html with a
 * policy that sandboxes it and blocks network access.
 */
export const getForkHtmlAttachmentURL = (fileId: string) =>
	`/api/v2/chats/fork/files/${encodeURIComponent(fileId)}/html`;

/**
 * Reports whether a file part is a stored HTML page. The server stores HTML
 * as text/plain, so the name decides, as it does on the server.
 */
export const isForkHtmlAttachment = (
	block: TypesGen.ChatFilePart,
): block is TypesGen.ChatFilePart & { file_id: string; name: string } =>
	Boolean(block.file_id) &&
	/\.html?$/i.test(block.name ?? "") &&
	(block.media_type === "text/plain" || block.media_type === "text/html");

/**
 * Maps each HTML attachment name to the file ID of its newest attachment by
 * the agent, so an earlier page with the same name gives way to it. Pages in
 * user messages are uploads, not agent pages, and stay out. The live blocks
 * of the streaming turn come last, because they are the newest.
 */
export const latestForkHtmlAttachmentIds = (
	entries: readonly Pick<ParsedMessageEntry, "message">[],
	liveBlocks: readonly RenderBlock[] = [],
): ReadonlyMap<string, string> => {
	const latest = new Map<string, string>();
	const add = (part: RenderBlock | TypesGen.ChatMessagePart) => {
		if (part.type === "file" && isForkHtmlAttachment(part)) {
			latest.set(part.name, part.file_id);
		}
	};
	for (const { message } of entries) {
		if (message.role === "assistant") {
			for (const part of message.content ?? []) {
				add(part);
			}
		}
	}
	for (const block of liveBlocks) {
		add(block);
	}
	return latest;
};
