// This file is fork code: the helpers of the fork-html-attachments viewer.

import type * as TypesGen from "#/api/typesGenerated";
import type { ParsedMessageEntry } from "./types";

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
 * Maps each HTML attachment name to the file ID of its newest attachment in
 * the conversation, so an earlier page with the same name gives way to it.
 */
export const latestForkHtmlAttachmentIds = (
	entries: readonly Pick<ParsedMessageEntry, "message">[],
): ReadonlyMap<string, string> => {
	const latest = new Map<string, string>();
	for (const { message } of entries) {
		for (const part of message.content ?? []) {
			if (part.type === "file" && isForkHtmlAttachment(part)) {
				latest.set(part.name, part.file_id);
			}
		}
	}
	return latest;
};
