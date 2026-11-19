import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, spyOn, userEvent } from "storybook/test";
import { API } from "#/api/api";
import {
	MockDefaultOrganization,
	MockGroup,
	MockOrganizationSkillACL,
	mockApiError,
} from "#/testHelpers/entities";
import { MockForkSavedWorkflowMetadata } from "#/testHelpers/forkSavedWorkflows";
import { ForkOrganizationSavedWorkflowSharingDialog } from "./ForkOrganizationSavedWorkflowSharingDialog";

const meta: Meta<typeof ForkOrganizationSavedWorkflowSharingDialog> = {
	title:
		"pages/AISettingsPage/ForkWorkflowsPage/ForkOrganizationSavedWorkflowSharingDialog",
	component: ForkOrganizationSavedWorkflowSharingDialog,
	args: {
		organizationId: MockDefaultOrganization.id,
		workflowName: MockForkSavedWorkflowMetadata.name,
		onClose: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof ForkOrganizationSavedWorkflowSharingDialog>;

export const GroupAndUser: Story = {
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockResolvedValue({
			data: MockOrganizationSkillACL,
		});
	},
	play: async () => {
		await screen.findByRole("button", {
			name: `Remove ${MockGroup.display_name}`,
		});
	},
};

export const SaveError: Story = {
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockResolvedValue({
			data: MockOrganizationSkillACL,
		});
		spyOn(API.getAxiosInstance(), "patch").mockRejectedValue(
			mockApiError({ message: "Failed to save saved workflow permissions." }),
		);
	},
	play: async () => {
		await userEvent.click(
			await screen.findByRole("button", {
				name: `Remove ${MockGroup.display_name}`,
			}),
		);
		await userEvent.click(
			screen.getByRole("button", { name: "Save permissions" }),
		);
	},
};

export const Empty: Story = {
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockResolvedValue({
			data: { users: [], groups: [] },
		});
	},
	play: async () => {
		await screen.findByText("No members or groups have permission yet");
	},
};

export const LoadError: Story = {
	beforeEach: () => {
		spyOn(API.getAxiosInstance(), "get").mockRejectedValue(
			mockApiError({ message: "Failed to load saved workflow permissions." }),
		);
	},
	play: async () => {
		await screen.findByText("Failed to load saved workflow permissions.");
	},
};
