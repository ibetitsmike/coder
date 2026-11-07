import type {
	LinkablePlugin,
	PluginLinkCode,
	PluginUserLink,
} from "#/api/typesGenerated";
import { MOCK_TIMESTAMP } from "./chatEntities";
import { MockOrganization, MockUserOwner } from "./entities";

export const MockPluginUserLink: PluginUserLink = {
	plugin_id: "plugin-signal",
	external_id: "+15550100001",
	user_id: MockUserOwner.id,
	created_at: MOCK_TIMESTAMP,
};

export const MockLinkablePlugin: LinkablePlugin = {
	id: "plugin-signal",
	organization_id: MockOrganization.id,
	organization_name: MockOrganization.name,
	name: "signal-bridge",
	version: "1.0.0",
	chats: ["create", "read", "send"],
	links: [MockPluginUserLink],
};

export const MockPluginLinkCode: PluginLinkCode = {
	code: "K7Q2-M9XD",
	expires_at: "2024-01-01T00:10:00Z",
};
