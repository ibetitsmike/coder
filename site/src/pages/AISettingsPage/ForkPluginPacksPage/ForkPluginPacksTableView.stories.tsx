import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import {
	MockOrganization,
	MockOrganization2,
	mockApiError,
} from "#/testHelpers/entities";
import {
	MockForkPluginPack,
	MockForkPluginPackWithoutPath,
} from "#/testHelpers/forkPluginPacks";
import { OrganizationPicker } from "../MCPServersPage/components/OrganizationPicker";
import { ForkPluginPacksTableView } from "./ForkPluginPacksTableView";

const meta = {
	title: "pages/AISettingsPage/ForkPluginPacksTableView",
	component: ForkPluginPacksTableView,
	args: {
		packs: [MockForkPluginPack, MockForkPluginPackWithoutPath],
		error: undefined,
		isLoadingError: false,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
	},
} satisfies Meta<typeof ForkPluginPacksTableView>;

export default meta;
type Story = StoryObj<typeof ForkPluginPacksTableView>;

export const Populated: Story = {};

export const WithOrganizationPicker: Story = {
	args: {
		action: (
			<OrganizationPicker
				id="plugin-packs-organization"
				className="w-full sm:w-60"
				organizations={[MockOrganization, MockOrganization2]}
				organization={MockOrganization}
				onChange={fn()}
				showLabel={false}
			/>
		),
	},
};

export const Empty: Story = {
	args: { packs: [] },
};

export const LoadError: Story = {
	args: {
		packs: [],
		error: mockApiError({ message: "Failed to list plugin packs." }),
		isLoadingError: true,
	},
};

export const Retrying: Story = {
	args: {
		packs: [],
		error: mockApiError({ message: "Failed to list plugin packs." }),
		isLoadingError: true,
		isRetrying: true,
	},
};

export const Loading: Story = {
	args: { packs: [], isLoading: true },
};

export const RefetchError: Story = {
	args: {
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};

export const EmptyRefetchError: Story = {
	args: {
		packs: [],
		error: mockApiError({ message: "Failed to list plugin packs." }),
	},
};
