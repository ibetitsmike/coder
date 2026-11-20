// This file is fork code: covers the query strings of the conditional
// saved workflow writes.

import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	deleteUserSavedWorkflow,
	updateUserSavedWorkflow,
} from "./forkSavedWorkflows";

const axiosSpies = vi.hoisted(() => ({
	patch: vi.fn().mockResolvedValue({ data: {} }),
	delete: vi.fn().mockResolvedValue({ data: {} }),
}));

vi.mock("./api", async (importOriginal) => ({
	...(await importOriginal<typeof import("./api")>()),
	API: {
		...vi.fn(),
		getAxiosInstance: () => axiosSpies,
	},
}));

describe("conditional saved workflow writes", () => {
	beforeEach(() => {
		axiosSpies.patch.mockClear();
		axiosSpies.delete.mockClear();
	});

	it("sends expect_id on the patch and delete URLs", async () => {
		const id = "8b5d2f5e-3a49-4f57-9c1a-6f5a7d3b2e10";
		await updateUserSavedWorkflow(
			"org-1",
			"deploy",
			{ description: "x" },
			{ expect_id: id },
		);
		expect(axiosSpies.patch).toHaveBeenCalledTimes(1);
		expect(axiosSpies.patch.mock.calls[0][0]).toContain(
			`/api/v2/chats/fork/organizations/org-1/members/me/workflows/deploy?expect_id=${id}`,
		);
		await deleteUserSavedWorkflow("org-1", "deploy", { expect_id: id });
		expect(axiosSpies.delete).toHaveBeenCalledTimes(1);
		expect(axiosSpies.delete.mock.calls[0][0]).toContain(
			`/api/v2/chats/fork/organizations/org-1/members/me/workflows/deploy?expect_id=${id}`,
		);
	});

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
