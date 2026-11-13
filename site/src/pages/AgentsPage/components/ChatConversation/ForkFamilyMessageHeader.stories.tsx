import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { defaultUrlTransform } from "streamdown";
import { MockFamilyMessage } from "#/testHelpers/forkFamilyMessages";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { deriveMessageDisplayState } from "./messageHelpers";
import { parseMessageContent } from "./messageParsing";
import { UserMessageContent } from "./UserMessageContent";

const parsed = parseMessageContent(MockFamilyMessage.content);

const meta: Meta<typeof UserMessageContent> = {
	title: "pages/AgentsPage/ChatConversation/ForkFamilyMessageHeader",
	component: UserMessageContent,
	decorators: [withDashboardProvider],
	parameters: { experiments: ["fork-family-messages"] },
	args: {
		displayState: deriveMessageDisplayState({
			message: MockFamilyMessage,
			parsed,
			hideActions: false,
			hasActiveStream: false,
			isAwaitingFirstStreamChunk: false,
		}),
		markdown: parsed.markdown,
		isEditing: false,
		onImageClick: fn(),
		onTextFileClick: fn(),
		urlTransform: defaultUrlTransform,
	},
};

export default meta;
type Story = StoryObj<typeof UserMessageContent>;

export const FamilyMessage: Story = {};
