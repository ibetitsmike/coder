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
	/** The run's phases in display order; empty when the script uses none. */
	phases: readonly string[];
	/** The phase the script set last; empty before the first phase() call. */
	currentPhase: string;
};

export const ForkWorkflowSteps: React.FC<ForkWorkflowStepsProps> = ({
	steps,
	phases,
	currentPhase,
}) => {
	return (
		<>
			<ForkWorkflowPhaseRail
				phases={phases}
				currentPhase={currentPhase}
				steps={steps}
			/>
			<ol
				aria-label="Workflow steps"
				className="m-0 mt-1.5 flex list-none flex-col gap-1 p-0"
			>
				{forkWorkflowStepPhases(steps).map(
					({ phase, steps: phaseSteps }, groupIndex) => (
						<li key={groupIndex} className="flex min-w-0 flex-col gap-0.5">
							{phase !== "" && (
								<div className="mt-1 font-mono text-[11px] uppercase tracking-wide text-content-secondary">
									{phase}
								</div>
							)}
							<ol className="m-0 flex list-none flex-col gap-1 p-0">
								{phaseSteps.map((step) => (
									<li
										key={step.chatId}
										className="flex min-w-0 flex-col gap-0.5"
									>
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
								))}
							</ol>
						</li>
					),
				)}
			</ol>
		</>
	);
};
