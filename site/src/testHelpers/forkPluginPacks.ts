// This file is fork code: mock entities for the fork-plugin-packs experiment.

import type { ForkPluginPack } from "#/api/typesGenerated";
import { MockOrganization, MockUserOwner } from "./entities";

export const MockForkPluginPack: ForkPluginPack = {
	id: "8d2b4c2e-6f3a-4c55-9d43-4a0d7a5e9b11",
	organization_id: MockOrganization.id,
	name: "pstack",
	source_url: "https://git.example.com/team/plugins.git",
	source_path: "pstack",
	ref: "v0.15.36",
	commit_sha: "0123456789abcdef0123456789abcdef01234567",
	created_by: MockUserOwner.id,
	created_at: "2026-11-10T12:00:00Z",
	updated_at: "2026-11-11T12:00:00Z",
};

export const MockForkPluginPackWithoutPath: ForkPluginPack = {
	...MockForkPluginPack,
	id: "2f6c1a9e-0b7d-4e8a-a3c5-7d9e1f2b4c60",
	name: "review",
	source_url: "https://git.example.com/team/review-pack.git",
	source_path: "",
	ref: "main",
};
