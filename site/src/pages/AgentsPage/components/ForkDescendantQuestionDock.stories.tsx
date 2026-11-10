import type { Meta, StoryObj } from "@storybook/react-vite";
import { chatDescendantQuestionsKey } from "#/api/queries/forkChatQuestions";
import { MockChat } from "#/testHelpers/chatEntities";
import { MockChatDescendantQuestion } from "#/testHelpers/forkChatQuestions";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { ForkDescendantQuestionDock } from "./ForkDescendantQuestionDock";

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
	},
};

export default meta;
type Story = StoryObj<typeof ForkDescendantQuestionDock>;

export const OnePending: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [MockChatDescendantQuestion],
			},
		],
	},
};

export const SeveralPending: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [
					MockChatDescendantQuestion,
					{
						...MockChatDescendantQuestion,
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
				data: [{ ...MockChatDescendantQuestion, chat_title: "" }],
			},
		],
	},
};
