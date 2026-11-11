import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http, type PathParams } from "msw";
import { QueryClientProvider } from "react-query";
import { describe, expect, it, vi } from "vitest";
import * as forkAgentDefinitionsApi from "#/api/forkAgentDefinitions";
import type { UpdateOrganizationSkillACLRequest } from "#/api/typesGenerated";
import {
	MockGroup,
	MockMCPServerConfigACLAvailable,
	MockOrganization,
	MockOrganizationSkillACL,
	MockUserOwner,
	mockApiError,
} from "#/testHelpers/entities";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { server } from "#/testHelpers/server";
import { ForkOrganizationAgentDefinitionSharingDialog } from "./ForkOrganizationAgentDefinitionSharingDialog";

const aclPath =
	"/api/v2/chats/fork/organizations/:organization/agent-definitions/:agentDefinition/acl";

describe("ForkOrganizationAgentDefinitionSharingDialog", () => {
	it("saves only the changed entries", async () => {
		const updates: unknown[] = [];
		server.use(
			http.get(aclPath, () => HttpResponse.json(MockOrganizationSkillACL)),
			http.get(`${aclPath}/available`, () =>
				HttpResponse.json(MockMCPServerConfigACLAvailable),
			),
			http.patch<PathParams, UpdateOrganizationSkillACLRequest>(
				aclPath,
				async ({ params, request }) => {
					updates.push({ ...params, body: await request.json() });
					return new HttpResponse(null, { status: 204 });
				},
			),
		);
		const onClose = vi.fn();
		const user = userEvent.setup();
		renderComponent(
			<QueryClientProvider client={createTestQueryClient()}>
				<ForkOrganizationAgentDefinitionSharingDialog
					organizationId={MockOrganization.id}
					definitionName="reviewer"
					onClose={onClose}
				/>
			</QueryClientProvider>,
		);

		await user.click(
			await screen.findByRole("button", {
				name: `Remove ${MockGroup.display_name}`,
			}),
		);
		await user.click(
			screen.getByRole("button", { name: "Search for user or group" }),
		);
		await user.type(
			screen.getByPlaceholderText("Search for user or group"),
			MockUserOwner.email,
		);
		await user.click(
			await screen.findByRole("option", {
				name: new RegExp(MockUserOwner.email, "i"),
			}),
		);
		await user.click(screen.getByRole("button", { name: "Add member" }));
		await user.click(screen.getByRole("button", { name: "Save permissions" }));

		await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
		expect(updates).toStrictEqual([
			{
				organization: MockOrganization.id,
				agentDefinition: "reviewer",
				body: {
					user_roles: { [MockUserOwner.id]: "read" },
					group_roles: { [MockGroup.id]: "" },
				},
			},
		]);
	});

	it("keeps the dialog open when saving fails", async () => {
		server.use(
			http.get(aclPath, () => HttpResponse.json(MockOrganizationSkillACL)),
		);
		const updateACL = vi
			.spyOn(forkAgentDefinitionsApi, "updateOrganizationAgentDefinitionACL")
			.mockRejectedValue(
				mockApiError({
					message: "Failed to save agent definition permissions.",
				}),
			);
		const queryClient = createTestQueryClient();
		const onClose = vi.fn();
		const user = userEvent.setup();
		renderComponent(
			<QueryClientProvider client={queryClient}>
				<ForkOrganizationAgentDefinitionSharingDialog
					organizationId={MockOrganization.id}
					definitionName="reviewer"
					onClose={onClose}
				/>
			</QueryClientProvider>,
		);

		await user.click(
			await screen.findByRole("button", {
				name: `Remove ${MockGroup.display_name}`,
			}),
		);
		await user.click(screen.getByRole("button", { name: "Save permissions" }));

		await waitFor(() =>
			expect(queryClient.getMutationCache().getAll()[0]?.state.status).toBe(
				"error",
			),
		);
		expect(updateACL).toHaveBeenCalledWith(MockOrganization.id, "reviewer", {
			group_roles: { [MockGroup.id]: "" },
		});
		expect(onClose).not.toHaveBeenCalled();
	});
});
