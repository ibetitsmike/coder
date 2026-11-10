// This file is fork code: stories for the fork-nested-subagents experiment's
// organization subagent limits form.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, userEvent, within } from "storybook/test";
import type { ForkOrganizationSubagentLimitsResponse } from "#/api/typesGenerated";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkOrganizationSubagentLimits } from "#/testHelpers/forkSubagentLimits";
import { ForkSubagentLimitsSettings } from "./ForkSubagentLimitsSettings";

const meta = {
	title:
		"pages/AISettingsPage/CoderAgentsPage/components/ForkSubagentLimitsSettings",
	component: ForkSubagentLimitsSettings,
	args: {
		limits: MockForkOrganizationSubagentLimits,
		isLoading: false,
		loadError: undefined,
		canEdit: true,
		onSave: fn(),
		isSaving: false,
		saveError: undefined,
	},
} satisfies Meta<typeof ForkSubagentLimitsSettings>;

export default meta;
type Story = StoryObj<typeof ForkSubagentLimitsSettings>;

const mockUnsetLimits: ForkOrganizationSubagentLimitsResponse = {
	...MockForkOrganizationSubagentLimits,
	organization: {},
	effective: MockForkOrganizationSubagentLimits.deployment,
};

export const Editable: Story = {};

export const Unset: Story = {
	args: { limits: mockUnsetLimits },
};

export const Edited: Story = {
	play: async ({ canvasElement }) => {
		const input = within(canvasElement).getByLabelText("Max depth");
		await userEvent.clear(input);
		await userEvent.type(input, "3");
	},
};

export const Saving: Story = {
	args: { isSaving: true },
};

export const ReadOnly: Story = {
	args: { canEdit: false },
};

export const Loading: Story = {
	args: { limits: undefined, isLoading: true },
};

export const LoadError: Story = {
	args: {
		limits: undefined,
		loadError: mockApiError({
			message: "Failed to load the organization's subagent limits.",
		}),
	},
};

export const RefetchError: Story = {
	args: {
		loadError: mockApiError({
			message: "Failed to refresh the organization's subagent limits.",
		}),
	},
};

export const SaveError: Story = {
	args: {
		saveError: mockApiError({ message: "Invalid subagent limits." }),
	},
};

export const InvalidValue: Story = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const input = canvas.getByLabelText("Max depth");
		await userEvent.clear(input);
		await userEvent.type(input, "17");
		await userEvent.tab();
	},
};
