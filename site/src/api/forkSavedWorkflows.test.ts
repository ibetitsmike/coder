// This file is fork code: covers the query strings of the conditional
// saved workflow writes.

import { describe, expect, it, vi } from "vitest";
import {
	deleteUserSavedWorkflow,
	updateUserSavedWorkflow,
} from "./forkSavedWorkflows";

vi.mock("./api", async (importOriginal) => ({
	...(await importOriginal<typeof import("./api")>()),
	API: {
		...vi.fn(),
		getAxiosInstance: () => ({
			patch: vi.fn().mockResolvedValue({ data: {} }),
			delete: vi.fn().mockResolvedValue({ data: {} }),
		}),
	},
}));

describe("conditional saved workflow writes", () => {
	it("rejects an empty expect_id instead of dropping the guard", async () => {
		await expect(
			updateUserSavedWorkflow(
				"org-1",
				"deploy",
				{ description: "x" },
				{ expect_id: "" } as never,
			),
		).rejects.toThrow("expect_id must be a UUID.");
		await expect(
			deleteUserSavedWorkflow("org-1", "deploy", { expect_id: "" }),
		).rejects.toThrow("expect_id must be a UUID.");
	});
});