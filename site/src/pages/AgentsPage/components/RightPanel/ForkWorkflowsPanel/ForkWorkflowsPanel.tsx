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

type ForkWorkflowsPanelProps = {
	chatId: string;
};

/** Lists the chat's workflow runs; picking one opens its detail. */
export const ForkWorkflowsPanel: React.FC<ForkWorkflowsPanelProps> = ({
	chatId,
}) => {
	const { isActive, isPanelOpen } = useTabPanelState();
	const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
	const runsQuery = useQuery(chatWorkflowRuns(chatId));

	return (
		<ScrollArea
			className="h-full"
			viewportClassName="size-full [&>div]:block!"
			scrollBarClassName="w-1.5"
		>
			<div className="min-h-full w-full min-w-0 overflow-x-hidden">
				{selectedRunId ? (
					<ForkWorkflowRunDetail
						chatId={chatId}
						runId={selectedRunId}
						isVisible={isActive && isPanelOpen}
						onBack={() => setSelectedRunId(null)}
					/>
				) : (
					<ForkWorkflowRunList
						runs={runsQuery.data}
						error={runsQuery.error}
						onSelectRun={setSelectedRunId}
					/>
				)}
			</div>
		</ScrollArea>
	);
};
