import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChatFilePart, Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { getChatFileURL } from "../../utils/chatAttachments";
import { AttachmentBlock } from "./AttachmentBlocks";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";

const videoBlock: ChatFilePart = {
	type: "file",
	media_type: "video/mp4",
	name: "review.mp4",
	file_id: "6f1c2a9e-0d7b-4c43-9f0e-2b8d1a5c7e31",
};

const renderPreview = (experiments: Experiment[]) =>
	renderComponent(
		<DashboardContext.Provider
			value={{
				entitlements: MockEntitlements,
				experiments,
				appearance: MockAppearanceConfig,
				buildInfo: MockBuildInfo,
				organizations: [MockDefaultOrganization],
				showOrganizations: false,
				canViewOrganizationSettings: false,
			}}
		>
			<ForkAttachmentPreview block={videoBlock}>
				<AttachmentBlock
					block={videoBlock}
					onImageClick={vi.fn()}
					onTextFileClick={vi.fn()}
					framePreview
				/>
			</ForkAttachmentPreview>
		</DashboardContext.Provider>,
	);

describe("ForkAttachmentPreview", () => {
	it("plays an attached MP4 from its chat file URL", () => {
		renderPreview(["fork-chat-todos"]);

		expect(screen.getByLabelText("review.mp4").getAttribute("src")).toBe(
			getChatFileURL("6f1c2a9e-0d7b-4c43-9f0e-2b8d1a5c7e31"),
		);
	});

	it("keeps the upstream download card when fork-chat-todos is off", () => {
		renderPreview([]);

		expect(screen.queryByLabelText("review.mp4")).toBeNull();
	});

	it("falls back to the download card when the MP4 cannot play", () => {
		renderPreview(["fork-chat-todos"]);

		fireEvent.error(screen.getByLabelText("review.mp4"));

		expect(screen.queryByLabelText("review.mp4")).toBeNull();
	});
});
