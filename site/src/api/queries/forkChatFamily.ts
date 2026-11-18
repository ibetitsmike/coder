import { queryOptions } from "react-query";
import { getForkChatFamilyCapacity } from "#/api/forkChatFamily";

export const forkChatFamilyRootKey = ["chats", "fork-family"] as const;

/**
 * Keyed by the family root, never a descendant, so every row of a family
 * shares one cache entry.
 */
export const forkChatFamilyCapacityKey = (rootChatId: string) =>
	[...forkChatFamilyRootKey, rootChatId] as const;

/**
 * Admitting a subagent held for capacity and freeing a slot in another
 * family raise no watch event, so this query polls.
 */
export const forkChatFamilyCapacity = (rootChatId: string) =>
	queryOptions({
		queryKey: forkChatFamilyCapacityKey(rootChatId),
		queryFn: ({ signal }) => getForkChatFamilyCapacity(rootChatId, signal),
		refetchInterval: 30_000,
	});
