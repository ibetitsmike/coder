import type { Chat } from "#/api/typesGenerated";
import { Badge } from "#/components/Badge/Badge";

type ForkAgentTypeBadgeProps = {
	readonly chat: Pick<Chat, "agent_type">;
};

/**
 * The API sets agent_type only with the fork-agent-definitions experiment,
 * and general, the default type, gets no badge.
 */
export const ForkAgentTypeBadge: React.FC<ForkAgentTypeBadgeProps> = ({
	chat,
}) => {
	if (!chat.agent_type || chat.agent_type === "general") {
		return null;
	}
	return (
		<Badge size="sm" className="shrink-0">
			<span className="sr-only">Agent type: </span>
			{chat.agent_type}
		</Badge>
	);
};
