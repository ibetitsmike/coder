// This file is fork code: it is the only place fork features add inline
// attachment previews. Upstream's MessageBlocks.tsx wraps each file block in
// ForkAttachmentPreview once, so an upstream sync of it stays clean.

import { DownloadIcon } from "lucide-react";
import type React from "react";
import { useContext, useId } from "react";
import type * as TypesGen from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { getAttachmentHref } from "../../utils/chatAttachments";
import { RecordingPreview } from "../ChatElements/tools/RecordingPreview";
import { getAttachmentDownload } from "./AttachmentBlocks";
import { AttachmentDownloadLink } from "./AttachmentDownloadLink";

type ForkAttachmentPreviewProps = {
	block: TypesGen.ChatFilePart;
	/** The upstream rendering, used whenever no fork preview applies. */
	children: React.ReactNode;
};

/** Previews a stored MP4 with the recording player while fork-chat-todos is on. */
export const ForkAttachmentPreview: React.FC<ForkAttachmentPreviewProps> = ({
	block,
	children,
}) => {
	// Timeline stories and tests render without a DashboardProvider; treat
	// that as every fork experiment being off, which is upstream behavior.
	const experiments = useContext(DashboardContext)?.experiments ?? [];
	const nameId = useId();
	const href = getAttachmentHref(block);

	if (
		!experiments.includes("fork-chat-todos") ||
		block.media_type !== "video/mp4" ||
		!block.file_id ||
		!href
	) {
		return children;
	}

	const download = getAttachmentDownload(block, href);
	return (
		<figure aria-labelledby={nameId} className="m-0 flex flex-col gap-1">
			<RecordingPreview recordingFileId={block.file_id} />
			<figcaption className="flex min-w-0 items-center gap-1 text-xs text-content-secondary">
				<span id={nameId} className="truncate">
					{download.displayName}
				</span>
				<AttachmentDownloadLink
					{...download}
					className="flex size-6 shrink-0 items-center justify-center rounded text-content-secondary hover:text-content-primary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
				>
					<DownloadIcon aria-hidden="true" className="size-3.5" />
				</AttachmentDownloadLink>
			</figcaption>
		</figure>
	);
};
