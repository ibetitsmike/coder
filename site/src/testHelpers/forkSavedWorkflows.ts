import type { ForkSavedWorkflowSchema } from "#/api/forkSavedWorkflowSchema";
import type {
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
} from "#/api/typesGenerated";
import { MOCK_TIMESTAMP } from "./chatEntities";
import {
	MockDefaultOrganization,
	MockUserMember,
	MockUserOwner,
} from "./entities";
import { MockForkWorkflowScript } from "./forkWorkflows";

export const MockForkSavedWorkflowMetadata: ForkSavedWorkflowMetadata = {
	id: "saved-workflow-1",
	organization_id: MockDefaultOrganization.id,
	// A personal workflow has an owner, which an organization workflow
	// leaves empty.
	user_id: MockUserOwner.id,
	created_by: MockUserOwner.id,
	name: "review-files",
	description: "Reviews a list of files for bugs with parallel agents.",
	args_schema: {
		type: "object",
		properties: { files: { type: "array" } },
		required: ["files"],
	} as ForkSavedWorkflowSchema,
	phases: ["review", "summarize"],
	created_at: MOCK_TIMESTAMP,
	updated_at: MOCK_TIMESTAMP,
	permissions: { update: true, delete: true, share: true },
};

export const MockForkSavedWorkflow: ForkSavedWorkflow = {
	...MockForkSavedWorkflowMetadata,
	script: MockForkWorkflowScript,
};

/** An organization workflow, which has no owner. */
export const MockForkOrganizationSavedWorkflowMetadata: ForkSavedWorkflowMetadata =
	{
		...MockForkSavedWorkflowMetadata,
		id: "saved-workflow-org",
		name: "rotate-keys",
		description: "Rotates the API keys of a deployment.",
		user_id: undefined,
		created_by: undefined,
		args_schema: undefined,
		phases: [],
		// The server grants no rights on this fixture by default.
		permissions: { update: false, delete: false, share: false },
	};

/** MockForkSavedWorkflowMetadata after MockUserMember shared it. */
export const MockForkSharedSavedWorkflowMetadata: ForkSavedWorkflowMetadata = {
	...MockForkSavedWorkflowMetadata,
	id: "saved-workflow-shared",
	user_id: undefined,
	created_by: MockUserMember.id,
	// Sharing grants the author edit and delete, not access management.
	permissions: { update: true, delete: true, share: false },
};
