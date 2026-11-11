import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, spyOn, userEvent } from "storybook/test";
import { API } from "#/api/api";
import {
	MockDefaultOrganization,
	MockGroup,
	MockOrganizationSkillACL,
	mockApiError,
} from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { ForkOrganizationAgentDefinitionSharingDialog } from "./ForkOrganizationAgentDefinitionSharingDialog";

const meta: Meta<typeof ForkOrganizationAgentDefinitionSharingDialog> = {
	title:
		"pages/AISettingsPage/ForkAgentDefinitionsPage/ForkOrganizationAgentDefinitionSharingDialog",
	component: ForkOrganizationAgentDefinitionSharingDialog,
	args: {
		organizationId: MockDefaultOrganization.id,
		definitionName: MockForkAgentDefinitionMetadata.name,
		onClose: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof ForkOrganizationAgentDefinitionSharingDialog>;

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
			mockApiError({ message: "Failed to save agent definition permissions." }),
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
			mockApiError({ message: "Failed to load agent definition permissions." }),
		);
	},
	play: async () => {
		await screen.findByText("Failed to load agent definition permissions.");
	},
};
