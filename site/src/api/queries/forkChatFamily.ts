import { queryOptions } from "react-query";
import { getForkChatFamilyCapacity } from "#/api/forkChatFamily";

/**
 * Keyed by the family root, never a descendant, so every row of a family
 * shares one cache entry.
 */
export const forkChatFamilyCapacityKey = (rootChatId: string) =>
	["chats", "fork-family", rootChatId] as const;

/** No event announces a capacity change, so the query polls. */
export const forkChatFamilyCapacity = (rootChatId: string) =>
	queryOptions({
		queryKey: forkChatFamilyCapacityKey(rootChatId),
		queryFn: ({ signal }) => getForkChatFamilyCapacity(rootChatId, signal),
		// A failing read backs off instead of stopping, so a transient error
		// does not hide capacity until the row remounts.
		refetchInterval: ({ state }) => (state.status === "error" ? 30_000 : 5_000),
	});
