import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, userEvent } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import {
	MockForkOrganizationSavedWorkflowMetadata,
	MockForkSavedWorkflow,
	MockForkSavedWorkflowMetadata,
	MockForkSharedSavedWorkflowMetadata,
} from "#/testHelpers/forkSavedWorkflows";
import { ForkSavedWorkflowsTableView } from "./ForkSavedWorkflowsTableView";

const meta = {
	title: "pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowsTableView",
	component: ForkSavedWorkflowsTableView,
	args: {
		title: "Personal workflows",
		description:
			"Starlark workflows you own in this organization. Your chats run one with run_workflow.",
		emptyMessage: "No personal workflows in this organization yet.",
		workflows: [MockForkSavedWorkflowMetadata],
		error: undefined,
		isLoading: false,
		isRetrying: false,
		onRetry: fn(),
		onCreate: fn(),
		onView: fn(),
		onEdit: fn(),
		onDelete: fn(),
		editorState: undefined,
		deleteState: undefined,
		detailState: undefined,
	},
} satisfies Meta<typeof ForkSavedWorkflowsTableView>;

export default meta;
type Story = StoryObj<typeof ForkSavedWorkflowsTableView>;

export const Populated: Story = {};

export const RowMenuOpen: Story = {
	play: async () => {
		await userEvent.click(
			await screen.findByRole("button", {
				name: `More actions for ${MockForkSavedWorkflowMetadata.name}`,
			}),
		);
	},
};

export const UpdateOnlyMenuOpen: Story = {
	args: { access: { create: false, update: true, delete: false } },
	play: RowMenuOpen.play,
};

export const ReadOnly: Story = {
	args: { access: { create: false, update: false, delete: false } },
};

export const ShareMenuOpen: Story = {
	args: { onShare: fn() },
	play: RowMenuOpen.play,
};

/** Only the row the user shared offers a menu. */
export const RowAccess: Story = {
	args: {
		title: "Organization workflows",
		workflows: [
			MockForkSharedSavedWorkflowMetadata,
			MockForkOrganizationSavedWorkflowMetadata,
		],
		access: { create: false, update: false, delete: false },
		rowAccess: (workflow) =>
			workflow.id === MockForkSharedSavedWorkflowMetadata.id
				? { create: false, update: true, delete: true }
				: { create: false, update: false, delete: false },
	},
};

export const ShareConfirm: Story = {
	args: {
		shareState: {
			workflow: MockForkSavedWorkflowMetadata,
			error: undefined,
			isSharing: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const SharePending: Story = {
	args: {
		shareState: {
			workflow: MockForkSavedWorkflowMetadata,
			error: undefined,
			isSharing: true,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const ShareError: Story = {
	args: {
		shareState: {
			workflow: MockForkSavedWorkflowMetadata,
			error: mockApiError({
				message: "The organization already has a workflow with this name.",
				status: 409,
			}),
			isSharing: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const Empty: Story = {
	args: { workflows: [] },
};

export const AtLimit: Story = {
	args: {
		workflows: Array.from({ length: 100 }, (_, i) => ({
			...MockForkSavedWorkflowMetadata,
			id: `saved-workflow-${i}`,
			name: `workflow-${i}`,
		})),
	},
	play: async () => {
		await userEvent.hover(
			await screen.findByRole("button", { name: "New workflow" }),
		);
		await screen.findByRole("tooltip");
	},
};

export const EmptyReadOnly: Story = {
	args: {
		workflows: [],
		access: { create: false, update: false, delete: false },
	},
};

export const Loading: Story = {
	args: { workflows: [], isLoading: true },
};

export const LoadError: Story = {
	args: {
		workflows: [],
		error: mockApiError({ message: "Failed to list saved workflows." }),
	},
};

export const Retrying: Story = {
	args: {
		workflows: [],
		error: mockApiError({ message: "Failed to list saved workflows." }),
		isRetrying: true,
	},
};

export const RefetchError: Story = {
	args: {
		error: mockApiError({ message: "Failed to list saved workflows." }),
	},
};

export const DeleteConfirm: Story = {
	args: {
		deleteState: {
			workflow: MockForkSavedWorkflowMetadata,
			message: `Your personal workflow ${MockForkSavedWorkflowMetadata.name} will be deleted. Organization workflows with the same name are not affected. This action cannot be undone.`,
			error: undefined,
			isDeleting: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const DeleteError: Story = {
	args: {
		deleteState: {
			workflow: MockForkSavedWorkflowMetadata,
			message: `Your personal workflow ${MockForkSavedWorkflowMetadata.name} will be deleted. Organization workflows with the same name are not affected. This action cannot be undone.`,
			error: mockApiError({ message: "Failed to delete saved workflow." }),
			isDeleting: false,
			onConfirm: fn(),
			onClose: fn(),
		},
	},
};

export const DetailLoaded: Story = {
	args: {
		detailState: {
			workflow: MockForkSavedWorkflow,
			isLoading: false,
			error: undefined,
			isRetrying: false,
			onClose: fn(),
		},
	},
};

export const DetailLoadError: Story = {
	args: {
		detailState: {
			workflow: undefined,
			isLoading: false,
			error: mockApiError({ message: "Saved workflow not found." }),
			onRetry: fn(),
			isRetrying: false,
			onClose: fn(),
		},
	},
};
