import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn, screen, userEvent, within } from "storybook/test";
import { mockApiError } from "#/testHelpers/entities";
import { SKILL_MAX_SIZE_BYTES } from "../../utils/skills";
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
			frontMatter:
				"name: code-reviewer\ndescription: Reviews a diff.\nmodel: gpt-5\ntools:\n  - Read\n  - Grep",
		},
	},
};

export const Submitting: Story = {
	args: { ...Edit.args, isSubmitting: true },
};

export const ImportError: Story = {
	play: async () => {
		const dialog = within(await screen.findByRole("dialog"));
		await userEvent.upload(
			dialog.getByLabelText("Upload agent definition file"),
			new File(["Review the diff."], "reviewer.md", { type: "text/markdown" }),
		);
		await dialog.findByText("The file must start with --- front-matter.");
	},
};

export const SizeError: Story = {
	args: {
		initialValues: {
			...emptyAgentDefinitionFormValues,
			name: "code-reviewer",
			body: "a".repeat(SKILL_MAX_SIZE_BYTES),
		},
	},
	play: async () => {
		const dialog = within(await screen.findByRole("dialog"));
		await userEvent.click(dialog.getByLabelText("Body"));
		await userEvent.tab();
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

export const LoadErrorWithoutRetry: Story = {
	args: {
		mode: "edit",
		initialValues: undefined,
		loadError: new Error("This agent definition no longer exists."),
	},
};

export const Retrying: Story = {
	args: { ...LoadError.args, isRetrying: true },
};
