import { queryOptions } from "react-query";
import { getForkChatFamilyCapacity } from "#/api/forkChatFamily";

/** Query key for the subagent capacity of the family under a root chat. */
export const forkChatFamilyCapacityKey = (rootChatId: string) =>
	["chats", "fork-family", rootChatId] as const;

/**
 * Query options for the subagent capacity of the family under a root chat.
 * No event announces a capacity change, so the query polls.
 */
export const forkChatFamilyCapacity = (rootChatId: string) =>
	queryOptions({
		queryKey: forkChatFamilyCapacityKey(rootChatId),
		queryFn: ({ signal }) => getForkChatFamilyCapacity(rootChatId, signal),
		refetchInterval: 5_000,
	});
