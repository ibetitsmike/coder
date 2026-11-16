// This file is fork code: the phase rail of a Starlark workflow run
// (fork-workflows experiment). It renders nothing when the script declared
// no phases and never called phase().

import { cn } from "cn";
import type React from "react";
import {
	type ForkWorkflowPhaseState,
	type ForkWorkflowStep,
	forkWorkflowPhaseStateOf,
} from "./forkWorkflows";

const phaseDotClassName: Record<ForkWorkflowPhaseState, string> = {
	pending: "border-border bg-transparent",
	running: "border-border-pending bg-surface-sky",
	done: "border-border-green bg-surface-green",
	failed: "border-border-destructive bg-surface-red",
};

const phaseLabelClassName: Record<ForkWorkflowPhaseState, string> = {
	pending: "text-content-secondary",
	running: "text-content-primary",
	done: "text-content-secondary",
	failed: "text-content-destructive",
};

type ForkWorkflowPhaseRailProps = {
	phases: readonly string[];
	currentPhase: string;
	steps: readonly ForkWorkflowStep[];
	/** The run's status; a failed or stopped run marks unfinished phases. */
	runStatus: string;
};

const phaseStateLabel: Record<ForkWorkflowPhaseState, string> = {
	pending: "pending",
	running: "running",
	done: "done",
	failed: "failed",
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
	runStatus,
}) => {
	if (phases.length === 0) {
		return null;
	}
	return (
		<ol
			aria-label="Workflow phases"
			className="m-0 mt-1.5 flex list-none flex-wrap items-center gap-1.5 p-0"
		>
			{phases.map((phase, index) => {
				const state = forkWorkflowPhaseStateOf(
					phase,
					index,
					phases,
					currentPhase,
					steps,
					runStatus,
				);
				return (
					<li key={phase} className="flex min-w-0 items-center gap-1.5">
						{index > 0 && (
							<span
								aria-hidden
								className={cn(
									"h-px w-3",
									phases.indexOf(currentPhase) >= index
										? "bg-border-secondary"
										: "bg-border",
								)}
							/>
						)}
						<span
							aria-current={phase === currentPhase ? "step" : undefined}
							title={phase}
							className={cn(
								"flex min-w-0 items-center gap-1 rounded-full border border-solid px-1.5 py-0.5 text-xs",
								phaseLabelClassName[state],
							)}
						>
							<span
								className={cn(
									"size-1.5 shrink-0 rounded-full border border-solid",
									phaseDotClassName[state],
								)}
							/>
							<span className="truncate">{phase}</span>
							<span className="sr-only"> ({phaseStateLabel[state]})</span>
						</span>
					</li>
				);
			})}
		</ol>
	);
};
