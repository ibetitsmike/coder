import { act, fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { createMemoryRouter } from "react-router";
import { defaultUrlTransform } from "streamdown";
import { describe, expect, it, vi } from "vitest";
import type { ChatMessage, Experiment } from "#/api/typesGenerated";
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
import {
	createPreferenceSeededQueryClient,
	renderComponent,
	renderWithRouter,
} from "#/testHelpers/renderHelpers";
import { MessageScroller } from "#/vendor/message-scroller";
import { AttachmentBlock } from "./AttachmentBlocks";
import { ConversationTimeline } from "./ConversationTimeline";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";
import { ForkFileProbeProvider } from "./ForkHtmlAttachmentViewer";
import { latestForkHtmlAttachmentIds } from "./forkHtmlAttachments";
import {
	attachmentStepMessages,
	buildLiveStatus,
	buildQuestionTimelineArgs,
	MockCollapsedStepsPreferences,
	workingFixtureTime,
} from "./storyFixtures";

const chatId = "chat-html-1";

const dashboardValue = (experiments: Experiment[]) => ({
	entitlements: MockEntitlements,
	experiments,
	appearance: MockAppearanceConfig,
	buildInfo: MockBuildInfo,
	organizations: [MockDefaultOrganization],
	showOrganizations: false,
	canViewOrganizationSettings: false,
});

const renderPreview = (
	experiments: Experiment[],
	block: typeof MockChatHtmlFilePart = MockChatHtmlFilePart,
	{
		entries = MockChatHtmlMessages,
		evictedFileIds = new Set<string>(),
	}: {
		entries?: readonly { message: ChatMessage }[];
		evictedFileIds?: ReadonlySet<string>;
	} = {},
) =>
	renderWithRouter(
		createMemoryRouter(
			[
				{
					path: "/agents/:agentId",
					element: (
						<DashboardContext.Provider value={dashboardValue(experiments)}>
							<ForkFileProbeProvider
								evictedFileIds={evictedFileIds}
								entries={entries}
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
		// The upload is the newest file of the name, yet the agent's page
		// still renders and the upload does not.
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
		renderPreview(["fork-html-attachments"], upload, { entries });

		expect(screen.queryByTitle("choices.html")).toBeNull();
		expect(
			screen
				.getByRole("link", { name: "Download choices.html" })
				.getAttribute("href"),
		).toBe(`/api/v2/chats/files/${upload.file_id}`);

		renderPreview(["fork-html-attachments"], MockChatHtmlFilePart, {
			entries,
		});
		expect(
			screen.getByTitle<HTMLIFrameElement>("choices.html").getAttribute("src"),
		).toBe(`/api/v2/chats/fork/files/${MockChatHtmlFilePart.file_id}/html`);
	});

	it("shows the upstream card for a page that expired", () => {
		renderPreview(["fork-html-attachments"], MockChatHtmlFilePart, {
			evictedFileIds: new Set([MockChatHtmlFilePart.file_id]),
		});

		expect(screen.queryByTitle("choices.html")).toBeNull();
		expect(screen.getByText("Attachment expired")).toBeInTheDocument();
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

describe("ConversationTimeline with fork-html-attachments", () => {
	// One turn in which the agent attaches choices.html twice.
	const messages: ChatMessage[] = [
		{
			...MockChatHtmlMessages[0].message,
			id: 1,
			role: "user",
			created_at: workingFixtureTime(0),
			content: [{ type: "text", text: "Let me pick an option" }],
		},
		...[MockChatReplacedHtmlFilePart, MockChatHtmlFilePart].flatMap(
			(file, index) =>
				attachmentStepMessages({
					id: 2 + index * 3,
					toolCallId: `attach-${index}`,
					callAt: 1 + index * 2,
					toolName: "attach_file",
					args: { path: `/home/coder/${file.name}` },
					file,
					result: { name: file.name },
				}),
		),
		{
			...MockChatHtmlMessages[0].message,
			id: 8,
			role: "assistant",
			created_at: workingFixtureTime(10),
			content: [{ type: "text", text: "Pick one in the page." }],
		},
	];

	const renderTimeline = (experiments: Experiment[]) =>
		renderComponent(
			<DashboardContext.Provider value={dashboardValue(experiments)}>
				<QueryClientProvider
					client={createPreferenceSeededQueryClient(
						MockCollapsedStepsPreferences,
					)}
				>
					<MessageScroller.Provider autoScroll defaultScrollPosition="end">
						<MessageScroller.Root>
							<MessageScroller.Viewport>
								<MessageScroller.Content>
									<ConversationTimeline
										organizationId="organization-id"
										urlTransform={defaultUrlTransform}
										mcpServers={[]}
										streamTools={[]}
										liveStatus={buildLiveStatus()}
										subagentStatusOverrides={new Map()}
										subagentTitles={new Map()}
										subagentVariants={new Map()}
										{...buildQuestionTimelineArgs(messages)}
										chatStatus={null}
										hasMoreMessages={false}
										isChatCompleted
										showDesktopPreviews={false}
										hasActiveStream={false}
										isAwaitingFirstStreamChunk={false}
										onSendAskUserQuestionResponse={vi.fn()}
									/>
								</MessageScroller.Content>
							</MessageScroller.Viewport>
						</MessageScroller.Root>
					</MessageScroller.Provider>
				</QueryClientProvider>
			</DashboardContext.Provider>,
		);

	// The timeline's hook feeds the newest-page lookup from its messages, so
	// only the newest page of the name runs.
	it("renders only the newest agent page of a name", async () => {
		renderTimeline(["fork-html-attachments"]);
		await userEvent.click(
			screen.getByRole("button", { name: "Attachments (2)" }),
		);

		const frames = screen.getAllByTitle<HTMLIFrameElement>("choices.html");
		expect(frames.map((frame) => frame.getAttribute("src"))).toEqual([
			`/api/v2/chats/fork/files/${MockChatHtmlFilePart.file_id}/html`,
		]);
	});

	it("renders no page when fork-html-attachments is off", async () => {
		renderTimeline([]);
		await userEvent.click(
			screen.getByRole("button", { name: "Attachments (2)" }),
		);

		expect(screen.queryByTitle("choices.html")).toBeNull();
	});
});
