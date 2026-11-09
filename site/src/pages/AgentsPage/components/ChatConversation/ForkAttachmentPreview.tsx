// This file is fork code: it is the only place fork features add inline
// attachment previews. Upstream's MessageBlocks.tsx wraps each file block in
// ForkAttachmentPreview once, so an upstream sync of it stays clean.

import type React from "react";
import { useContext, useState } from "react";
import type * as TypesGen from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { getAttachmentHref } from "../../utils/chatAttachments";
import {
	AttachmentPreviewFrame,
	getAttachmentDisplayName,
	getAttachmentDownload,
} from "./AttachmentBlocks";
import { useFileProbes } from "./FileProbeContext";

type ForkAttachmentPreviewProps = {
	block: TypesGen.ChatFilePart;
	/** The upstream rendering, used whenever no fork preview applies. */
	children: React.ReactNode;
};

/**
 * Plays an MP4 that the agent attached with attach_file inline while the
 * fork-chat-todos experiment is enabled. Any other file, an expired MP4, or
 * one the browser cannot play renders the upstream children instead.
 */
export const ForkAttachmentPreview: React.FC<ForkAttachmentPreviewProps> = ({
	block,
	children,
}) => {
	// Timeline stories and tests render without a DashboardProvider; treat
	// that as every fork experiment being off, which is upstream behavior.
	const experiments = useContext(DashboardContext)?.experiments ?? [];
	const { hasExpired } = useFileProbes();
	const [playbackFailed, setPlaybackFailed] = useState(false);
	const href = getAttachmentHref(block);

	if (
		!experiments.includes("fork-chat-todos") ||
		block.media_type !== "video/mp4" ||
		!href ||
		playbackFailed ||
		(block.file_id !== undefined && hasExpired(block.file_id))
	) {
		return children;
	}

	return (
		<AttachmentPreviewFrame {...getAttachmentDownload(block, href)}>
			{/* oxlint-disable-next-line jsx-a11y/media-has-caption -- Agent attached videos do not have caption tracks. */}
			<video
				src={href}
				controls
				preload="metadata"
				aria-label={getAttachmentDisplayName(block)}
				className="aspect-video w-96 max-w-full rounded-lg border border-solid border-border bg-black"
				onError={() => setPlaybackFailed(true)}
			/>
		</AttachmentPreviewFrame>
	);
};
