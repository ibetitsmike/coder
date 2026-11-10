import type { ForkAgentDefinitionMetadata } from "#/api/typesGenerated";
import { MOCK_TIMESTAMP } from "./chatEntities";
import { MockDefaultOrganization } from "./entities";

export const MockForkAgentDefinitionMetadata: ForkAgentDefinitionMetadata = {
	id: "agent-definition-1",
	organization_id: MockDefaultOrganization.id,
	name: "code-reviewer",
	description: "Reviews a diff for correctness and test gaps.",
	enabled: true,
	created_at: MOCK_TIMESTAMP,
	updated_at: MOCK_TIMESTAMP,
};
