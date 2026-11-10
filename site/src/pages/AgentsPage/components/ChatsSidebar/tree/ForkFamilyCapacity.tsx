// This file is fork code: it shows the subagent capacity of a
// fork-nested-subagents family in the sidebar tree. ChatTreeNode.tsx calls it
// in two places, so upstream syncs of that file stay small.

import { HourglassIcon } from "lucide-react";
import { useQuery } from "react-query";
import { forkChatFamilyCapacity } from "#/api/queries/forkChatFamily";
import type { Chat, ChatStatus } from "#/api/typesGenerated";
import { Badge } from "#/components/Badge/Badge";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { asNonEmptyString } from "../../ChatConversation/blockUtils";
import {
	type ChatTree,
	collectDescendantChats,
	getParentChatID,
} from "./chatTree";

// The statuses that the server counts toward the family's live limit.
const liveStatuses: ReadonlySet<ChatStatus> = new Set<ChatStatus>([
	"running",
	"interrupting",
	"requires_action",
	"paused",
]);

/**
 * The status that marks a subagent waiting for a free concurrency slot, in
 * the sidebar row and in the chat top bar.
 */
export const waitingForSlotStatus = {
	icon: HourglassIcon,
	className: "text-content-warning",
	label: "Waiting for a free subagent slot",
};

type ForkFamilyCapacity = {
	/** Replaces the row's status icon while the subagent waits for a slot. */
	readonly status?: typeof waitingForSlotStatus;
	/** The family's live subagent count and limit, set only at the limit. */
	readonly liveLimit?: { readonly live: number; readonly max: number };
};

/** The family endpoint rejects a descendant, so the root keys the query. */
export const getForkFamilyRootID = (chat: Chat): string =>
	asNonEmptyString(chat.root_chat_id) ?? getParentChatID(chat) ?? chat.id;

/** Every row of a family shares one query keyed by the family root. */
export const useForkFamilyCapacity = (
	chat: Chat,
	isExpanded: boolean,
	chatTree: ChatTree,
	currentUserId: string,
): ForkFamilyCapacity => {
	const { experiments } = useDashboard();
	const parentID = getParentChatID(chat);
	const enabled =
		experiments.includes("fork-nested-subagents") &&
		chat.owner_id === currentUserId &&
		(parentID
			? chat.status === "running"
			: isExpanded &&
				collectDescendantChats(chatTree, chat.id).some((descendant) =>
					liveStatuses.has(descendant.status),
				));
	// Only the root row polls. Subagent rows read the shared cache, so rows
	// mounted later add no fetches of their own.
	const { data } = useQuery({
		...forkChatFamilyCapacity(getForkFamilyRootID(chat)),
		enabled,
		...(parentID
			? { refetchInterval: false, refetchOnMount: false, retryOnMount: false }
			: {}),
	});
	if (!enabled || !data) {
		return {};
	}
	if (parentID) {
		return data.waiting_for_capacity.includes(chat.id)
			? { status: waitingForSlotStatus }
			: {};
	}
	if (
		data.max_live_subagents > 0 &&
		data.live_subagents >= data.max_live_subagents
	) {
		return {
			liveLimit: { live: data.live_subagents, max: data.max_live_subagents },
		};
	}
	return {};
};

type ForkFamilyLiveLimitChipProps = {
	liveLimit: ForkFamilyCapacity["liveLimit"];
};

/** Tells the user why a family cannot spawn more subagents right now. */
export const ForkFamilyLiveLimitChip: React.FC<
	ForkFamilyLiveLimitChipProps
> = ({ liveLimit }) => {
	if (!liveLimit) {
		return null;
	}
	return (
		<Badge
			size="sm"
			variant="warning"
			className="shrink-0 tabular-nums"
			title="This family is at its live subagent limit. It cannot spawn another subagent until enough subagents finish."
		>
			live {liveLimit.live}/{liveLimit.max}
			<span className="sr-only"> subagents at limit</span>
		</Badge>
	);
};
