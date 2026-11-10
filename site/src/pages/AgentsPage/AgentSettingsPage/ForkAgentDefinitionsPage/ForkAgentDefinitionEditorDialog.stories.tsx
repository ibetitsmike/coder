import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { emptyAgentDefinitionFormValues } from "./agentDefinitionMarkdown";
import { ForkAgentDefinitionEditorDialog } from "./ForkAgentDefinitionEditorDialog";

const meta = {
	title: "pages/AgentsPage/ForkAgentDefinitionEditorDialog",
	component: ForkAgentDefinitionEditorDialog,
	args: {
		mode: "create",
		initialValues: emptyAgentDefinitionFormValues,
		isSubmitting: false,
		onSubmit: fn(),
		onClose: fn(),
	},
} satisfies Meta<typeof ForkAgentDefinitionEditorDialog>;

export default meta;
type Story = StoryObj<typeof ForkAgentDefinitionEditorDialog>;

export const Create: Story = {};

export const Edit: Story = {
	args: {
		mode: "edit",
		initialValues: {
			name: "code-reviewer",
			description: "Reviews a diff for correctness and test gaps.",
			model: "gpt-5",
			body: "Review the diff and report bugs with file and line references.",
			extraFrontMatter: "tools:\n  - Read\n  - Grep",
		},
	},
};

export const SubmitError: Story = {
	args: {
		initialValues: {
			...emptyAgentDefinitionFormValues,
			name: "code-reviewer",
			body: "Review the diff.",
		},
		submitError: {
			message: "Invalid agent definition content.",
			detail: 'front-matter field "readonly" must be a boolean',
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
		loadError: mockApiError({ message: "Agent definition not found." }),
		onRetry: fn(),
	},
};

export const Retrying: Story = {
	args: { ...LoadError.args, isRetrying: true },
};
