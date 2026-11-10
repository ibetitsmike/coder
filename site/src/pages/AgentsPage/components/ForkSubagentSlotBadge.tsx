// This file is fork code: it shows in the chat top bar that a
// fork-nested-subagents subagent waits for a free concurrency slot.
// ChatTopBar.tsx renders it as one element, so upstream syncs stay small.

import { useQuery } from "react-query";
import { forkChatFamilyCapacity } from "#/api/queries/forkChatFamily";
import type { Chat, ChatStatus } from "#/api/typesGenerated";
import { Badge } from "#/components/Badge/Badge";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { getParentChatID } from "./ChatConversation/chatHelpers";
import {
	getForkFamilyRootID,
	waitingForSlotStatus,
} from "./ChatsSidebar/tree/ForkFamilyCapacity";

type ForkSubagentSlotBadgeProps = {
	readonly chat: Chat;
	readonly status: ChatStatus;
};

/**
 * The sidebar polls the family only while its root row is expanded, so the
 * top bar polls on its own to cover a collapsed or hidden sidebar.
 */
export const ForkSubagentSlotBadge: React.FC<ForkSubagentSlotBadgeProps> = ({
	chat,
	status,
}) => {
	const { experiments } = useDashboard();
	const { user } = useAuthenticated();
	const enabled =
		experiments.includes("fork-nested-subagents") &&
		chat.owner_id === user.id &&
		getParentChatID(chat) !== undefined &&
		status === "running";
	const { data } = useQuery({
		...forkChatFamilyCapacity(getForkFamilyRootID(chat)),
		enabled,
	});
	if (!enabled || !data?.waiting_for_capacity.includes(chat.id)) {
		return null;
	}
	const { icon: Icon, label } = waitingForSlotStatus;
	return (
		<Badge size="sm" variant="warning" className="shrink-0">
			<Icon />
			<span className="max-sm:sr-only">{label}</span>
		</Badge>
	);
};
