// This file is fork code: it is the only place fork features add inline
// attachment previews. Upstream's MessageBlocks.tsx wraps each file block in
// ForkAttachmentPreview once, so an upstream sync of it stays clean.

import type React from "react";
import { useContext } from "react";
import type * as TypesGen from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { RecordingPreview } from "../ChatElements/tools/RecordingPreview";

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

	if (
		!experiments.includes("fork-chat-todos") ||
		block.media_type !== "video/mp4" ||
		!block.file_id
	) {
		return children;
	}

	return <RecordingPreview recordingFileId={block.file_id} />;
};
