// This file is fork code: the fork-workflows experiment's API calls live
// here so api.ts, an upstream file, stays unchanged.

import { API, getURLWithSearchParams } from "./api";
import type { ForkSavedWorkflowSchema } from "./forkSavedWorkflowSchema";
import type {
	ACLAvailable,
	CreateForkSavedWorkflowRequest,
	ForkSavedWorkflow,
	ForkSavedWorkflowMetadata,
	OrganizationSkillACL,
	UpdateForkSavedWorkflowRequest,
	UpdateOrganizationSkillACLRequest,
	UsersRequest,
} from "./typesGenerated";

/**
 * The same update request as the generated type, with args_schema widened
 * to null, which removes the schema where an absent key leaves it.
 */
export type UpdateSavedWorkflowRequest = Omit<
	UpdateForkSavedWorkflowRequest,
	"args_schema"
> & { args_schema?: ForkSavedWorkflowSchema | null };

// The server resolves "me" to the signed-in member.
const userSavedWorkflowsPath = (organizationId: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/members/me/workflows`;
const userSavedWorkflowPath = (organizationId: string, name: string) =>
	`${userSavedWorkflowsPath(organizationId)}/${encodeURIComponent(name)}`;

/** Lists the signed-in member's personal saved workflows. */
export const getUserSavedWorkflows = async (
	organizationId: string,
): Promise<ForkSavedWorkflowMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkSavedWorkflowMetadata[]
	>(userSavedWorkflowsPath(organizationId));
	return response.data;
};

/** Gets one of the signed-in member's personal saved workflows with its script. */
export const getUserSavedWorkflow = async (
	organizationId: string,
	name: string,
): Promise<ForkSavedWorkflow> => {
	const response = await API.getAxiosInstance().get<ForkSavedWorkflow>(
		userSavedWorkflowPath(organizationId, name),
	);
	return response.data;
};

/** Creates a personal saved workflow for the signed-in member. */
export const createUserSavedWorkflow = async (
	organizationId: string,
	req: CreateForkSavedWorkflowRequest,
): Promise<ForkSavedWorkflow> => {
	const response = await API.getAxiosInstance().post<ForkSavedWorkflow>(
		userSavedWorkflowsPath(organizationId),
		req,
	);
	return response.data;
};

/** Updates one of the signed-in member's personal saved workflows. */
export const updateUserSavedWorkflow = async (
	organizationId: string,
	name: string,
	req: UpdateSavedWorkflowRequest,
): Promise<ForkSavedWorkflow> => {
	const response = await API.getAxiosInstance().patch<ForkSavedWorkflow>(
		userSavedWorkflowPath(organizationId, name),
		req,
	);
	return response.data;
};

/** Deletes one of the signed-in member's personal saved workflows. */
export const deleteUserSavedWorkflow = async (
	organizationId: string,
	name: string,
): Promise<void> => {
	await API.getAxiosInstance().delete(
		userSavedWorkflowPath(organizationId, name),
	);
};

const organizationSavedWorkflowsPath = (organizationId: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/workflows`;
const organizationSavedWorkflowPath = (organizationId: string, name: string) =>
	`${organizationSavedWorkflowsPath(organizationId)}/${encodeURIComponent(name)}`;

/** Lists the organization saved workflows the signed-in member can read. */
export const getOrganizationSavedWorkflows = async (
	organizationId: string,
): Promise<ForkSavedWorkflowMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkSavedWorkflowMetadata[]
	>(organizationSavedWorkflowsPath(organizationId));
	return response.data;
};

/** Gets one organization saved workflow with its script. */
export const getOrganizationSavedWorkflow = async (
	organizationId: string,
	name: string,
): Promise<ForkSavedWorkflow> => {
	const response = await API.getAxiosInstance().get<ForkSavedWorkflow>(
		organizationSavedWorkflowPath(organizationId, name),
	);
	return response.data;
};

/** Creates an organization saved workflow; requires the create permission. */
export const createOrganizationSavedWorkflow = async (
	organizationId: string,
	req: CreateForkSavedWorkflowRequest,
): Promise<ForkSavedWorkflow> => {
	const response = await API.getAxiosInstance().post<ForkSavedWorkflow>(
		organizationSavedWorkflowsPath(organizationId),
		req,
	);
	return response.data;
};

/** Updates an organization saved workflow; requires the update permission. */
export const updateOrganizationSavedWorkflow = async (
	organizationId: string,
	name: string,
	req: UpdateSavedWorkflowRequest,
): Promise<ForkSavedWorkflow> => {
	const response = await API.getAxiosInstance().patch<ForkSavedWorkflow>(
		organizationSavedWorkflowPath(organizationId, name),
		req,
	);
	return response.data;
};

/** Deletes an organization saved workflow; requires the delete permission. */
export const deleteOrganizationSavedWorkflow = async (
	organizationId: string,
	name: string,
): Promise<void> => {
	await API.getAxiosInstance().delete(
		organizationSavedWorkflowPath(organizationId, name),
	);
};

const organizationSavedWorkflowACLPath = (
	organizationId: string,
	name: string,
) => `${organizationSavedWorkflowPath(organizationId, name)}/acl`;

/** Gets the members and groups an organization saved workflow is shared with; requires share access. */
export const getOrganizationSavedWorkflowACL = async (
	organizationId: string,
	name: string,
): Promise<OrganizationSkillACL> => {
	const response = await API.getAxiosInstance().get<OrganizationSkillACL>(
		organizationSavedWorkflowACLPath(organizationId, name),
	);
	return response.data;
};

/** Searches the members and groups an organization saved workflow can be shared with. */
export const getOrganizationSavedWorkflowACLAvailable = async (
	organizationId: string,
	name: string,
	options: UsersRequest,
): Promise<ACLAvailable> => {
	const response = await API.getAxiosInstance().get<ACLAvailable>(
		getURLWithSearchParams(
			`${organizationSavedWorkflowACLPath(organizationId, name)}/available`,
			options,
		),
	);
	return response.data;
};

/** Grants or revokes read access for the listed members and groups, leaving others unchanged. */
export const updateOrganizationSavedWorkflowACL = async (
	organizationId: string,
	name: string,
	req: UpdateOrganizationSkillACLRequest,
): Promise<void> => {
	await API.getAxiosInstance().patch(
		organizationSavedWorkflowACLPath(organizationId, name),
		req,
	);
};
