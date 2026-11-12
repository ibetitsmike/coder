// This file is fork code: it decides whether a chat shows the Workflows tab
// of the fork-workflows experiment.

import { useQuery } from "react-query";
import { chatWorkflowRuns } from "#/api/queries/forkWorkflows";
import type { Chat } from "#/api/typesGenerated";
import { useForkExperiment } from "../../ChatElements/tools/forkToolUtils";

/**
 * Reports whether the chat shows the Workflows tab: workflows run only in
 * root chats, and the tab waits for the first run so ordinary chats stay
 * unchanged. A failed list request hides the tab.
 */
export const useForkWorkflowsTab = (chat: Chat): boolean => {
	const isEnabled =
		useForkExperiment("fork-workflows") && !chat.parent_chat_id;
	const runsQuery = useQuery({
		...chatWorkflowRuns(chat.id),
		enabled: isEnabled,
	});
	return isEnabled && (runsQuery.data?.length ?? 0) > 0;
};
