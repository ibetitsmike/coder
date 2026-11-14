// This file is fork code: the sandboxed viewer of the fork-html-attachments
// experiment.

import { DownloadIcon } from "lucide-react";
import type React from "react";
import { createContext, useContext, useId, useRef, useState } from "react";
import type * as TypesGen from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { getChatFileURL } from "../../utils/chatAttachments";
import { getAttachmentDownload } from "./AttachmentBlocks";
import { AttachmentDownloadLink } from "./AttachmentDownloadLink";
import { FileProbeProvider } from "./FileProbeContext";
import {
	getForkHtmlAttachmentURL,
	latestForkHtmlAttachmentIds,
} from "./forkHtmlAttachments";
import type { ParsedMessageEntry, RenderBlock } from "./types";

type HtmlFilePart = TypesGen.ChatFilePart & { file_id: string; name: string };

const LatestHtmlAttachmentsContext = createContext<
	ReadonlyMap<string, string> | undefined
>(undefined);

/**
 * Wraps the upstream FileProbeProvider so the timeline needs one hook line,
 * and tells each HTML attachment whether it is the newest agent page of its
 * name.
 */
export const ForkFileProbeProvider: React.FC<
	React.PropsWithChildren<{
		evictedFileIds: ReadonlySet<string>;
		entries: readonly Pick<ParsedMessageEntry, "message">[];
		liveBlocks?: readonly RenderBlock[];
	}>
> = ({ evictedFileIds, entries, liveBlocks, children }) => (
	<FileProbeProvider evictedFileIds={evictedFileIds}>
		<LatestHtmlAttachmentsContext.Provider
			value={latestForkHtmlAttachmentIds(entries, liveBlocks)}
		>
			{children}
		</LatestHtmlAttachmentsContext.Provider>
	</FileProbeProvider>
);

type ForkHtmlAttachmentPreviewProps = {
	block: HtmlFilePart;
	/**
	 * The upstream card, shown for a page that a newer one replaced and for
	 * an upload by the user.
	 */
	children: React.ReactNode;
};

/** Only the newest agent attachment of a name renders as a live page. */
export const ForkHtmlAttachmentPreview: React.FC<
	ForkHtmlAttachmentPreviewProps
> = ({ block, children }) => {
	const latestFileId = useContext(LatestHtmlAttachmentsContext)?.get(
		block.name,
	);
	if (latestFileId !== block.file_id) {
		return children;
	}
	return <ForkHtmlAttachmentViewer block={block} />;
};

/**
 * Renders the page in an iframe that may run scripts but has an opaque
 * origin, so it cannot read the Coder session, and whose server policy
 * blocks network access. The sandbox still lets a page navigate its own
 * frame, so the viewer closes the frame after its first navigation.
 */
const ForkHtmlAttachmentViewer: React.FC<{ block: HtmlFilePart }> = ({
	block,
}) => {
	const headerId = useId();
	const download = getAttachmentDownload(block, getChatFileURL(block.file_id));
	const frameLoads = useRef(0);
	const [navigatedAway, setNavigatedAway] = useState(false);
	const [frameKey, setFrameKey] = useState(0);

	// The page itself is the first load. Any later load is the page leaving
	// for another address, or reloading.
	const onFrameLoad = () => {
		frameLoads.current += 1;
		if (frameLoads.current > 1) {
			setNavigatedAway(true);
		}
	};

	const reopen = () => {
		frameLoads.current = 0;
		setFrameKey((key) => key + 1);
		setNavigatedAway(false);
	};

	return (
		<figure aria-labelledby={headerId} className="flex w-full flex-col gap-1">
			{/* The header sits outside the frame, so the page cannot hide or
			    restyle it. */}
			<figcaption className="flex min-w-0 items-center gap-1 text-xs text-content-secondary">
				<span id={headerId} className="min-w-0 break-words">
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
			{navigatedAway ? (
				<div className="flex h-96 w-full flex-col items-center justify-center gap-2 rounded-md border border-solid border-border p-4 text-center text-sm text-content-secondary">
					<p className="m-0">
						The page tried to load another address, so the viewer closed it.
					</p>
					<Button size="sm" variant="outline" onClick={reopen}>
						Show the page again
					</Button>
				</div>
			) : (
				<iframe
					key={frameKey}
					src={getForkHtmlAttachmentURL(block.file_id)}
					sandbox="allow-scripts"
					referrerPolicy="no-referrer"
					loading="lazy"
					title={download.displayName}
					onLoad={onFrameLoad}
					className="h-96 w-full rounded-md border border-solid border-border bg-white"
				/>
			)}
		</figure>
	);
};
