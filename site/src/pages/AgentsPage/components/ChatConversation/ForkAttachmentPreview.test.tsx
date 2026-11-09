import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Experiment } from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockChatVideoFilePart } from "#/testHelpers/forkChatAttachments";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { getAttachmentHref } from "../../utils/chatAttachments";
import { AttachmentBlock } from "./AttachmentBlocks";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";

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
			<ForkAttachmentPreview block={MockChatVideoFilePart}>
				<AttachmentBlock
					block={MockChatVideoFilePart}
					onImageClick={vi.fn()}
					onTextFileClick={vi.fn()}
					framePreview
				/>
			</ForkAttachmentPreview>
		</DashboardContext.Provider>,
	);

describe("ForkAttachmentPreview", () => {
	it("plays an attached MP4 when fork-chat-todos is on", async () => {
		renderPreview(["fork-chat-todos"]);

		await userEvent.click(
			screen.getByRole("button", { name: "View recording" }),
		);

		await screen.findByRole("dialog", { name: "Recording playback" });
	});

	it("offers the upstream download when fork-chat-todos is off", () => {
		renderPreview([]);

		expect(screen.queryByRole("button", { name: "View recording" })).toBeNull();
		expect(
			screen
				.getByRole("link", { name: "Download review.mp4" })
				.getAttribute("href"),
		).toBe(getAttachmentHref(MockChatVideoFilePart));
	});
});
