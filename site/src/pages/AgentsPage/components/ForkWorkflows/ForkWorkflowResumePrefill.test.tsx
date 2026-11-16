import type { QueryClient } from "react-query";
import { QueryClientProvider } from "react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it } from "vitest";
import { chatEntityKey } from "#/api/queries/chats";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import { MockChat } from "#/testHelpers/chatEntities";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
} from "#/testHelpers/entities";
import { MockForkWorkflowRunId } from "#/testHelpers/forkWorkflows";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import type {
	EditorContentPart,
	FileReferenceData,
} from "../ChatMessageInput/ChatMessageInput";
import { ForkWorkflowResumePrefill } from "../ForkWorkflowResumePrefill";
import { forkWorkflowResumePrompt } from "./forkWorkflowResume";

// A composer double: records what the prefill wrote, like the real input.
// The editor-state pair mirrors the real handle: getEditorState always
// serializes a JSON tree once mounted, even when the composer has no text,
// and restoreEditorState rebuilds that state with the appended text, so
// the double tracks both paths. Chips are modeled as content parts, which
// is how the real handle reports a draft that has no text.
const composer = () => {
	let value = "";
	let chips = 0;
	let focused = false;
	return {
		setValue: (text: string) => {
			value = text;
			chips = 0;
		},
		insertText: () => {},
		clear: () => {
			value = "";
			chips = 0;
		},
		focus: () => {
			focused = true;
		},
		getValue: () => value,
		addFileReference: (_ref: FileReferenceData) => {
			chips += 1;
		},
		insertSkill: () => {
			chips += 1;
		},
		removeSkills: () => {},
		getEditorState: () => JSON.stringify({ root: { text: value, chips } }),
		restoreEditorState: (state: string, appendText = "") => {
			const parsed = JSON.parse(state);
			value = `${parsed.root.text}${appendText}`;
			chips = parsed.root.chips;
		},
		getContentParts: () =>
			[
				...(chips > 0 ? [{ type: "file-reference" }] : []),
				...(value ? [{ type: "text", text: value }] : []),
			] as EditorContentPart[],
		isFocused: () => focused,
	};
};

const renderPrefill = (
	queryClient: QueryClient,
	chatId: string = MockChat.id,
) => {
	const input = composer();
	renderComponent(
		<QueryClientProvider client={queryClient}>
			<DashboardContext.Provider
				value={{
					entitlements: MockEntitlements,
					experiments: ["fork-workflows"],
					appearance: MockAppearanceConfig,
					buildInfo: MockBuildInfo,
					organizations: [MockDefaultOrganization],
					showOrganizations: false,
					canViewOrganizationSettings: false,
				}}
			>
				<MemoryRouter initialEntries={[`/agents/${chatId}`]}>
					<Routes>
						<Route
							path="/agents/:agentId"
							element={
								<ForkWorkflowResumePrefill inputRef={{ current: input }} />
							}
						/>
					</Routes>
				</MemoryRouter>
			</DashboardContext.Provider>
		</QueryClientProvider>,
	);
	return input;
};

const seedChat = (queryClient: QueryClient, chat: typeof MockChat) => {
	queryClient.setQueryDefaults(chatEntityKey(chat.id), {
		staleTime: Number.POSITIVE_INFINITY,
		gcTime: Number.POSITIVE_INFINITY,
	});
	queryClient.setQueryData(chatEntityKey(chat.id), chat);
};

const dispatchResume = (chatId: string, prompt: string) => {
	window.dispatchEvent(
		new CustomEvent("fork-workflow-resume", {
			detail: { chatId, prompt },
		}),
	);
};

describe("ForkWorkflowResumePrefill", () => {
	it("prefills the composer with the resume request and focuses it", async () => {
		const queryClient = createTestQueryClient();
		seedChat(queryClient, MockChat);
		const input = renderPrefill(queryClient);

		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);

		expect(input.getValue()).toBe(
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);
		expect(input.isFocused()).toBe(true);
	});

	it("keeps a draft and appends the request on a new line", async () => {
		const queryClient = createTestQueryClient();
		seedChat(queryClient, MockChat);
		const input = renderPrefill(queryClient);
		input.setValue("First check the diff");

		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);

		expect(input.getValue()).toBe(
			`First check the diff\n\n${forkWorkflowResumePrompt(MockForkWorkflowRunId)}`,
		);
	});

	it("keeps a chip-only draft and appends the request after it", async () => {
		const queryClient = createTestQueryClient();
		seedChat(queryClient, MockChat);
		const input = renderPrefill(queryClient);
		input.addFileReference({
			fileName: "main.go",
			startLine: 1,
			endLine: 10,
			content: "package main",
		});

		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);

		// The chips survive, and the request is not separated by the
		// two blank lines a plain-text append would have added.
		expect(input.getValue()).toBe(
			`\n\n${forkWorkflowResumePrompt(MockForkWorkflowRunId)}`,
		);
		expect(input.getContentParts()[0]).toMatchObject({
			type: "file-reference",
		});
	});

	it("prefills again for the same run after the first request is consumed", async () => {
		const queryClient = createTestQueryClient();
		seedChat(queryClient, MockChat);
		const input = renderPrefill(queryClient);

		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);
		// The user sends or clears the prefilled request, then a later
		// resume arrives for the same run: it must prefill again.
		input.setValue("");
		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);

		expect(input.getValue()).toBe(
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);
	});

	it("ignores a resume request for another open chat", async () => {
		const queryClient = createTestQueryClient();
		seedChat(queryClient, MockChat);
		const input = renderPrefill(queryClient, "other-chat");

		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);

		expect(input.getValue()).toBe("");
	});

	it("renders nothing for a child chat", async () => {
		const queryClient = createTestQueryClient();
		seedChat(queryClient, { ...MockChat, parent_chat_id: "parent-1" });
		const input = renderPrefill(queryClient);

		dispatchResume(
			MockChat.id,
			forkWorkflowResumePrompt(MockForkWorkflowRunId),
		);

		expect(input.getValue()).toBe("");
	});
});
