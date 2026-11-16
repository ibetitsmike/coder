// This file is fork code: the step list of a Starlark workflow run
// (fork-workflows experiment). Each step is a child chat.

import type React from "react";
import { useId } from "react";
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
	const stepsId = useId();
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
				{forkWorkflowStepPhases(steps).map(
					({ phase, steps: phaseSteps }, groupIndex) => {
						// A phase name can hold any text, so the heading id uses the
						// group index. The sentinel group of steps that ran before the
						// first phase() call needs no heading.
						const headingId =
							phase === "" ? undefined : `${stepsId}-${groupIndex}`;
						return (
							<li
								key={phase === "" ? "\u0000" : phase}
								className="flex min-w-0 flex-col gap-0.5"
							>
								{phase !== "" && (
									<div
										id={headingId}
										title={phase}
										className="mt-1 truncate font-mono text-[11px] uppercase tracking-wide text-content-secondary"
									>
										{phase}
									</div>
								)}
								<ol
									aria-labelledby={headingId}
									className="m-0 flex list-none flex-col gap-1 p-0"
								>
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
						);
					},
				)}
			</ol>
		</>
	);
};
