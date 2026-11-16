// This file is fork code: the step list of a Starlark workflow run
// (fork-workflows experiment). Each step is a child chat.

import type React from "react";
import { Badge } from "#/components/Badge/Badge";
import { ForkChatLink } from "../ChatElements/tools/ForkChatLink";
import { ForkWorkflowPhaseRail } from "./ForkWorkflowPhaseRail";
import { ForkWorkflowStatusBadge } from "./ForkWorkflowStatusBadge";
import {
	type ForkWorkflowStep,
	forkWorkflowStepPhases,
	forkWorkflowStepStatusMeta,
} from "./forkWorkflows";

type ForkWorkflowStepsProps = {
	steps: readonly ForkWorkflowStep[];
	phases: readonly string[];
	currentPhase: string;
	/** The run's status; the rail uses it to mark unfinished phases. */
	runStatus: string;
};

export const ForkWorkflowSteps: React.FC<ForkWorkflowStepsProps> = ({
	steps,
	phases,
	currentPhase,
	runStatus,
}) => {
	return (
		<>
			<ForkWorkflowPhaseRail
				phases={phases}
				currentPhase={currentPhase}
				steps={steps}
				runStatus={runStatus}
			/>
			<ol
				aria-label="Workflow steps"
				className="m-0 mt-1.5 flex list-none flex-col gap-1 p-0"
			>
				{forkWorkflowStepPhases(steps).flatMap((group) =>
					group.steps.map((step) => {
						// Each phase group starts with a heading, so a script that
						// re-enters a phase shows its later steps under that phase
						// again. Steps before the first phase() call follow no
						// heading.
						const isFirstStepOfGroup = group.steps[0] === step;
						return (
							<li
								key={step.chatId}
								aria-describedby={
									isFirstStepOfGroup && group.phase !== ""
										? group.phase
										: undefined
								}
								className="flex min-w-0 flex-col gap-0.5"
							>
								{isFirstStepOfGroup && group.phase !== "" && (
									<div
										id={group.phase}
										title={group.phase}
										className="mt-1 truncate font-mono text-[11px] uppercase tracking-wide text-content-secondary"
									>
										{group.phase}
									</div>
								)}
								<div className="flex min-w-0 items-center gap-1.5">
									<ForkWorkflowStatusBadge
										{...forkWorkflowStepStatusMeta(step.status)}
									/>
									<ForkChatLink chatId={step.chatId}>
										{step.title || "Untitled step"}
									</ForkChatLink>
									{step.reused && (
										<Badge
											asChild
											size="sm"
											variant="outline"
											className="shrink-0"
										>
											<span>Reused</span>
										</Badge>
									)}
								</div>
								{step.error !== "" && (
									<div className="pl-1 font-mono text-xs text-content-destructive wrap-anywhere">
										{step.error}
									</div>
								)}
							</li>
						);
					}),
				)}
			</ol>
		</>
	);
};
