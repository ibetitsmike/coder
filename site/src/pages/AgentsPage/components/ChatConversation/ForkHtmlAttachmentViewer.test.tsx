import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
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
import {
	forkHtmlMessageIntervalMs,
	forkHtmlMessageMaxLength,
	forkHtmlMessageType,
	formatForkHtmlMessage,
	latestForkHtmlAttachmentIds,
	parseForkHtmlMessage,
} from "./forkHtmlAttachments";

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

const originalUserActivation = Object.getOwnPropertyDescriptor(
	navigator,
	"userActivation",
);

const setUserActivation = (isActive: boolean) => {
	Object.defineProperty(navigator, "userActivation", {
		configurable: true,
		value: { isActive, hasBeenActive: isActive },
	});
};

// A click in the page focuses its frame, so the frame is focused unless a
// test clicks elsewhere first.
const postFromFrame = (
	data: unknown,
	source?: Window | null,
	clickInFrame = true,
) => {
	const frame = screen.getByTitle<HTMLIFrameElement>("choices.html");
	act(() => {
		if (clickInFrame) {
			frame.focus();
		}
		window.dispatchEvent(
			new MessageEvent("message", {
				data,
				source: source === undefined ? frame.contentWindow : source,
			}),
		);
	});
};

const pageMessage = (text: string) => ({ type: forkHtmlMessageType, text });

const mockCreateChatMessage = () =>
	vi
		.spyOn(API.experimental, "createChatMessage")
		.mockResolvedValue({ queued: false });

const proposalName = "The page asks to send this message to the chat:";

const queryProposal = () =>
	screen.queryByRole("region", { name: proposalName });

// Expects the open proposal to show text, then confirms it.
const confirmProposal = async (text: string) => {
	const proposal = await screen.findByRole("region", { name: proposalName });
	expect(within(proposal).getByText(text, { exact: false })).toHaveTextContent(
		formatForkHtmlMessage("choices.html", text),
		{ normalizeWhitespace: false },
	);
	await userEvent.click(within(proposal).getByRole("button", { name: "Send" }));
};

const expectSent = (
	createChatMessage: ReturnType<typeof mockCreateChatMessage>,
	text: string,
) => {
	expect(createChatMessage).toHaveBeenCalledTimes(1);
	expect(createChatMessage).toHaveBeenCalledWith(chatId, {
		content: [
			{ type: "text", text: formatForkHtmlMessage("choices.html", text) },
		],
	});
};

// Proposes a valid message last and expects it to be the only one sent. The
// mutation calls the API asynchronously, so a check right after the ignored
// messages would pass even if they were sent.
const expectOnlyValidMessageSent = async (
	createChatMessage: ReturnType<typeof mockCreateChatMessage>,
) => {
	postFromFrame(pageMessage("Option B"));
	await confirmProposal("Option B");
	await screen.findByText("Sent to the chat");
	expectSent(createChatMessage, "Option B");
};

