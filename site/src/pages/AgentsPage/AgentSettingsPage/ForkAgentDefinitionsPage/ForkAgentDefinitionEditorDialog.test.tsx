import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { render } from "#/testHelpers/renderHelpers";
import { emptyAgentDefinitionFormValues } from "./agentDefinitionMarkdown";
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
		expect(onSubmit.mock.calls[0][0]).toBe(
			[
				"---",
				'name: "imported"',
				'description: "Imported agent"',
				'model: "sonnet"',
				"tools:",
				"- Read",
				"---",
				"Imported body.",
				"",
			].join("\n"),
		);
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
		expect(onSubmit.mock.calls[0][0]).toMatch(/^---\nname: "reviewer"\n/);
		expect(onSubmit.mock.calls[0][0]).toContain("tools:\n- Read\n");
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
		expect(onSubmit.mock.calls[0][0]).toBe(
			'---\nname: "reviewer"\nmodel: "opus"\n---\nReview.\n',
		);
	});

	it("ignores Escape while a save is in flight", async () => {
		const user = userEvent.setup();
		const { onClose } = renderDialog({ isSubmitting: true });

		await user.keyboard("{Escape}");

		expect(onClose).not.toHaveBeenCalled();
	});
});
