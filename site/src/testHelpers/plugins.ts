import type {
	LinkablePlugin,
	PluginConnectPrompt,
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
	connect: false,
	links: [MockPluginUserLink],
};

export const MockPluginLinkCode: PluginLinkCode = {
	code: "K7Q2-M9XD",
	expires_at: "2024-01-01T00:10:00Z",
};

export const MockPluginConnectPrompt: PluginConnectPrompt = {
	// A 25 by 25 pixel PNG that looks like a QR code but encodes nothing.
	qr_code_png:
		"iVBORw0KGgoAAAANSUhEUgAAABkAAAAZCAAAAADhgtq/AAAAfElEQVR42o1SARKAIAzi/59edyIbVHbNO02RDZaoU+ALAaNsBog0GWuIsPYAycT2zhCxXhCujkydyjrSxkqmbQzg4acRv61sNSUlh5/Vl6EcGB7lplg/Ck70XY7dj/qx1VpaDAHZt32ifxGIr4GYwVuvW4q7tBeAP2/nEBf1Xo2BNoGF2QAAAABJRU5ErkJggg==",
	instructions:
		"In Signal on your phone, open Settings > Linked devices, select Link a new device and scan this code. Then send the /link command below to Note to Self.",
};
