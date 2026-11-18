// This file is fork code: it refreshes the fork panels from the chat watch
// stream.

import type { QueryClient, QueryKey } from "react-query";
import type { ChatWatchEvent } from "#/api/typesGenerated";
import {
	forkChatFamilyCapacityKey,
	forkChatFamilyRootKey,
} from "./forkChatFamily";
import {
	chatDescendantQuestionsKey,
	chatDescendantQuestionsRootKey,
} from "./forkChatQuestions";
import { chatWakeupsKey, chatWakeupsRootKey } from "./forkChatWakeups";
import {
	chatWorkflowRunKey,
	chatWorkflowRunsKey,
	chatWorkflowRunsRootKey,
} from "./forkWorkflows";

/**
 * The shortest gap between two refetches of one panel. A running workflow
 * writes its logs every two seconds; the first event refetches at once and
 * the rest of the burst folds into one trailing refetch.
 */
export const forkWatchInvalidateGap = 3_000;

type ForkInvalidation = { queryKey: QueryKey; exact?: boolean };

type ForkChatWatch = {
	/**
	 * Handles one watch event. It returns true for a fork_change event, which
	 * carries no chat for the shared cache merge to apply.
	 */
	onEvent: (event: ChatWatchEvent) => boolean;
	/**
	 * Refreshes the fork panels each time the stream opens: an event published
	 * before the subscription reached no one.
	 */
	onOpen: () => void;
	/** Drops the pending trailing refetches. */
	dispose: () => void;
};

const forkInvalidationsForEvent = (
	event: ChatWatchEvent,
): ForkInvalidation[] => {
	const change = event.fork_change;
	if (event.kind === "fork_change") {
		if (!change) {
			return [];
		}
		const runId = change.workflow_run_id;
		return change.facets.flatMap((facet): ForkInvalidation[] => {
			switch (facet) {
				case "questions":
					return [
						{ queryKey: chatDescendantQuestionsKey(change.root_chat_id) },
					];
				case "wakeups":
					return [{ queryKey: chatWakeupsKey(change.chat_id) }];
				case "workflows":
					// exact keeps the other runs' details, which this
					// change did not touch.
					return [
						{ queryKey: chatWorkflowRunsKey(change.chat_id), exact: true },
						...(runId
							? [{ queryKey: chatWorkflowRunKey(change.chat_id, runId) }]
							: []),
					];
				case "workflow_logs":
					return runId
						? [{ queryKey: chatWorkflowRunKey(change.chat_id, runId) }]
						: [];
				default:
					return [];
			}
		});
	}
	const chat = event.chat;
	const invalidations: ForkInvalidation[] = [];
	if (event.kind === "deleted") {
		// Archiving a chat cancels its wake-ups.
		invalidations.push({ queryKey: chatWakeupsKey(chat.id) });
	}
	const root = chat.parent_chat_id
		? (chat.root_chat_id ?? chat.parent_chat_id)
		: undefined;
	if (
		root &&
		(event.kind === "status_change" ||
			event.kind === "created" ||
			event.kind === "deleted")
	) {
		// A descendant that starts, ends or is archived changes the family's
		// live count, and a message that answers its question moves it out
		// of waiting.
		invalidations.push(
			{ queryKey: forkChatFamilyCapacityKey(root) },
			{ queryKey: chatDescendantQuestionsKey(root) },
		);
	}
	return invalidations;
};

const forkPanelRootKeys: readonly QueryKey[] = [
	chatDescendantQuestionsRootKey,
	chatWakeupsRootKey,
	chatWorkflowRunsRootKey,
	forkChatFamilyRootKey,
];

/**
 * Refetches the fork panel queries that each chat watch event makes stale,
 * each at most once per forkWatchInvalidateGap. Call dispose when the stream
 * closes.
 */
export const createForkChatWatch = (
	queryClient: QueryClient,
): ForkChatWatch => {
	// One entry per query in its gap. An entry leaves the map when its gap
	// ends with no event, so the map holds only recently changed panels.
	const gaps = new Map<
		string,
		{ timer: ReturnType<typeof setTimeout>; trailing: boolean }
	>();

	const startGap = (id: string, invalidation: ForkInvalidation) => {
		const gap = {
			trailing: false,
			timer: setTimeout(() => {
				if (!gap.trailing) {
					gaps.delete(id);
					return;
				}
				void queryClient.invalidateQueries(invalidation);
				startGap(id, invalidation);
			}, forkWatchInvalidateGap),
		};
		gaps.set(id, gap);
	};

	const invalidate = (invalidation: ForkInvalidation) => {
		const id = JSON.stringify([invalidation.queryKey, invalidation.exact]);
		const gap = gaps.get(id);
		if (gap) {
			gap.trailing = true;
			return;
		}
		void queryClient.invalidateQueries(invalidation);
		startGap(id, invalidation);
	};

	return {
		onEvent: (event) => {
			for (const invalidation of forkInvalidationsForEvent(event)) {
				invalidate(invalidation);
			}
			return event.kind === "fork_change";
		},
		onOpen: () => {
			for (const queryKey of forkPanelRootKeys) {
				void queryClient.invalidateQueries({ queryKey });
			}
		},
		dispose: () => {
			for (const gap of gaps.values()) {
				clearTimeout(gap.timer);
			}
			gaps.clear();
		},
	};
};
