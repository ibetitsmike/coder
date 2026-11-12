// This file is fork code: cache helpers shared by the fork's list queries.

import { isAxiosError } from "axios";
import type { QueryClient, QueryKey } from "react-query";
import { compareUpdatedAtInstants } from "#/api/queries/chats";

type Timestamped = { updated_at: string };

/** Whether a was updated after b, to the timestamps' full precision. */
export const isNewer = (a: Timestamped, b: Timestamped) =>
	compareUpdatedAtInstants(a.updated_at, b.updated_at) > 0;

/** Whether a request failed with no response, so it may still have applied. */
export const isResponseLost = (error: unknown) =>
	isAxiosError(error) && error.response === undefined;

/**
 * Invalidates a list query. Callers reconcile an unknown outcome against the
 * refetched list, so only then (`awaitRefetch`) does the mutation stay pending
 * until the refetch settles.
 */
export const invalidateList = (
	queryClient: QueryClient,
	queryKey: QueryKey,
	{ exact, awaitRefetch }: { exact: boolean; awaitRefetch: boolean },
) => {
	// An unknown outcome is reconciled against the list it started from,
	// which may have lost its observers when the user moved on, so inactive
	// copies refetch too.
	const refetch = queryClient.invalidateQueries({
		queryKey,
		exact,
		refetchType: awaitRefetch ? "all" : "active",
	});
	return awaitRefetch ? refetch : undefined;
};
