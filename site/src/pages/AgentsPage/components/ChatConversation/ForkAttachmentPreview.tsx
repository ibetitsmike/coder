// This file is fork code: it is the only place fork features add attachment
// previews. Upstream files wrap each attachment in ForkAttachmentPreview once,
// so an upstream sync of them stays clean.

import { DownloadIcon } from "lucide-react";
import type React from "react";
import { useContext, useId } from "react";
import type * as TypesGen from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { getChatFileURL } from "../../utils/chatAttachments";
import { RecordingPreview } from "../ChatElements/tools/RecordingPreview";
import { getAttachmentDownload } from "./AttachmentBlocks";
import { AttachmentDownloadLink } from "./AttachmentDownloadLink";
import { useFileProbes } from "./FileProbeContext";
import { ForkHtmlAttachmentPreview } from "./ForkHtmlAttachmentViewer";
import { isForkHtmlAttachment } from "./forkHtmlAttachments";

type ForkAttachmentPreviewProps = {
	block: TypesGen.ChatFilePart;
	/** The upstream rendering, used whenever no fork preview applies. */
	children: React.ReactNode;
};

/**
 * Agent-attached MP4s, such as review videos, reuse the computer-use
 * recording player. Only stored files qualify, because the player loads the
 * video from its chat file URL. With fork-html-attachments, an HTML page
 * renders in a sandboxed viewer.
 */
export const ForkAttachmentPreview: React.FC<ForkAttachmentPreviewProps> = ({
	block,
	children,
}) => {
	// Timeline stories and tests render without a DashboardProvider; treat
	// that as every fork experiment being off, which is upstream behavior.
	const experiments = useContext(DashboardContext)?.experiments ?? [];
	const { hasExpired } = useFileProbes();
	const nameId = useId();

	if (
		experiments.includes("fork-html-attachments") &&
		isForkHtmlAttachment(block) &&
		!hasExpired(block.file_id)
	) {
		return (
			<ForkHtmlAttachmentPreview block={block}>
				{children}
			</ForkHtmlAttachmentPreview>
		);
	}

	if (
		!experiments.includes("fork-chat-todos") ||
		block.media_type !== "video/mp4" ||
		!block.file_id ||
		// The upstream card shows the expired state; the caption here would
		// still offer a dead download link.
		hasExpired(block.file_id)
	) {
		return children;
	}

	const download = getAttachmentDownload(block, getChatFileURL(block.file_id));
	return (
		<figure
			aria-labelledby={nameId}
			className="flex w-fit max-w-full flex-col gap-1"
		>
			<div className="w-fit">
				<RecordingPreview
					recordingFileId={block.file_id}
					playLabel={`Play ${download.displayName}`}
				/>
			</div>
			<figcaption className="flex min-w-0 items-center gap-1 text-xs text-content-secondary">
				<span id={nameId} className="truncate">
					{download.displayName}
				</span>
				<AttachmentDownloadLink
					{...download}
					className="flex size-6 shrink-0 items-center justify-center rounded-md text-content-secondary transition-colors hover:bg-surface-secondary hover:text-content-primary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
				>
					<DownloadIcon aria-hidden="true" className="size-3.5" />
				</AttachmentDownloadLink>
			</figcaption>
		</figure>
	);
};
