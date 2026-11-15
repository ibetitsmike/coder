// This file is fork code: the write-access gate of the workflow run controls
// (fork-workflows experiment). The stop route authorizes as a chat update
// (coderd/fork_workflow_runs.go), and a resume sends a chat message, so both
// controls stay hidden until the viewer may update the chat.

import { useQuery } from "react-query";
import { checkAuthorization } from "#/api/queries/authCheck";
import type { Chat } from "#/api/typesGenerated";

/**
 * Reports whether the viewer may stop or resume a run of this chat.
 * Undefined while the check is pending or fails, which hides the controls
 * instead of offering an action that the API would refuse. A failed check
 * also fails the underlying query, so consumers can tell it from a pending
 * one and surface the error like any other query failure.
 */
export const useCanWriteChat = (
	chat: Pick<Chat, "owner_id" | "organization_id"> | undefined,
): boolean | undefined => {
	const query = useQuery({
		...checkAuthorization<{ canUpdateChat: boolean }>({
			checks: {
				canUpdateChat: {
					object: {
						resource_type: "chat",
						owner_id: chat?.owner_id ?? "",
						organization_id: chat?.organization_id ?? "",
					},
					action: "update",
				},
			},
		}),
		enabled: chat !== undefined,
	});
	return chat === undefined ? undefined : query.data?.canUpdateChat;
};
