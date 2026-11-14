// This file is fork code: the sandboxed viewer of the fork-html-attachments
// experiment.

import { DownloadIcon } from "lucide-react";
import type React from "react";
import { createContext, useContext, useId } from "react";
import type * as TypesGen from "#/api/typesGenerated";
import { getChatFileURL } from "../../utils/chatAttachments";
import { getAttachmentDownload } from "./AttachmentBlocks";
import { AttachmentDownloadLink } from "./AttachmentDownloadLink";
import { FileProbeProvider } from "./FileProbeContext";
import {
	getForkHtmlAttachmentURL,
	latestForkHtmlAttachmentIds,
} from "./forkHtmlAttachments";
import type { ParsedMessageEntry } from "./types";

type HtmlFilePart = TypesGen.ChatFilePart & { file_id: string; name: string };

const LatestHtmlAttachmentsContext = createContext<
	ReadonlyMap<string, string> | undefined
>(undefined);

/**
 * Wraps the upstream FileProbeProvider so the timeline needs one hook line,
 * and tells each HTML attachment whether a newer one with its name exists.
 */
export const ForkFileProbeProvider: React.FC<
	React.PropsWithChildren<{
		evictedFileIds: ReadonlySet<string>;
		entries: readonly Pick<ParsedMessageEntry, "message">[];
	}>
> = ({ evictedFileIds, entries, children }) => (
	<FileProbeProvider evictedFileIds={evictedFileIds}>
		<LatestHtmlAttachmentsContext.Provider
			value={latestForkHtmlAttachmentIds(entries)}
		>
			{children}
		</LatestHtmlAttachmentsContext.Provider>
	</FileProbeProvider>
);

type ForkHtmlAttachmentPreviewProps = {
	block: HtmlFilePart;
	/** The upstream card, shown for a page that a newer one replaced. */
	children: React.ReactNode;
};

/** Only the newest attachment of a name renders as a live page. */
export const ForkHtmlAttachmentPreview: React.FC<
	ForkHtmlAttachmentPreviewProps
> = ({ block, children }) => {
	const latestFileId = useContext(LatestHtmlAttachmentsContext)?.get(
		block.name,
	);
	if (latestFileId !== undefined && latestFileId !== block.file_id) {
		return children;
	}
	return <ForkHtmlAttachmentViewer block={block} />;
};

/**
 * Renders the page in an iframe that may run scripts but has an opaque
 * origin, so it cannot read the Coder session, and whose server policy
 * blocks network access.
 */
const ForkHtmlAttachmentViewer: React.FC<{ block: HtmlFilePart }> = ({
	block,
}) => {
	const headerId = useId();
	const download = getAttachmentDownload(block, getChatFileURL(block.file_id));

	return (
		<figure aria-labelledby={headerId} className="flex w-full flex-col gap-1">
			{/* The header sits outside the frame, so the page cannot hide or
			    restyle it. */}
			<figcaption className="flex min-w-0 items-center gap-1 text-xs text-content-secondary">
				<span id={headerId} className="min-w-0">
					Page attached by the agent: {download.displayName}. It cannot see your
					session. Do not type passwords or secrets.
				</span>
				<AttachmentDownloadLink
					{...download}
					className="flex size-6 shrink-0 items-center justify-center rounded-md text-content-secondary transition-colors hover:bg-surface-secondary hover:text-content-primary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
				>
					<DownloadIcon aria-hidden="true" className="size-3.5" />
				</AttachmentDownloadLink>
			</figcaption>
			<iframe
				src={getForkHtmlAttachmentURL(block.file_id)}
				sandbox="allow-scripts"
				referrerPolicy="no-referrer"
				loading="lazy"
				title={download.displayName}
				className="h-96 w-full rounded-md border border-solid border-border bg-white"
			/>
		</figure>
	);
};
