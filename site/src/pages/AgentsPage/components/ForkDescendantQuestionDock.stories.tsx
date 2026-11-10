import type { Meta, StoryObj } from "@storybook/react-vite";
import { spyOn } from "storybook/test";
import { API } from "#/api/api";
import { chatDescendantQuestionsKey } from "#/api/queries/forkChatQuestions";
import { MockChat } from "#/testHelpers/chatEntities";
import { mockApiError } from "#/testHelpers/entities";
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
		canAnswer: true,
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

export const LoadError: Story = {
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockRejectedValue(
			mockApiError({ message: "Database unavailable." }),
		);
	},
};

export const AnswersDisabled: Story = {
	args: { canAnswer: false },
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [MockChatDescendantQuestion],
			},
		],
	},
};
