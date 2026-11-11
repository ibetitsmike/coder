import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { afterEach, expect, it, vi } from "vitest";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import { MockChatProject } from "#/testHelpers/entities";
import { MockForkAgentDefinitionMetadata } from "#/testHelpers/forkAgentDefinitions";
import { createTestQueryClient } from "#/testHelpers/renderHelpers";
import { ProjectAgentDefinitionsDialog } from "./ProjectAgentDefinitionsDialog";

const { organization_id: orgId, id: projectId } = MockChatProject;

const renderDialog = (canEdit: boolean) => {
	const onOpenChange = vi.fn();
	const getDefinitions = vi
		.spyOn(forkAgentDefinitionsApi, "getProjectAgentDefinitions")
		.mockResolvedValue([MockForkAgentDefinitionMetadata]);
	render(
		<QueryClientProvider client={createTestQueryClient()}>
			<ProjectAgentDefinitionsDialog
				project={MockChatProject}
				open
				onOpenChange={onOpenChange}
				canEdit={canEdit}
				onCloseAutoFocus={vi.fn()}
			/>
		</QueryClientProvider>,
	);
	return { user: userEvent.setup(), getDefinitions, onOpenChange };
};

afterEach(() => {
	vi.restoreAllMocks();
});

it("creates and toggles definitions through the project route", async () => {
	const create = vi
		.spyOn(forkAgentDefinitionsApi, "createProjectAgentDefinition")
		.mockResolvedValue({ ...MockForkAgentDefinitionMetadata, content: "" });
	const update = vi
		.spyOn(forkAgentDefinitionsApi, "updateProjectAgentDefinition")
		.mockResolvedValue({
			...MockForkAgentDefinitionMetadata,
			enabled: false,
			content: "",
		});
	const { user } = renderDialog(true);

	await user.click(
		await screen.findByRole("switch", {
			name: `Enable ${MockForkAgentDefinitionMetadata.name}`,
		}),
	);
	await waitFor(() =>
		expect(update).toHaveBeenCalledWith(
			orgId,
			projectId,
			MockForkAgentDefinitionMetadata.name,
			{ enabled: false },
		),
	);

	await user.click(
		screen.getByRole("button", { name: "New agent definition" }),
	);
	const editor = await screen.findByRole("dialog", {
		name: "Create agent definition",
	});
	await user.type(within(editor).getByLabelText("Name"), "planner");
	await user.type(within(editor).getByLabelText("Body"), "Plan the work.");
	await user.click(
		within(editor).getByRole("button", { name: "Create agent definition" }),
	);

	await waitFor(() =>
		expect(create).toHaveBeenCalledWith(orgId, projectId, {
			content: "---\nname: planner\n---\nPlan the work.\n",
		}),
	);
});

it("lists the project's definitions without update access and closes on Escape", async () => {
	const { user, getDefinitions, onOpenChange } = renderDialog(false);

	await waitFor(() =>
		expect(getDefinitions).toHaveBeenCalledWith(orgId, projectId),
	);
	await user.keyboard("{Escape}");
	expect(onOpenChange).toHaveBeenCalledWith(false);
});
