import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { MockChatVideoFilePart } from "#/testHelpers/forkChatAttachments";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { AttachmentBlock } from "./AttachmentBlocks";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";

const meta: Meta<typeof ForkAttachmentPreview> = {
	title: "pages/AgentsPage/ChatConversation/ForkAttachmentPreview",
	component: ForkAttachmentPreview,
	decorators: [withDashboardProvider],
	parameters: { experiments: ["fork-chat-todos"] },
	args: {
		block: MockChatVideoFilePart,
		children: (
			<AttachmentBlock
				block={MockChatVideoFilePart}
				onImageClick={fn()}
				onTextFileClick={fn()}
				framePreview
			/>
		),
	},
};

export default meta;
type Story = StoryObj<typeof ForkAttachmentPreview>;

export const AttachedVideo: Story = {};
