import { act, fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

	it("skips pages that the user uploaded", () => {
		const upload = { ...MockChatHtmlFilePart, file_id: "chat-file-html-user" };
		const entries = [
			...MockChatHtmlMessages,
			{
				message: {
					...MockChatHtmlMessages[1].message,
					id: 3,
					role: "user" as const,
					content: [upload],
				},
			},
		];

		expect(latestForkHtmlAttachmentIds(entries)).toEqual(
			new Map([[MockChatHtmlFilePart.name, MockChatHtmlFilePart.file_id]]),
		);
	});

	it("prefers a page from the streaming turn", () => {
		const streamed = {
			...MockChatHtmlFilePart,
			file_id: "chat-file-html-streamed",
		};

		expect(
			latestForkHtmlAttachmentIds(MockChatHtmlMessages, [streamed]),
		).toEqual(new Map([[streamed.name, streamed.file_id]]));
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

	it("closes the frame when the page navigates away", async () => {
		renderPreview(["fork-html-attachments"]);

		fireEvent.load(screen.getByTitle("choices.html"));
		fireEvent.load(screen.getByTitle("choices.html"));
		expect(screen.queryByTitle("choices.html")).toBeNull();

		await userEvent.click(
			screen.getByRole("button", { name: "Show the page again" }),
		);
		const frame = screen.getByTitle<HTMLIFrameElement>("choices.html");
		expect(frame.getAttribute("src")).toBe(
			`/api/v2/chats/fork/files/${MockChatHtmlFilePart.file_id}/html`,
		);
		fireEvent.load(frame);
		expect(screen.queryByTitle("choices.html")).toBe(frame);
	});

	it("keeps focus with the viewer when the frame closes and reopens", async () => {
		renderPreview(["fork-html-attachments"]);
		const frame = screen.getByTitle<HTMLIFrameElement>("choices.html");

		fireEvent.load(frame);
		act(() => frame.focus());
		fireEvent.load(frame);

		const reopen = screen.getByRole("button", { name: "Show the page again" });
		expect(reopen).toHaveFocus();

		await userEvent.keyboard("{Enter}");
		expect(screen.getByTitle("choices.html")).toHaveFocus();
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

	it("shows the upstream card for a page that the user uploaded", () => {
		const upload = {
			...MockChatHtmlFilePart,
			file_id: "chat-file-html-user",
			name: "mine.html",
		};
		renderPreview(["fork-html-attachments"], upload);

		expect(screen.queryByTitle("mine.html")).toBeNull();
		expect(
			screen
				.getByRole("link", { name: "Download mine.html" })
				.getAttribute("href"),
		).toBe(`/api/v2/chats/files/${upload.file_id}`);
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
