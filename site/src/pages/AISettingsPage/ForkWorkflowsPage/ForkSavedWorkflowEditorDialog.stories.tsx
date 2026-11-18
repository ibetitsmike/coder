import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { MockForkSavedWorkflowMetadata } from "#/testHelpers/forkSavedWorkflows";
import { MockForkWorkflowScript } from "#/testHelpers/forkWorkflows";
import { ForkSavedWorkflowEditorDialog } from "./ForkSavedWorkflowEditorDialog";
import { emptySavedWorkflowFormValues } from "./savedWorkflowForm";

const mockEditValues = {
	name: MockForkSavedWorkflowMetadata.name,
	description: MockForkSavedWorkflowMetadata.description,
	script: MockForkWorkflowScript,
	argsSchema: JSON.stringify(
		MockForkSavedWorkflowMetadata.args_schema,
		null,
		2,
	),
	phases: MockForkSavedWorkflowMetadata.phases.join("\n"),
};

const meta = {
	title: "pages/AISettingsPage/ForkWorkflowsPage/ForkSavedWorkflowEditorDialog",
	component: ForkSavedWorkflowEditorDialog,
	args: {
		mode: "create",
		initialValues: emptySavedWorkflowFormValues,
		isSubmitting: false,
		onSubmit: fn(),
		onClose: fn(),
	},
} satisfies Meta<typeof ForkSavedWorkflowEditorDialog>;

export default meta;
type Story = StoryObj<typeof ForkSavedWorkflowEditorDialog>;

export const Create: Story = {};

export const Edit: Story = {
	args: {
		mode: "edit",
		initialValues: mockEditValues,
	},
};

export const Submitting: Story = {
	args: { ...Edit.args, isSubmitting: true },
};

export const SubmitError: Story = {
	args: {
		initialValues: {
			...mockEditValues,
			script: "def main(args):\n    log('broken')\n",
		},
		submitError: {
			message: "Invalid saved workflow.",
			detail: "the script must define main(args)",
		},
	},
};

export const Loading: Story = {
	args: { mode: "edit", initialValues: undefined, isLoading: true },
};

export const LoadError: Story = {
	args: {
		mode: "edit",
		initialValues: undefined,
		loadError: mockApiError({ message: "Saved workflow not found." }),
		onRetry: fn(),
	},
};

export const LoadErrorWithoutRetry: Story = {
	args: {
		mode: "edit",
		initialValues: undefined,
		loadError: mockApiError({ message: "Saved workflow not found." }),
	},
};

export const InvalidArgsSchema: Story = {
	args: {
		initialValues: {
			...mockEditValues,
			argsSchema: "{ not json",
		},
	},
};

export const Retrying: Story = {
	args: { ...LoadError.args, isRetrying: true },
};
