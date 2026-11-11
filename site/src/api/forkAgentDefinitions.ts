// This file is fork code: the fork-agent-definitions experiment's API calls
// live here so api.ts, an upstream file, stays unchanged.

import { API, getURLWithSearchParams } from "./api";
import type {
	ACLAvailable,
	CreateForkAgentDefinitionRequest,
	ForkAgentDefinition,
	ForkAgentDefinitionMetadata,
	ForkAgentType,
	OrganizationSkillACL,
	UpdateForkAgentDefinitionRequest,
	UpdateOrganizationSkillACLRequest,
	UsersRequest,
} from "./typesGenerated";

const userAgentDefinitionsPath = (organizationId: string, user: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/members/${user}/agent-definitions`;
const userAgentDefinitionPath = (
	organizationId: string,
	name: string,
	user: string,
) =>
	`${userAgentDefinitionsPath(organizationId, user)}/${encodeURIComponent(name)}`;

/** Lists a member's personal agent definitions; `user` defaults to the signed-in member. */
export const getUserAgentDefinitions = async (
	organizationId: string,
	user = "me",
): Promise<ForkAgentDefinitionMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkAgentDefinitionMetadata[]
	>(userAgentDefinitionsPath(organizationId, user));
	return response.data;
};

/** Gets one of a member's personal agent definitions with its content; `user` defaults to the signed-in member. */
export const getUserAgentDefinition = async (
	organizationId: string,
	name: string,
	user = "me",
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().get<ForkAgentDefinition>(
		userAgentDefinitionPath(organizationId, name, user),
	);
	return response.data;
};

/** Creates a personal agent definition for a member; `user` defaults to the signed-in member. */
export const createUserAgentDefinition = async (
	organizationId: string,
	req: CreateForkAgentDefinitionRequest,
	user = "me",
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().post<ForkAgentDefinition>(
		userAgentDefinitionsPath(organizationId, user),
		req,
	);
	return response.data;
};

/** Updates one of a user's personal agent definitions. */
export const updateUserAgentDefinition = async (
	organizationId: string,
	name: string,
	req: UpdateForkAgentDefinitionRequest,
	user = "me",
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().patch<ForkAgentDefinition>(
		userAgentDefinitionPath(organizationId, name, user),
		req,
	);
	return response.data;
};

/** Deletes one of a user's personal agent definitions. */
export const deleteUserAgentDefinition = async (
	organizationId: string,
	name: string,
	user = "me",
): Promise<void> => {
	await API.getAxiosInstance().delete(
		userAgentDefinitionPath(organizationId, name, user),
	);
};

const organizationAgentDefinitionsPath = (organizationId: string) =>
	`/api/v2/chats/fork/organizations/${organizationId}/agent-definitions`;
const organizationAgentDefinitionPath = (
	organizationId: string,
	name: string,
) =>
	`${organizationAgentDefinitionsPath(organizationId)}/${encodeURIComponent(name)}`;

/** Lists the organization agent definitions the signed-in member can read. */
export const getOrganizationAgentDefinitions = async (
	organizationId: string,
): Promise<ForkAgentDefinitionMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkAgentDefinitionMetadata[]
	>(organizationAgentDefinitionsPath(organizationId));
	return response.data;
};

/** Gets one organization agent definition with its content. */
export const getOrganizationAgentDefinition = async (
	organizationId: string,
	name: string,
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().get<ForkAgentDefinition>(
		organizationAgentDefinitionPath(organizationId, name),
	);
	return response.data;
};

/** Creates an organization agent definition; organization admins only. */
export const createOrganizationAgentDefinition = async (
	organizationId: string,
	req: CreateForkAgentDefinitionRequest,
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().post<ForkAgentDefinition>(
		organizationAgentDefinitionsPath(organizationId),
		req,
	);
	return response.data;
};

/** Updates an organization agent definition; organization admins only. */
export const updateOrganizationAgentDefinition = async (
	organizationId: string,
	name: string,
	req: UpdateForkAgentDefinitionRequest,
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().patch<ForkAgentDefinition>(
		organizationAgentDefinitionPath(organizationId, name),
		req,
	);
	return response.data;
};

/** Deletes an organization agent definition; organization admins only. */
export const deleteOrganizationAgentDefinition = async (
	organizationId: string,
	name: string,
): Promise<void> => {
	await API.getAxiosInstance().delete(
		organizationAgentDefinitionPath(organizationId, name),
	);
};

const organizationAgentDefinitionACLPath = (
	organizationId: string,
	name: string,
) => `${organizationAgentDefinitionPath(organizationId, name)}/acl`;

/** Gets the members and groups an organization agent definition is shared with; requires share access. */
export const getOrganizationAgentDefinitionACL = async (
	organizationId: string,
	name: string,
): Promise<OrganizationSkillACL> => {
	const response = await API.getAxiosInstance().get<OrganizationSkillACL>(
		organizationAgentDefinitionACLPath(organizationId, name),
	);
	return response.data;
};

/** Searches the members and groups an organization agent definition can be shared with. */
export const getOrganizationAgentDefinitionACLAvailable = async (
	organizationId: string,
	name: string,
	options: UsersRequest,
): Promise<ACLAvailable> => {
	const response = await API.getAxiosInstance().get<ACLAvailable>(
		getURLWithSearchParams(
			`${organizationAgentDefinitionACLPath(organizationId, name)}/available`,
			options,
		),
	);
	return response.data;
};

/** Grants or revokes read access for the listed members and groups, leaving others unchanged. */
export const updateOrganizationAgentDefinitionACL = async (
	organizationId: string,
	name: string,
	req: UpdateOrganizationSkillACLRequest,
): Promise<void> => {
	await API.getAxiosInstance().patch(
		organizationAgentDefinitionACLPath(organizationId, name),
		req,
	);
};

const projectAgentDefinitionsPath = (
	organizationId: string,
	projectId: string,
) =>
	`/api/v2/chats/fork/organizations/${organizationId}/projects/${projectId}/agent-definitions`;
const projectAgentDefinitionPath = (
	organizationId: string,
	projectId: string,
	name: string,
) =>
	`${projectAgentDefinitionsPath(organizationId, projectId)}/${encodeURIComponent(name)}`;

/** Lists a project's agent definitions; anyone who can read the project may. */
export const getProjectAgentDefinitions = async (
	organizationId: string,
	projectId: string,
): Promise<ForkAgentDefinitionMetadata[]> => {
	const response = await API.getAxiosInstance().get<
		ForkAgentDefinitionMetadata[]
	>(projectAgentDefinitionsPath(organizationId, projectId));
	return response.data;
};

/** Gets one project agent definition with its content. */
export const getProjectAgentDefinition = async (
	organizationId: string,
	projectId: string,
	name: string,
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().get<ForkAgentDefinition>(
		projectAgentDefinitionPath(organizationId, projectId, name),
	);
	return response.data;
};

/** Creates a project agent definition; requires update access to the project. */
export const createProjectAgentDefinition = async (
	organizationId: string,
	projectId: string,
	req: CreateForkAgentDefinitionRequest,
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().post<ForkAgentDefinition>(
		projectAgentDefinitionsPath(organizationId, projectId),
		req,
	);
	return response.data;
};

/** Updates a project agent definition; requires update access to the project. */
export const updateProjectAgentDefinition = async (
	organizationId: string,
	projectId: string,
	name: string,
	req: UpdateForkAgentDefinitionRequest,
): Promise<ForkAgentDefinition> => {
	const response = await API.getAxiosInstance().patch<ForkAgentDefinition>(
		projectAgentDefinitionPath(organizationId, projectId, name),
		req,
	);
	return response.data;
};

/** Deletes a project agent definition; requires update access to the project. */
export const deleteProjectAgentDefinition = async (
	organizationId: string,
	projectId: string,
	name: string,
): Promise<void> => {
	await API.getAxiosInstance().delete(
		projectAgentDefinitionPath(organizationId, projectId, name),
	);
};

/** Narrows the agent types to those a chat in a project or workspace, on a model, can use. */
export type AgentTypesParams = {
	project?: string;
	workspace?: string;
	model_config_id?: string;
};

/** Lists the agent types a new chat in an organization can start as or spawn. */
export const getAgentTypes = async (
	organizationId: string,
	params: AgentTypesParams = {},
): Promise<ForkAgentType[]> => {
	const response = await API.getAxiosInstance().get<ForkAgentType[]>(
		getURLWithSearchParams(
			`/api/v2/chats/fork/organizations/${organizationId}/agent-types`,
			params,
		),
	);
	return response.data;
};