describe("parseForkHtmlMessage", () => {
	it("returns the trimmed text of a well formed message", () => {
		expect(parseForkHtmlMessage(pageMessage("  Option B  "))).toBe("Option B");
	});

	it("rejects messages that do not match the schema", () => {
		const rejected: unknown[] = [
			null,
			"Option B",
			[forkHtmlMessageType, "Option B"],
			{ type: "other", text: "Option B" },
			{ type: forkHtmlMessageType, text: 1 },
			{ type: forkHtmlMessageType },
			{ ...pageMessage("Option B"), role: "assistant" },
			pageMessage("   "),
			pageMessage("x".repeat(forkHtmlMessageMaxLength + 1)),
		];
		for (const data of rejected) {
			expect(parseForkHtmlMessage(data)).toBeNull();
		}
	});

	it("accepts text of the maximum length", () => {
		const text = "x".repeat(forkHtmlMessageMaxLength);
		expect(parseForkHtmlMessage(pageMessage(text))).toBe(text);
	});

	it("counts characters, not UTF-16 code units", () => {
		const text = "😀".repeat(forkHtmlMessageMaxLength);
		expect(parseForkHtmlMessage(pageMessage(text))).toBe(text);
		expect(parseForkHtmlMessage(pageMessage(`${text}😀`))).toBeNull();
	});
});

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
	beforeEach(() => {
		setUserActivation(true);
	});

	afterEach(() => {
		if (originalUserActivation) {
			Object.defineProperty(
				navigator,
				"userActivation",
				originalUserActivation,
			);
		} else {
			Reflect.deleteProperty(navigator, "userActivation");
		}
		vi.restoreAllMocks();
	});

	it("loads the page from the sandboxed route", () => {
		renderPreview(["fork-html-attachments"]);
		const frame = screen.getByTitle<HTMLIFrameElement>("choices.html");

		expect(frame.getAttribute("src")).toBe(
			`/api/v2/chats/fork/files/${MockChatHtmlFilePart.file_id}/html`,
		);
		expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
		expect(frame.getAttribute("referrerpolicy")).toBe("no-referrer");
	});

	it("sends a clicked choice as a user message that names the page", async () => {
		const createChatMessage = mockCreateChatMessage();
		renderPreview(["fork-html-attachments"]);

		await expectOnlyValidMessageSent(createChatMessage);
		expect(formatForkHtmlMessage("choices.html", "Option B")).toBe(
			'Message from the attached page "choices.html":\n\nOption B',
		);
	});

	it("keeps the proposal for a retry when the message fails to send", async () => {
		const createChatMessage = mockCreateChatMessage().mockRejectedValueOnce(
			new Error("The message queue is full."),
		);
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("Option B"));
		await confirmProposal("Option B");
		await screen.findByText("The message queue is full.");

		await confirmProposal("Option B");
		await screen.findByText("Sent to the chat");
		expect(createChatMessage).toHaveBeenCalledTimes(2);
		expect(queryProposal()).toBeNull();
	});

	it("clears the last send status for a new proposal", async () => {
		mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("Option A"));
		await confirmProposal("Option A");
		await screen.findByText("Sent to the chat");

		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		postFromFrame(pageMessage("Option B"));
		await screen.findByRole("region", { name: proposalName });
		expect(screen.getByRole("status").textContent).toBe("");
	});

	it("ignores messages from other windows", async () => {
		const createChatMessage = mockCreateChatMessage();
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("From the dashboard"), window);
		postFromFrame(pageMessage("From nowhere"), null);

		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("ignores messages that do not match the schema", async () => {
		const createChatMessage = mockCreateChatMessage();
		renderPreview(["fork-html-attachments"]);

		postFromFrame({ type: "coder:api", path: "/api/v2/users/me" });
		postFromFrame(pageMessage("x".repeat(forkHtmlMessageMaxLength + 1)));

		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("ignores messages while the user works outside the frame", async () => {
		const createChatMessage = mockCreateChatMessage();
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("After a click elsewhere"), undefined, false);

		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("ignores messages right after input in the dashboard", async () => {
		const createChatMessage = mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		await userEvent.click(screen.getByRole("figure"));
		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs - 1);
		postFromFrame(pageMessage("After a click in the dashboard"));
		expect(queryProposal()).toBeNull();

		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("counts Tab into the frame as input in the dashboard", async () => {
		const createChatMessage = mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		await userEvent.keyboard("{Tab}");
		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs - 1);
		postFromFrame(pageMessage("On focus"));
		expect(queryProposal()).toBeNull();

		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("returns focus to the page when the proposal closes", async () => {
		mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);
		const frame = screen.getByTitle("choices.html");

		postFromFrame(pageMessage("Option A"));
		const proposal = await screen.findByRole("region", { name: proposalName });
		await userEvent.click(
			within(proposal).getByRole("button", { name: "Cancel" }),
		);
		expect(frame).toHaveFocus();

		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		postFromFrame(pageMessage("Option B"));
		await confirmProposal("Option B");
		await screen.findByText("Sent to the chat");
		expect(frame).toHaveFocus();
	});

	it("ignores messages sent without a click", async () => {
		const createChatMessage = mockCreateChatMessage();
		setUserActivation(false);
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("Without a click"));
		setUserActivation(true);

		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("shows the warning header outside the frame", () => {
		renderPreview(["fork-html-attachments"]);

		expect(
			screen.getByRole("figure", {
				name: "Page attached by the agent: choices.html. It cannot see your session. Do not type passwords or secrets.",
			}),
		).toContainElement(screen.getByTitle("choices.html"));
	});

	it("sends nothing until the user confirms", async () => {
		const createChatMessage = mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("Option A"));
		const proposal = await screen.findByRole("region", { name: proposalName });
		await userEvent.click(
			within(proposal).getByRole("button", { name: "Cancel" }),
		);
		expect(queryProposal()).toBeNull();

		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		await expectOnlyValidMessageSent(createChatMessage);
	});

	it("keeps an open proposal when the page sends another", async () => {
		const createChatMessage = mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("Option B"));
		await screen.findByRole("region", { name: proposalName });
		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		postFromFrame(pageMessage("Option C"));

		await confirmProposal("Option B");
		await screen.findByText("Sent to the chat");
		expectSent(createChatMessage, "Option B");
	});

	it("proposes at most one message per interval", async () => {
		const createChatMessage = mockCreateChatMessage();
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		postFromFrame(pageMessage("Option A"));
		const proposal = await screen.findByRole("region", { name: proposalName });
		await userEvent.click(
			within(proposal).getByRole("button", { name: "Cancel" }),
		);
		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs - 1);
		postFromFrame(pageMessage("Option C"));
		expect(queryProposal()).toBeNull();

		now.mockReturnValue(1_000_000 + forkHtmlMessageIntervalMs);
		await expectOnlyValidMessageSent(createChatMessage);
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

	it("drops an open proposal when the page navigates away", async () => {
		vi.spyOn(Date, "now").mockReturnValue(1_000_000);
		renderPreview(["fork-html-attachments"]);

		fireEvent.load(screen.getByTitle("choices.html"));
		postFromFrame(pageMessage("Option A"));
		await screen.findByRole("region", { name: proposalName });

		fireEvent.load(screen.getByTitle("choices.html"));
		expect(queryProposal()).toBeNull();
	});

	it("moves focus from a closed frame to the reopen button", () => {
		renderPreview(["fork-html-attachments"]);
		const frame = screen.getByTitle<HTMLIFrameElement>("choices.html");

		fireEvent.load(frame);
		act(() => frame.focus());
		fireEvent.load(frame);

		expect(
			screen.getByRole("button", { name: "Show the page again" }),
		).toHaveFocus();
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
