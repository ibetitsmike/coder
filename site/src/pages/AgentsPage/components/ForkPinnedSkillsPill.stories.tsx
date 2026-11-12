import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import type { ChatPinnedSkill } from "#/api/typesGenerated";
import { mockApiError } from "#/testHelpers/entities";
import { MockSkills } from "#/testHelpers/skills";
import type { SkillSourceList } from "../utils/skillAliases";
import type { SkillMetadata } from "./ChatMessageInput/SkillsTriggerMenu";
import {
	ForkPinnableSkillListView,
	ForkPinnedSkillsPillView,
} from "./ForkPinnedSkillsPill";

const mockSkillLists: SkillSourceList<SkillMetadata>[] = [
	{ source: "personal", skills: MockSkills },
	{
		source: "org",
		skills: [{ name: "triage", description: "Triage incoming issues." }],
	},
	{ source: "project", skills: [] },
	{
		source: "workspace",
		skills: [{ name: "deploy", description: "Deploy the workspace app." }],
	},
];

const mockPins: ChatPinnedSkill[] = [
	{ source: "personal", name: "docs" },
	{ source: "org", name: "triage" },
];

const mockFullPins: ChatPinnedSkill[] = [
	{ source: "personal", name: "reviewer" },
	{ source: "personal", name: "docs" },
	{ source: "personal", name: "plan" },
	{ source: "org", name: "triage" },
	{ source: "workspace", name: "deploy" },
	{ source: "personal", name: "release" },
	{ source: "personal", name: "lint" },
	{ source: "personal", name: "changelog" },
];

const meta: Meta<typeof ForkPinnedSkillsPillView> = {
	title: "pages/AgentsPage/ForkPinnedSkillsPill",
	component: ForkPinnedSkillsPillView,
	args: {
		pins: mockPins,
		error: undefined,
		isRetrying: false,
		onRetry: fn(),
		canEdit: true,
		isPending: false,
		onUnpin: fn(),
		children: (
			<ForkPinnableSkillListView
				skillLists={mockSkillLists}
				pins={mockPins}
				disabled={false}
				error={undefined}
				isRetrying={false}
				onRetry={fn()}
				onToggle={fn()}
			/>
		),
	},
};

export default meta;
type Story = StoryObj<typeof ForkPinnedSkillsPillView>;

export const Collapsed: Story = {};

export const Expanded: Story = {
	play: async ({ canvasElement }) => {
		await userEvent.click(
			within(canvasElement).getByRole("button", { name: /Pinned skills/ }),
		);
	},
};

export const Loading: Story = {
	args: { pins: undefined, children: undefined },
	play: Expanded.play,
};

export const LoadError: Story = {
	args: {
		pins: undefined,
		error: mockApiError({ message: "You do not have access to this chat." }),
		children: undefined,
	},
	play: Expanded.play,
};

export const NonePinned: Story = {
	args: {
		pins: [],
		children: (
			<ForkPinnableSkillListView
				skillLists={mockSkillLists}
				pins={[]}
				disabled={false}
				error={undefined}
				isRetrying={false}
				onRetry={fn()}
				onToggle={fn()}
			/>
		),
	},
	play: Expanded.play,
};

export const ReadOnly: Story = {
	args: { canEdit: false, children: undefined },
	play: Expanded.play,
};

export const PinLimit: Story = {
	args: {
		pins: mockFullPins,
		children: (
			<ForkPinnableSkillListView
				skillLists={mockSkillLists}
				pins={mockFullPins}
				disabled={false}
				error={undefined}
				isRetrying={false}
				onRetry={fn()}
				onToggle={fn()}
			/>
		),
	},
	play: Expanded.play,
};

export const SkillsLoadError: Story = {
	args: {
		children: (
			<ForkPinnableSkillListView
				skillLists={mockSkillLists.map((list) =>
					list.source === "org" ? { ...list, skills: undefined } : list,
				)}
				pins={mockPins}
				disabled={false}
				error={mockApiError({ message: "Failed to load organization skills." })}
				isRetrying={false}
				onRetry={fn()}
				onToggle={fn()}
			/>
		),
	},
	play: Expanded.play,
};
