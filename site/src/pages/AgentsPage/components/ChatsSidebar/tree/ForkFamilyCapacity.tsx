// This file is fork code: it shows the subagent capacity of a
// fork-nested-subagents family in the sidebar tree. ChatTreeNode.tsx calls it
// in two places, so upstream syncs of that file stay small.

import { HourglassIcon, type LucideProps } from "lucide-react";
import { useQuery } from "react-query";
import { forkChatFamilyCapacity } from "#/api/queries/forkChatFamily";
import type { Chat, ChatStatus } from "#/api/typesGenerated";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "#/components/Tooltip/Tooltip";
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

const WaitingForSlotIcon: React.FC<LucideProps> = (props) => (
	<Tooltip>
		<TooltipTrigger asChild>
			<HourglassIcon {...props} />
		</TooltipTrigger>
		<TooltipContent side="right">
			Waiting for a free subagent slot. It starts when another subagent frees
			one.
		</TooltipContent>
	</Tooltip>
);

const waitingForSlotStatus = {
	icon: WaitingForSlotIcon,
	className: "text-content-warning",
	label: "Waiting for slot",
};

type ForkFamilyCapacity = {
	/** Replaces the row's status icon while the subagent waits for a slot. */
	readonly status?: typeof waitingForSlotStatus;
	/** The family's live subagent count and limit, set only at the limit. */
	readonly liveLimit?: { readonly live: number; readonly max: number };
};

/**
 * Reads the family capacity for a tree row. A root row asks only while it is
 * expanded and has a live subagent; a subagent row asks only while it runs.
 * Both rows of a family share one query.
 */
export const useForkFamilyCapacity = (
	chat: Chat,
	isExpanded: boolean,
	chatTree: ChatTree,
): ForkFamilyCapacity => {
	const { experiments } = useDashboard();
	const parentID = getParentChatID(chat);
	const rootID = asNonEmptyString(chat.root_chat_id) ?? parentID ?? chat.id;
	const enabled =
		experiments.includes("fork-nested-subagents") &&
		(parentID
			? chat.status === "running"
			: isExpanded &&
				collectDescendantChats(chatTree, chat.id).some((descendant) =>
					liveStatuses.has(descendant.status),
				));
	const { data } = useQuery({ ...forkChatFamilyCapacity(rootID), enabled });
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

/** Marks a root row whose family is at its live subagent limit. */
export const ForkFamilyLiveLimitChip: React.FC<{
	liveLimit: ForkFamilyCapacity["liveLimit"];
}> = ({ liveLimit }) => {
	if (!liveLimit) {
		return null;
	}
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="shrink-0 rounded-sm bg-surface-tertiary px-1 text-[11px] leading-4 tabular-nums text-content-warning">
					live {liveLimit.live}/{liveLimit.max}
				</span>
			</TooltipTrigger>
			<TooltipContent side="right">
				This family has reached its live subagent limit. It cannot spawn another
				subagent until one finishes.
			</TooltipContent>
		</Tooltip>
	);
};
