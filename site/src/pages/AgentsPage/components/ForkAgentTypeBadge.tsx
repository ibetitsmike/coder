import type { Chat } from "#/api/typesGenerated";
import { Badge } from "#/components/Badge/Badge";

/**
 * Shows the agent type of a child chat. The API sets agent_type only with
 * the fork-agent-definitions experiment, and general, the default type, has
 * no badge.
 */
export const ForkAgentTypeBadge: React.FC<{
	chat: Pick<Chat, "agent_type">;
}> = ({ chat }) => {
	if (!chat.agent_type || chat.agent_type === "general") {
		return null;
	}
	return (
		<Badge size="sm" className="shrink-0" title="Agent type">
			{chat.agent_type}
		</Badge>
	);
};
