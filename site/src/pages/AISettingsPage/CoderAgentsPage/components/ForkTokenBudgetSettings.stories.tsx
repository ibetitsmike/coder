// This file is fork code: stories for the fork-token-budget experiment's
// organization token budget form.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkOrganizationTokenBudget } from "#/testHelpers/forkTokenBudget";
import { ForkTokenBudgetSettings } from "./ForkTokenBudgetSettings";

const meta = {
	title:
		"pages/AISettingsPage/CoderAgentsPage/components/ForkTokenBudgetSettings",
	component: ForkTokenBudgetSettings,
	args: {
		setting: MockForkOrganizationTokenBudget,
		isLoading: false,
		loadError: undefined,
		canEdit: true,
		onSave: fn(),
		isSaving: false,
		saveError: undefined,
	},
} satisfies Meta<typeof ForkTokenBudgetSettings>;

export default meta;
type Story = StoryObj<typeof ForkTokenBudgetSettings>;

export const Default: Story = {};

export const TokenBudget: Story = {
	args: {
		setting: { strategy: "token_budget", fallback: "summary" },
	},
};

export const Edited: Story = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const strategy = canvas.getByRole("radiogroup", { name: "Strategy" });
		await userEvent.click(
			within(strategy).getByRole("radio", { name: "Token budget" }),
		);
		const fallback = canvas.getByRole("radiogroup", {
			name: "Fallback at the usable limit",
		});
		await expect(
			within(fallback).getByRole("radio", {
				name: "Summarize the conversation",
			}),
		).toBeEnabled();
	},
};

export const Saving: Story = {
	args: {
		setting: { strategy: "token_budget", fallback: "new_window" },
		isSaving: true,
	},
};

export const ReadOnly: Story = {
	args: { canEdit: false },
};

export const Loading: Story = {
	args: { setting: undefined, isLoading: true },
};

export const LoadError: Story = {
	args: {
		setting: undefined,
		loadError: mockApiError({
			message: "Failed to load the organization's token budget setting.",
		}),
	},
};

export const SaveError: Story = {
	args: {
		saveError: mockApiError({ message: "Invalid token budget setting." }),
	},
};
