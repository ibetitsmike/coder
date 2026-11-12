import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, userEvent } from "storybook/test";
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
		access: { create: true, update: true, delete: true },
		error: undefined,
		isLoadingError: false,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
		updatingPackIds: new Set(),
		onInstall: fn(),
		onEditSources: fn(),
		onUpdate: fn(),
		onDelete: fn(),
	},
} satisfies Meta<typeof ForkPluginPacksTableView>;

export default meta;
type Story = StoryObj<typeof ForkPluginPacksTableView>;

export const Populated: Story = {};

export const Mobile: Story = {
	parameters: {
		viewport: { defaultViewport: "mobile1" },
		pixel: { matrix: { viewports: ["phone"] } },
	},
};

export const RowMenuOpen: Story = {
	play: async () => {
		await userEvent.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkPluginPack.name}`,
			}),
		);
	},
};

export const ReadOnly: Story = {
	args: { access: { create: false, update: false, delete: false } },
};

export const UpdateOnlyMenuOpen: Story = {
	args: { access: { create: false, update: true, delete: false } },
	play: async () => {
		await userEvent.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkPluginPack.name}`,
			}),
		);
	},
};

export const DeleteOnlyMenuOpen: Story = {
	args: { access: { create: false, update: false, delete: true } },
	play: async () => {
		await userEvent.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkPluginPack.name}`,
			}),
		);
	},
};

export const UpdatingMenuOpen: Story = {
	args: { updatingPackIds: new Set([MockForkPluginPack.id]) },
	play: async () => {
		await userEvent.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkPluginPack.name}`,
			}),
		);
	},
};

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

export const EmptyReadOnly: Story = {
	args: {
		packs: [],
		access: { create: false, update: false, delete: false },
	},
};

export const Loading: Story = {
	args: { packs: [], isLoading: true },
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
