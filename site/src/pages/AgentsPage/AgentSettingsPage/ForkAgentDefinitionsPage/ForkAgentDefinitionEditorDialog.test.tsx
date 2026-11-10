import { render as renderRTL, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { render } from "#/testHelpers/renderHelpers";
import {
	type AgentDefinitionFormValues,
	emptyAgentDefinitionFormValues,
} from "./agentDefinitionMarkdown";
import { ForkAgentDefinitionEditorDialog } from "./ForkAgentDefinitionEditorDialog";

const importedFile = new File(
	[
		[
			"---",
			"name: imported",
			"description: Imported agent",
			"model: sonnet",
			"tools:",
			"- Read",
			"---",
			"Imported body.",
			"",
		].join("\n"),
	],
	"imported.md",
	{ type: "text/markdown" },
);

const editedValues: AgentDefinitionFormValues = {
	name: "reviewer",
	description: "Reviews diffs.",
	model: "sonnet",
	body: "Review the diff.",
	frontMatter:
		"name: reviewer\ndescription: Reviews diffs.\nmodel: sonnet\ntools: Read",
};

const renderDialog = (
	props: Partial<React.ComponentProps<typeof ForkAgentDefinitionEditorDialog>>,
) => {
	const onSubmit = vi.fn();
	const onClose = vi.fn();
	render(
		<ForkAgentDefinitionEditorDialog
			mode="create"
			initialValues={emptyAgentDefinitionFormValues}
			isSubmitting={false}
			onSubmit={onSubmit}
			onClose={onClose}
			{...props}
		/>,
	);
	return { onSubmit, onClose };
};

describe("ForkAgentDefinitionEditorDialog", () => {
	it("submits an imported file with its model and other front-matter", async () => {
		const user = userEvent.setup();
		const { onSubmit } = renderDialog({});

		await user.upload(
			screen.getByLabelText("Upload agent definition file"),
			importedFile,
		);
		await user.click(
			await screen.findByRole("button", { name: "Create agent definition" }),
		);

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit.mock.calls[0][0]).toEqual({
			name: "imported",
			content: [
				"---",
				"name: imported",
				"description: Imported agent",
				"model: sonnet",
				"tools:",
				"  - Read",
				"---",
				"Imported body.",
				"",
			].join("\n"),
		});
	});

	it("keeps the stored name when a file is imported while editing", async () => {
		const user = userEvent.setup();
		const { onSubmit } = renderDialog({
			mode: "edit",
			initialValues: {
				...emptyAgentDefinitionFormValues,
				name: "reviewer",
				body: "Review.",
			},
		});

		await user.upload(
			screen.getByLabelText("Upload agent definition file"),
			importedFile,
		);
		await user.click(
			await screen.findByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		const { name, content } = onSubmit.mock.calls[0][0];
		expect(name).toBe("reviewer");
		expect(content).toMatch(/^---\nname: reviewer\n/);
		expect(content).toContain("tools:\n  - Read\n");
	});

	it("saves a change to only the model", async () => {
		const user = userEvent.setup();
		const { onSubmit } = renderDialog({
			mode: "edit",
			initialValues: {
				...emptyAgentDefinitionFormValues,
				name: "reviewer",
				body: "Review.",
			},
		});

		await user.type(screen.getByLabelText("Model"), "opus");
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit.mock.calls[0][0].content).toBe(
			"---\nname: reviewer\nmodel: opus\n---\nReview.\n",
		);
	});

	it("validates the size again when only the model changes", async () => {
		const user = userEvent.setup();
		const header = "---\nname: reviewer\n---\n";
		// One byte under the limit with the trailing newline, before the edit.
		const body = "a".repeat(64 * 1024 - header.length - 2);
		const { onSubmit } = renderDialog({
			mode: "edit",
			initialValues: {
				...emptyAgentDefinitionFormValues,
				name: "reviewer",
				body,
			},
		});
		const model = screen.getByLabelText("Model");

		await user.type(model, "opus");
		await user.type(screen.getByLabelText("Body"), "b");
		await user.clear(model);
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
	});

	it("does not submit a body that is only an HTML comment", async () => {
		const user = userEvent.setup();
		const { onSubmit } = renderDialog({});

		await user.type(screen.getByLabelText("Name"), "reviewer");
		await user.type(screen.getByLabelText("Body"), "<!-- instructions -->");
		await user.click(
			screen.getByRole("button", { name: "Create agent definition" }),
		);

		expect(onSubmit).not.toHaveBeenCalled();
	});

	it("ignores Escape while a save is in flight", async () => {
		const user = userEvent.setup();
		const { onClose } = renderDialog({ isSubmitting: true });

		await user.keyboard("{Escape}");

		expect(onClose).not.toHaveBeenCalled();
	});

	it("ignores Escape from the moment Save is clicked", async () => {
		const user = userEvent.setup();
		const { onSubmit, onClose } = renderDialog({
			mode: "edit",
			initialValues: editedValues,
		});

		await user.keyboard("{Escape}");
		expect(onClose).toHaveBeenCalledTimes(1);
		await user.type(screen.getByLabelText("Model"), "-v2");
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);
		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		await user.keyboard("{Escape}");

		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("keeps every field's opening values when initialValues refresh", async () => {
		const user = userEvent.setup();
		const onSubmit = vi.fn();
		const dialog = (initialValues: AgentDefinitionFormValues) => (
			<ForkAgentDefinitionEditorDialog
				mode="edit"
				initialValues={initialValues}
				isSubmitting={false}
				onSubmit={onSubmit}
				onClose={() => {}}
			/>
		);
		const { rerender } = renderRTL(dialog(editedValues));

		await user.type(screen.getByLabelText("Body"), " Then report.");
		rerender(
			dialog({
				...editedValues,
				description: "Changed in another tab.",
				model: "opus",
				frontMatter: "name: reviewer\ntools: Grep",
			}),
		);
		await user.click(
			screen.getByRole("button", { name: "Save agent definition" }),
		);

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit.mock.calls[0][0].content).toBe(
			[
				"---",
				"name: reviewer",
				"description: Reviews diffs.",
				"model: sonnet",
				"tools: Read",
				"---",
				"Review the diff. Then report.",
				"",
			].join("\n"),
		);
	});
});
