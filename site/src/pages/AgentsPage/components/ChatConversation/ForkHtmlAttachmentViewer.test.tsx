import { screen } from "@testing-library/react";
import { createMemoryRouter } from "react-router";
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
	MockChatHtmlFilePart,
	MockChatHtmlMessages,
	MockChatReplacedHtmlFilePart,
} from "#/testHelpers/forkChatAttachments";
import { renderWithRouter } from "#/testHelpers/renderHelpers";
import { AttachmentBlock } from "./AttachmentBlocks";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";
import { ForkFileProbeProvider } from "./ForkHtmlAttachmentViewer";
import { latestForkHtmlAttachmentIds } from "./forkHtmlAttachments";

const chatId = "chat-html-1";

const renderPreview = (
	experiments: Experiment[],
	block: typeof MockChatHtmlFilePart = MockChatHtmlFilePart,
) =>
	renderWithRouter(
		createMemoryRouter(
			[
				{
					path: "/agents/:agentId",
					element: (
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
							<ForkFileProbeProvider
								evictedFileIds={new Set()}
								entries={MockChatHtmlMessages}
							>
								<ForkAttachmentPreview block={block}>
									<AttachmentBlock
										block={block}
										onImageClick={vi.fn()}
										onTextFileClick={vi.fn()}
										framePreview
									/>
								</ForkAttachmentPreview>
							</ForkFileProbeProvider>
						</DashboardContext.Provider>
					),
				},
			],
			{ initialEntries: [`/agents/${chatId}`] },
		),
	);

describe("latestForkHtmlAttachmentIds", () => {
	it("maps each page name to its newest attachment", () => {
		expect(latestForkHtmlAttachmentIds(MockChatHtmlMessages)).toEqual(
			new Map([[MockChatHtmlFilePart.name, MockChatHtmlFilePart.file_id]]),
		);
	});
});

describe("ForkHtmlAttachmentViewer", () => {
	it("loads the page from the sandboxed route", () => {
		renderPreview(["fork-html-attachments"]);
		const frame = screen.getByTitle<HTMLIFrameElement>("choices.html");

		expect(frame.getAttribute("src")).toBe(
			`/api/v2/chats/fork/files/${MockChatHtmlFilePart.file_id}/html`,
		);
		expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
		expect(frame.getAttribute("referrerpolicy")).toBe("no-referrer");
	});

	it("shows the warning header outside the frame", () => {
		renderPreview(["fork-html-attachments"]);

		expect(
			screen.getByRole("figure", {
				name: "Page attached by the agent: choices.html. It cannot see your session. Do not type passwords or secrets.",
			}),
		).toContainElement(screen.getByTitle("choices.html"));
	});

	it("shows the upstream card for a page that a newer one replaced", () => {
		renderPreview(["fork-html-attachments"], MockChatReplacedHtmlFilePart);

		expect(screen.queryByTitle("choices.html")).toBeNull();
		expect(
			screen
				.getByRole("link", { name: "Download choices.html" })
				.getAttribute("href"),
		).toBe(`/api/v2/chats/files/${MockChatReplacedHtmlFilePart.file_id}`);
	});

	it("shows the upstream card when fork-html-attachments is off", () => {
		renderPreview([]);

		expect(screen.queryByTitle("choices.html")).toBeNull();
		expect(
			screen
				.getByRole("link", { name: "Download choices.html" })
				.getAttribute("href"),
		).toBe(`/api/v2/chats/files/${MockChatHtmlFilePart.file_id}`);
	});
});
