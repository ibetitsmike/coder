// This file is fork code: it renders the run_workflow and await_workflow
// tools of the fork-workflows experiment.

import type React from "react";
import { ForkWorkflowLongText } from "../../ForkWorkflows/ForkWorkflowLongText";
import { ForkWorkflowStatusBadge } from "../../ForkWorkflows/ForkWorkflowStatusBadge";
import { ForkWorkflowSteps } from "../../ForkWorkflows/ForkWorkflowSteps";
import {
	type ForkWorkflowRunSnapshot,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepProgress,
	formatForkWorkflowValue,
	parseForkWorkflowRunSnapshot,
} from "../../ForkWorkflows/forkWorkflows";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asString, parseArgs } from "./utils";

const toolLabel = (
	props: ForkToolRendererProps,
	snapshot: ForkWorkflowRunSnapshot | null,
): string => {
	const isRunWorkflow = props.name === "run_workflow";
	if (props.status === "running") {
		return isRunWorkflow ? "Starting workflow…" : "Waiting for workflow…";
	}
	if (props.isError || !snapshot) {
		return isRunWorkflow ? "Failed to start workflow" : "Failed to read workflow";
	}
	switch (snapshot.status) {
		case "running":
			return isRunWorkflow ? "Started workflow" : "Workflow still running";
		case "completed":
			return "Workflow completed";
		case "failed":
			return "Workflow failed";
		default:
			return "Workflow";
	}
};

/** The run and its steps come from the result, the script from the args. */
export const ForkWorkflowToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const { name, status, isError } = props;
	const snapshot = isError ? null : parseForkWorkflowRunSnapshot(props.result);
	if (!isError && !snapshot && status !== "running") {
		return <Fallback {...props} />;
	}
	const args = parseArgs(props.args);
	const isRunWorkflow = name === "run_workflow";
	const scriptPath = isRunWorkflow ? asString(args?.script_path).trim() : "";
	const script = isRunWorkflow ? asString(args?.script) : "";
	const runArgs = isRunWorkflow ? args?.args : undefined;
	const isResume = isRunWorkflow && asString(args?.resume_run_id).trim() !== "";
	const label = toolLabel(props, snapshot);
	const errorMessage = isError ? forkToolErrorMessage(props.result).trim() : "";
	const runError = snapshot?.error ?? "";

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || label}
			hasContent={
				Boolean(snapshot) || errorMessage !== "" || script !== "" || scriptPath !== ""
			}
		>
			<ToolCall.Header
				iconName="fork_workflow"
				label={label}
				secondaryLabel={
					snapshot && (
						<>
							<ForkWorkflowStatusBadge
								{...forkWorkflowRunStatusMeta(snapshot.status)}
							/>
							<span className="shrink-0 text-xs text-content-secondary">
								{forkWorkflowStepProgress(snapshot.steps)}
							</span>
						</>
					)
				}
			/>
			<ToolCall.Content>
				{errorMessage !== "" && (
					<ToolCall.ErrorDetail>{errorMessage}</ToolCall.ErrorDetail>
				)}
				{snapshot && (
					<div className="mt-1.5 font-mono text-xs text-content-secondary">
						{`Run ${snapshot.runId}`}
					</div>
				)}
				{runError !== "" && (
					<ForkWorkflowLongText label="Error" text={runError} isError />
				)}
				{snapshot && snapshot.steps.length > 0 && (
					<ForkWorkflowSteps steps={snapshot.steps} />
				)}
				{isResume && snapshot?.steps.length === 0 && (
					<div className="mt-1.5 text-xs text-content-secondary">
						This run resumed. Reused steps show when the run is awaited.
					</div>
				)}
				{snapshot && snapshot.result !== undefined && (
					<ForkWorkflowLongText
						label="Result"
						text={formatForkWorkflowValue(snapshot.result)}
					/>
				)}
				{snapshot && snapshot.logs.length > 0 && (
					<ForkWorkflowLongText label="Logs" text={snapshot.logs.join("\n")} />
				)}
				{scriptPath !== "" && (
					<ForkWorkflowLongText label="Script path" text={scriptPath} />
				)}
				{script !== "" && <ForkWorkflowLongText label="Script" text={script} />}
				{runArgs !== undefined && (
					<ForkWorkflowLongText
						label="Arguments"
						text={formatForkWorkflowValue(runArgs)}
					/>
				)}
			</ToolCall.Content>
		</ToolCall.Root>
	);
};
