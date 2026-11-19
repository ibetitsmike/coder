import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkSavedWorkflow } from "#/testHelpers/forkSavedWorkflows";
import { ForkSavedWorkflowDetailDialog } from "./ForkSavedWorkflowDetailDialog";

const meta = {
	title: "pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowDetailDialog",
	component: ForkSavedWorkflowDetailDialog,
	args: {
		workflow: MockForkSavedWorkflow,
		isLoading: false,
		error: null,
		isRetrying: false,
		onRetry: fn(),
		onClose: fn(),
	},
} satisfies Meta<typeof ForkSavedWorkflowDetailDialog>;

export default meta;
type Story = StoryObj<typeof ForkSavedWorkflowDetailDialog>;

export const Loaded: Story = {};

export const Loading: Story = {
	args: { workflow: undefined, isLoading: true },
};

export const LoadError: Story = {
	args: {
		workflow: undefined,
		isLoading: false,
		error: mockApiError({ message: "Saved workflow not found." }),
		onRetry: fn(),
	},
};

export const Retrying: Story = {
	args: { ...LoadError.args, isRetrying: true },
};

export const Minimal: Story = {
	args: {
		workflow: {
			...MockForkSavedWorkflow,
			description: "",
			args_schema: undefined,
			phases: [],
		},
	},
};

export const RefetchError: Story = {
	args: {
		workflow: MockForkSavedWorkflow,
		error: mockApiError({ message: "Failed to reload saved workflow." }),
		onRetry: fn(),
	},
};
