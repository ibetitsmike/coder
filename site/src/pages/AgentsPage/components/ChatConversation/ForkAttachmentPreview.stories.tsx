import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	MockChatHtmlFilePart,
	MockChatHtmlMessages,
	MockChatReplacedHtmlFilePart,
	MockChatVideoAttachment,
	MockChatVideoFilePart,
} from "#/testHelpers/forkChatAttachments";
import { withDashboardProvider } from "#/testHelpers/storybook";
import { AttachmentBlock } from "./AttachmentBlocks";
import { FileProbeProvider } from "./FileProbeContext";
import { ForkAttachmentPreview } from "./ForkAttachmentPreview";
import { ForkFileProbeProvider } from "./ForkHtmlAttachmentViewer";
import { TurnAttachments } from "./TurnAttachments";

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

export const ExperimentOff: Story = {};

export const ExpiredVideo: Story = {
	parameters: { experiments: ["fork-chat-todos"] },
	decorators: [
		(Story) => (
			<FileProbeProvider
				evictedFileIds={new Set([MockChatVideoFilePart.file_id])}
			>
				<Story />
			</FileProbeProvider>
		),
	],
};

export const AttachmentsMenu: Story = {
	parameters: { experiments: ["fork-chat-todos"] },
	render: () => <TurnAttachments attachments={[MockChatVideoAttachment]} />,
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", { name: "Attachments (1)" }),
		);
	},
};

const htmlPageArgs = (block: typeof MockChatHtmlFilePart) => ({
	block,
	children: (
		<AttachmentBlock
			block={block}
			onImageClick={fn()}
			onTextFileClick={fn()}
			framePreview
		/>
	),
});

const htmlPageParameters = {
	experiments: ["fork-html-attachments"],
	reactRouter: reactRouterParameters({
		location: { pathParams: { agentId: "chat-html-1" } },
		routing: { path: "/agents/:agentId" },
	}),
};

const withHtmlMessages = (Story: React.FC) => (
	<ForkFileProbeProvider
		evictedFileIds={new Set()}
		entries={MockChatHtmlMessages}
	>
		<Story />
	</ForkFileProbeProvider>
);

// Storybook has no Coder server, so the frame does not load the page itself.
export const AttachedHtmlPage: Story = {
	args: htmlPageArgs(MockChatHtmlFilePart),
	parameters: htmlPageParameters,
	decorators: [withHtmlMessages],
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await expect(
			canvas.getByText(
				"Page attached by the agent: choices.html. It cannot see your session. Do not type passwords or secrets.",
			),
		).toBeVisible();
		await expect(canvas.getByTitle("choices.html")).toBeVisible();
	},
};

export const ReplacedHtmlPage: Story = {
	args: htmlPageArgs(MockChatReplacedHtmlFilePart),
	parameters: htmlPageParameters,
	decorators: [withHtmlMessages],
};

export const HtmlPageExperimentOff: Story = {
	args: htmlPageArgs(MockChatHtmlFilePart),
	parameters: { ...htmlPageParameters, experiments: [] },
	decorators: [withHtmlMessages],
};
