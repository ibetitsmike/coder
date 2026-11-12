// This file is fork code: the Workflows tab of the right panel
// (fork-workflows experiment).

import type React from "react";
import { useState } from "react";
import { useQuery } from "react-query";
import { chatWorkflowRuns } from "#/api/queries/forkWorkflows";
import { ScrollArea } from "#/components/ScrollArea/ScrollArea";
import { useTabPanelState } from "../../ChatsSidebar/tabs/SidebarTabView";
import { ForkWorkflowRunDetail } from "./ForkWorkflowRunDetail";
import { ForkWorkflowRunList } from "./ForkWorkflowRunList";

// The panel remounts when the user opens a step's child chat, so the open
// run is kept per chat for the session: navigating back shows it again.
const openRunIdByChat = new Map<string, string>();

type ForkWorkflowsPanelProps = {
	chatId: string;
};

/** Lists the chat's workflow runs; picking one opens its detail. */
export const ForkWorkflowsPanel: React.FC<ForkWorkflowsPanelProps> = ({
	chatId,
}) => {
	const { isActive, isPanelOpen } = useTabPanelState();
	const [selectedRunId, setSelectedRunId] = useState(
		() => openRunIdByChat.get(chatId) ?? null,
	);
	const selectRun = (runId: string | null) => {
		if (runId) {
			openRunIdByChat.set(chatId, runId);
		} else {
			openRunIdByChat.delete(chatId);
		}
		setSelectedRunId(runId);
	};
	const runsQuery = useQuery(chatWorkflowRuns(chatId));

	return (
		<ScrollArea
			className="h-full"
			viewportClassName="size-full [&>div]:block!"
			scrollBarClassName="w-1.5"
		>
			<div className="min-h-full w-full overflow-x-hidden">
				{selectedRunId ? (
					<ForkWorkflowRunDetail
						chatId={chatId}
						runId={selectedRunId}
						isVisible={isActive && isPanelOpen}
						onBack={() => selectRun(null)}
					/>
				) : (
					<ForkWorkflowRunList
						runs={runsQuery.data}
						error={runsQuery.error}
						onSelectRun={selectRun}
					/>
				)}
			</div>
		</ScrollArea>
	);
};
