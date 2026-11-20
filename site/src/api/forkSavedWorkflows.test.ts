// This file is fork code: covers the query strings of the conditional
// saved workflow writes, for the personal and the organization routes.

import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	deleteOrganizationSavedWorkflow,
	deleteUserSavedWorkflow,
	updateOrganizationSavedWorkflow,
	updateUserSavedWorkflow,
} from "./forkSavedWorkflows";
import type * as forkSavedWorkflowsModule from "./forkSavedWorkflows";

const axiosSpies = vi.hoisted(() => ({
	patch: vi.fn().mockResolvedValue({ data: {} }),
	delete: vi.fn().mockResolvedValue({ data: {} }),
}));

vi.mock("./api", async (importOriginal) => ({
	...(await importOriginal<typeof forkSavedWorkflowsModule>()),
	API: {
		...vi.fn(),
		getAxiosInstance: () => axiosSpies,
	},
}));

const families = [
	{
		tier: "personal",
		path: "organizations/org-1/members/me/workflows",
		update: updateUserSavedWorkflow,
		remove: deleteUserSavedWorkflow,
	},
	{
		tier: "organization",
		path: "organizations/org-1/workflows",
		update: updateOrganizationSavedWorkflow,
		remove: deleteOrganizationSavedWorkflow,
	},
] as const;

describe("conditional saved workflow writes", () => {
	beforeEach(() => {
		axiosSpies.patch.mockClear();
		axiosSpies.delete.mockClear();
	});

	it.each(families)(
		"sends expect_id on the $tier patch and delete URLs",
		async ({ path, update, remove }) => {
			const id = "8b5d2f5e-3a49-4f57-9c1a-6f5a7d3b2e10";
			await update("org-1", "deploy", { description: "x" }, { expect_id: id });
			expect(axiosSpies.patch).toHaveBeenCalledTimes(1);
			expect(axiosSpies.patch.mock.calls[0][0]).toContain(
				`/api/v2/chats/fork/${path}/deploy?expect_id=${id}`,
			);
			await remove("org-1", "deploy", { expect_id: id });
			expect(axiosSpies.delete).toHaveBeenCalledTimes(1);
			expect(axiosSpies.delete.mock.calls[0][0]).toContain(
				`/api/v2/chats/fork/${path}/deploy?expect_id=${id}`,
			);
		},
	);

	it("sends no expect_id when the write stays unconditional", async () => {
		await updateUserSavedWorkflow("org-1", "deploy", { description: "x" });
		expect(axiosSpies.patch.mock.calls[0][0]).not.toContain("expect_id");
		// The empty params object is the generated contract for an
		// unconditional write, so it must not fail.
		await updateUserSavedWorkflow("org-1", "deploy", { description: "x" }, {});
		expect(axiosSpies.patch.mock.calls[1][0]).not.toContain("expect_id");
	});

	it("rejects an empty expect_id instead of dropping the guard", async () => {
		await expect(
			updateUserSavedWorkflow(
				"org-1",
				"deploy",
				{ description: "x" },
				{ expect_id: "" },
			),
		).rejects.toThrow("expect_id must be a UUID.");
		await expect(
			deleteUserSavedWorkflow("org-1", "deploy", { expect_id: "" }),
		).rejects.toThrow("expect_id must be a UUID.");
		expect(axiosSpies.patch).not.toHaveBeenCalled();
		expect(axiosSpies.delete).not.toHaveBeenCalled();
	});
});