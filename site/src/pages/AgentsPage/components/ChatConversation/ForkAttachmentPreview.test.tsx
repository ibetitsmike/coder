import { screen, within } from "@testing-library/react";
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
import {
	MockChatVideoAttachment,
	MockChatVideoFilePart,
} from "#/testHelpers/forkChatAttachments";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { AttachmentBlock } from "./AttachmentBlocks";
import { FileProbeProvider } from "./FileProbeContext";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";
import { TurnAttachments } from "./TurnAttachments";

const renderWithExperiments = (
	experiments: Experiment[],
	ui: React.ReactElement,
) =>
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
			{ui}
		</DashboardContext.Provider>,
	);

const renderPreview = (experiments: Experiment[]) =>
	renderWithExperiments(
		experiments,
		<ForkAttachmentPreview block={MockChatVideoFilePart}>
			<AttachmentBlock
				block={MockChatVideoFilePart}
				onImageClick={vi.fn()}
				onTextFileClick={vi.fn()}
				framePreview
			/>
		</ForkAttachmentPreview>,
	);

const openTurnAttachments = async (
	experiments: Experiment[],
	evictedFileIds: ReadonlySet<string> = new Set(),
) => {
	renderWithExperiments(
		experiments,
		<FileProbeProvider evictedFileIds={evictedFileIds}>
			<TurnAttachments attachments={[MockChatVideoAttachment]} />
		</FileProbeProvider>,
	);
	await userEvent.click(
		screen.getByRole("button", { name: "Attachments (1)" }),
	);
	return screen.findByRole("dialog", { name: "Attachments" });
};

describe("ForkAttachmentPreview", () => {
	it("plays an attached MP4 when fork-chat-todos is on", async () => {
		renderPreview(["fork-chat-todos"]);
		const preview = screen.getByRole("figure", { name: "review.mp4" });

		await userEvent.click(
			within(preview).getByRole("button", { name: "Play review.mp4" }),
		);

		await screen.findByRole("dialog", { name: "Recording playback" });
	});

	it("offers the upstream download when fork-chat-todos is off", () => {
		renderPreview([]);

		expect(
			screen.queryByRole("button", { name: "Play review.mp4" }),
		).toBeNull();
		expect(
			screen
				.getByRole("link", { name: "Download review.mp4" })
				.getAttribute("href"),
		).toBe("/api/v2/chats/files/chat-file-1");
	});
});

describe("TurnAttachments with an attached MP4", () => {
	it("plays the MP4 from the menu when fork-chat-todos is on", async () => {
		const menu = await openTurnAttachments(["fork-chat-todos"]);

		await userEvent.click(
			within(menu).getByRole("button", { name: "Play review.mp4" }),
		);

		await screen.findByRole("dialog", { name: "Recording playback" });
	});

	it("shows the upstream expired card for an expired MP4", async () => {
		const menu = await openTurnAttachments(
			["fork-chat-todos"],
			new Set([MockChatVideoFilePart.file_id]),
		);

		expect(
			within(menu)
				.getByRole("button", { name: "review.mp4 Attachment expired" })
				.getAttribute("aria-disabled"),
		).toBe("true");
		expect(
			within(menu).queryByRole("link", { name: "Download review.mp4" }),
		).toBeNull();
	});

	it("offers the upstream download card when fork-chat-todos is off", async () => {
		const menu = await openTurnAttachments([]);

		expect(
			within(menu).queryByRole("button", { name: "Play review.mp4" }),
		).toBeNull();
		expect(
			within(menu)
				.getByRole("link", { name: "Download review.mp4" })
				.getAttribute("href"),
		).toBe("/api/v2/chats/files/chat-file-1");
	});
});
