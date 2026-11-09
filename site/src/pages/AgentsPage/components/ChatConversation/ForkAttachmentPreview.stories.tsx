import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { MockChatVideoFilePart } from "#/testHelpers/forkChatAttachments";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { AttachmentBlock } from "./AttachmentBlocks";
import { FileProbeProvider } from "./FileProbeContext";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";

const meta: Meta<typeof ForkAttachmentPreview> = {
	title: "pages/AgentsPage/ChatConversation/ForkAttachmentPreview",
	component: ForkAttachmentPreview,
	decorators: [withDashboardProvider],
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

export const AttachedVideo: Story = {
	parameters: { experiments: ["fork-chat-todos"] },
};

export const ExperimentOff: Story = {
	parameters: { experiments: [] },
};

export const ExpiredVideo: Story = {
	parameters: { experiments: ["fork-chat-todos"] },
	decorators: [
		(Story) => (
			<FileProbeProvider
				evictedFileIds={new Set([MockChatVideoFilePart.file_id ?? ""])}
			>
				<Story />
			</FileProbeProvider>
		),
	],
};
