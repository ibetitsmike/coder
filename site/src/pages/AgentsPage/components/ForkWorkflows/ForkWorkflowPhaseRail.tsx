// This file is fork code: the phase rail of a Starlark workflow run
// (fork-workflows experiment). It renders nothing when the script declared
// no phases and never called phase().

import { cn } from "cn";
import type React from "react";
import {
	type ForkWorkflowPhaseState,
	type ForkWorkflowStep,
	forkWorkflowPhaseState,
} from "./forkWorkflows";

const phaseDotClassName: Record<ForkWorkflowPhaseState, string> = {
	pending: "border-border bg-transparent",
	running: "border-border-pending bg-surface-sky",
	done: "border-border-green bg-surface-green",
};

const phaseLabelClassName: Record<ForkWorkflowPhaseState, string> = {
	pending: "text-content-secondary",
	running: "text-content-primary",
	done: "text-content-secondary",
};

type ForkWorkflowPhaseRailProps = {
	/** The run's phases in display order. */
	phases: readonly string[];
	/** The phase the script set last; empty before the first phase() call. */
	currentPhase: string;
	/** The run's steps, for the state of each phase. */
	steps: readonly ForkWorkflowStep[];
};

/**
 * One pill per phase, joined into a rail. A phase the script has not set
 * yet stays pending. A phase the script set but whose steps have not
 * spawned yet shows as running.
 */
export const ForkWorkflowPhaseRail: React.FC<ForkWorkflowPhaseRailProps> = ({
	phases,
	currentPhase,
	steps,
}) => {
	if (phases.length === 0) {
		return null;
	}
	const currentIndex = phases.indexOf(currentPhase);
	return (
		<ol
			aria-label="Workflow phases"
			className="m-0 mt-1.5 flex list-none flex-wrap items-center gap-1.5 p-0"
		>
			{phases.map((phase, index) => {
				const reached = currentIndex >= index;
				const phaseSteps = steps.filter((step) => step.phase === phase);
				const state: ForkWorkflowPhaseState =
					!reached || phaseSteps.length === 0
						? reached
							? "running"
							: "pending"
						: forkWorkflowPhaseState(phaseSteps);
				return (
					<li key={phase} className="flex items-center gap-1.5">
						{index > 0 && (
							<span
								aria-hidden
								className={cn(
									"h-px w-3",
									reached ? "bg-border-secondary" : "bg-border",
								)}
							/>
						)}
						<span
							aria-current={phase === currentPhase ? "step" : undefined}
							className={cn(
								"flex items-center gap-1 rounded-full border border-solid px-1.5 py-0.5 text-xs",
								phaseLabelClassName[state],
							)}
						>
							<span
								className={cn(
									"size-1.5 rounded-full border border-solid",
									phaseDotClassName[state],
								)}
							/>
							{phase}
						</span>
					</li>
				);
			})}
		</ol>
	);
};
