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

const mockTriageSkill = {
	...MockSkills[0],
	id: "skill-triage",
	name: "triage",
	description: "Triage incoming issues.",
};

const mockDeploySkill = {
	...MockSkills[0],
	id: "skill-deploy",
	name: "deploy",
	description: "Deploy the workspace app.",
};

const mockSkillLists: SkillSourceList<SkillMetadata>[] = [
	{ source: "personal", skills: MockSkills },
	{ source: "org", skills: [mockTriageSkill] },
	{ source: "project", skills: [] },
	{ source: "workspace", skills: [mockDeploySkill] },
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

const pickerProps: React.ComponentProps<typeof ForkPinnableSkillListView> = {
	skillLists: mockSkillLists,
	pins: mockPins,
	disabled: false,
	error: undefined,
	isRetrying: false,
	onRetry: fn(),
	onToggle: fn(),
};

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
		children: <ForkPinnableSkillListView {...pickerProps} />,
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
		children: <ForkPinnableSkillListView {...pickerProps} pins={[]} />,
	},
	play: Expanded.play,
};

export const ReadOnly: Story = {
	args: { canEdit: false, children: undefined },
	play: Expanded.play,
};

export const Saving: Story = {
	args: {
		isPending: true,
		children: <ForkPinnableSkillListView {...pickerProps} disabled />,
	},
	play: Expanded.play,
};

export const PinLimit: Story = {
	args: {
		pins: mockFullPins,
		children: (
			<ForkPinnableSkillListView {...pickerProps} pins={mockFullPins} />
		),
	},
	play: Expanded.play,
};

export const SkillsLoadError: Story = {
	args: {
		children: (
			<ForkPinnableSkillListView
				{...pickerProps}
				skillLists={mockSkillLists.map((list) =>
					list.source === "org" ? { ...list, skills: undefined } : list,
				)}
				error={mockApiError({ message: "Failed to load organization skills." })}
			/>
		),
	},
	play: Expanded.play,
};
