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
import { ForkWorkflowResumePrefill } from "../ForkWorkflowResumePrefill";
import { forkWorkflowResumePrompt } from "./forkWorkflowResume";

// A composer double: records what the prefill wrote, like the real input.
// The editor-state pair mirrors the real handle: getEditorState serializes
// the draft (chips included), restoreEditorState rebuilds it with the
// appended text, so the double tracks both paths.
const composer = () => {
	let value = "";
	let restored = "";
	let focused = false;
	return {
		setValue: (text: string) => {
			value = text;
			restored = "";
		},
		insertText: () => {},
		clear: () => {
			value = "";
			restored = "";
		},
		focus: () => {
			focused = true;
		},
		getValue: () => value,
		addFileReference: () => {},
		insertSkill: () => {},
		removeSkills: () => {},
		getEditorState: () =>
			value || restored ? `state:${value || restored}` : "",
		restoreEditorState: (state: string, appendText = "") => {
			restored = state;
			value = `${state.replace(/^state:/, "")}${appendText}`;
		},
		getContentParts: () => [],
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
