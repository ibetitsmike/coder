import type { Meta, StoryObj } from "@storybook/react-vite";
import { chatDescendantQuestionsKey } from "#/api/forkChatQuestions";
import type { ChatDescendantQuestion } from "#/api/typesGenerated";
import { MOCK_TIMESTAMP, MockChat } from "#/testHelpers/chatEntities";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { ForkDescendantQuestionDock } from "./ForkDescendantQuestionDock";

const pendingQuestion: ChatDescendantQuestion = {
	chat_id: "grandchild-chat",
	parent_chat_id: "child-chat",
	chat_title: "Migrate the billing tables",
	tool_call_id: "call-1",
	created_at: MOCK_TIMESTAMP,
	questions: [
		{
			header: "Backfill",
			question: "Should the migration backfill rows written before 2024?",
			options: [{ label: "Backfill every row" }, { label: "Skip old rows" }],
		},
	],
};

const meta: Meta<typeof ForkDescendantQuestionDock> = {
	title: "pages/AgentsPage/ForkDescendantQuestionDock",
	component: ForkDescendantQuestionDock,
	decorators: [withDashboardProvider],
	args: {
		chat: MockChat,
		isReadOnly: false,
	},
	parameters: {
		experiments: ["fork-nested-subagents"],
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [pendingQuestion],
			},
		],
	},
};

export default meta;
type Story = StoryObj<typeof ForkDescendantQuestionDock>;

export const OnePending: Story = {};

export const SeveralPending: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [
					pendingQuestion,
					{
						...pendingQuestion,
						chat_id: "other-child-chat",
						chat_title: "",
						tool_call_id: "call-2",
					},
				],
			},
		],
	},
};

export const UntitledChat: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [{ ...pendingQuestion, chat_title: "" }],
			},
		],
	},
};
