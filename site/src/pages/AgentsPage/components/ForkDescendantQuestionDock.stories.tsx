import type { Meta, StoryObj } from "@storybook/react-vite";
import { spyOn, userEvent, within } from "storybook/test";
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

const OtherChildQuestion = {
	...MockChatDescendantQuestion,
	chat_id: "other-child-chat",
	chat_title: "",
	tool_call_id: "call-2",
};

const UntitledChatQuestion = { ...MockChatDescendantQuestion, chat_title: "" };

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
				data: [MockChatDescendantQuestion, OtherChildQuestion],
			},
		],
	},
};

export const UntitledChat: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [UntitledChatQuestion],
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

const mockSecretQuestion = {
	...MockChatDescendantQuestion,
	tool_call_id: "call-secret",
	questions: [
		{
			header: "Token",
			question: "Paste the **API token** for the staging registry.",
			options: [],
			secret: true,
		},
	],
};

export const SecretPending: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [mockSecretQuestion],
			},
		],
	},
};

export const SecretAnswersDisabled: Story = {
	args: { canAnswer: false },
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [mockSecretQuestion],
			},
		],
	},
};

export const SecretAnswerSent: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [mockSecretQuestion],
			},
		],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "post").mockResolvedValue({
			data: undefined,
		});
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.type(
			canvas.getByLabelText(/API token/),
			"staging-token-value",
		);
		await userEvent.click(canvas.getByRole("button", { name: "Submit" }));
		await canvas.findByRole("status");
	},
};

export const SecretAnswerSubmitting: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [mockSecretQuestion],
			},
		],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "post").mockReturnValue(
			new Promise(() => {}),
		);
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.type(
			canvas.getByLabelText(/API token/),
			"staging-token-value",
		);
		await userEvent.click(canvas.getByRole("button", { name: "Submit" }));
		await canvas.findByText("Submitting...");
	},
};

export const SecretAnswerFailed: Story = {
	parameters: {
		queries: [
			{
				key: chatDescendantQuestionsKey(MockChat.id),
				data: [mockSecretQuestion],
			},
		],
	},
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "post").mockRejectedValue(
			mockApiError({ message: "The secret answer is too short." }),
		);
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.type(canvas.getByLabelText(/API token/), "short-value");
		await userEvent.click(canvas.getByRole("button", { name: "Submit" }));
		await canvas.findByRole("alert");
	},
};
