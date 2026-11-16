// This file is fork code: it renders the run_workflow and await_workflow
// tools of the fork-workflows experiment.

import type React from "react";
import { useRef, useState } from "react";
import { useQuery } from "react-query";
import { useParams } from "react-router";
import { getErrorStatus } from "#/api/errors";
import { chat } from "#/api/queries/chats";
import {
	chatWorkflowRun,
	workflowRunRefetchInterval,
} from "#/api/queries/forkWorkflows";
import type { WorkflowRunDetail } from "#/api/typesGenerated";
import { ForkWorkflowLongText } from "../../ForkWorkflows/ForkWorkflowLongText";
import { ForkWorkflowPhaseRail } from "../../ForkWorkflows/ForkWorkflowPhaseRail";
import { ForkWorkflowResumeButton } from "../../ForkWorkflows/ForkWorkflowResumeButton";
import { ForkWorkflowStatusBadge } from "../../ForkWorkflows/ForkWorkflowStatusBadge";
import { ForkWorkflowSteps } from "../../ForkWorkflows/ForkWorkflowSteps";
import { ForkWorkflowStopButton } from "../../ForkWorkflows/ForkWorkflowStopButton";
import {
	type ForkWorkflowRunSnapshot,
	forkWorkflowRunStatusMeta,
	forkWorkflowStepProgress,
	formatForkWorkflowValue,
	mergeForkWorkflowRunDetail,
	parseForkWorkflowRunSnapshot,
} from "../../ForkWorkflows/forkWorkflows";
import { useCanWriteChat } from "../../ForkWorkflows/useCanWriteChat";
import type { ForkToolRendererProps } from "./forkToolRenderers";
import { forkToolErrorMessage } from "./forkToolUtils";
import { ToolCall } from "./ToolCall";
import { asString, parseArgs } from "./utils";

const toolLabel = (
	props: Pick<ForkToolRendererProps, "name" | "status" | "isError">,
	snapshot: ForkWorkflowRunSnapshot | null,
): string => {
	const isRunWorkflow = props.name === "run_workflow";
	if (props.status === "running") {
		return isRunWorkflow ? "Starting workflow…" : "Waiting for workflow…";
	}
	if (props.isError || !snapshot) {
		return isRunWorkflow
			? "Failed to start workflow"
			: "Failed to read workflow";
	}
	switch (snapshot.status) {
		case "running":
			return isRunWorkflow ? "Started workflow" : "Workflow still running";
		case "completed":
			return "Workflow completed";
		case "failed":
			return "Workflow failed";
		case "stopped":
			return "Workflow stopped";
		case "interrupted":
			return "Workflow interrupted";
		default:
			return "Workflow";
	}
};

/** The run and its steps come from the result, the script from the args. */
export const ForkWorkflowToolRenderer: React.FC<ForkToolRendererProps> = ({
	fallback: Fallback,
	...props
}) => {
	const { agentId } = useParams();
	const snapshot = props.isError
		? null
		: parseForkWorkflowRunSnapshot(props.result);
	if (!props.isError && !snapshot && props.status !== "running") {
		return <Fallback {...props} />;
	}
	if (agentId && snapshot?.status === "running") {
		return (
			<LiveForkWorkflowTool
				{...props}
				chatId={agentId}
				resultSnapshot={snapshot}
			/>
		);
	}
	return <ForkWorkflowTool {...props} snapshot={snapshot} />;
};

type ForkWorkflowToolProps = Omit<ForkToolRendererProps, "fallback"> & {
	snapshot: ForkWorkflowRunSnapshot | null;
	/** Present on the live card only, which owns the run controls. */
	chatId?: string;
	detail?: WorkflowRunDetail;
};

type LiveForkWorkflowToolProps = Omit<ForkWorkflowToolProps, "snapshot"> & {
	chatId: string;
	resultSnapshot: ForkWorkflowRunSnapshot;
};

const LiveForkWorkflowTool: React.FC<LiveForkWorkflowToolProps> = ({
	chatId,
	resultSnapshot,
	...props
}) => {
	const [mountedAt] = useState(Date.now);
	// A resume keeps the run ID, so a cached "interrupted" detail can predate
	// this running result. It applies only once the card has refetched it.
	const currentDetail = (
		run: WorkflowRunDetail | undefined,
		updatedAt: number,
	) =>
		run?.status === "interrupted" && updatedAt < mountedAt ? undefined : run;
	// Shares the Workflows panel's cache. The app disables retries, so polling
	// rides out transient errors. A 403 or 404 means this viewer cannot read
	// runs, so polling stops.
	const runQuery = useQuery({
		...chatWorkflowRun(chatId, resultSnapshot.runId),
		refetchInterval: ({ state }) => {
			const detail = currentDetail(state.data, state.dataUpdatedAt);
			if (detail) {
				return workflowRunRefetchInterval(detail.status);
			}
			const errorStatus = getErrorStatus(state.error);
			return errorStatus === 403 || errorStatus === 404
				? false
				: workflowRunRefetchInterval(resultSnapshot.status);
		},
	});
	const detail = currentDetail(runQuery.data, runQuery.dataUpdatedAt);
	return (
		<ForkWorkflowTool
			{...props}
			chatId={chatId}
			detail={detail}
			snapshot={
				detail
					? mergeForkWorkflowRunDetail(resultSnapshot, detail)
					: resultSnapshot
			}
		/>
	);
};

const ForkWorkflowTool: React.FC<ForkWorkflowToolProps> = ({
	snapshot,
	chatId,
	detail,
	...props
}) => {
	const { name, status, isError } = props;
	const args = parseArgs(props.args);
	const isRunWorkflow = name === "run_workflow";
	const scriptPath = isRunWorkflow ? asString(args?.script_path).trim() : "";
	const script = isRunWorkflow ? asString(args?.script) : "";
	const runArgs = isRunWorkflow ? args?.args : undefined;
	const isResume = isRunWorkflow && asString(args?.resume_run_id).trim() !== "";
	const label = toolLabel(props, snapshot);
	const errorMessage = isError ? forkToolErrorMessage(props.result).trim() : "";
	const runError = snapshot?.error ?? "";
	// Receives focus when a stop closes the dialog after the Stop button
	// unmounted.
	const runRowRef = useRef<HTMLDivElement>(null);

	return (
		<ToolCall.Root
			className="w-full"
			status={status}
			isError={isError}
			errorMessage={errorMessage || label}
			hasContent={
				Boolean(snapshot) ||
				errorMessage !== "" ||
				script !== "" ||
				scriptPath !== ""
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
					<ForkWorkflowRunControls
						chatId={chatId}
						detail={detail}
						snapshot={snapshot}
						fallbackFocusRef={runRowRef}
					/>
				)}
				{snapshot && (
					<div
						ref={runRowRef}
						tabIndex={-1}
						className="mt-1.5 font-mono text-xs text-content-secondary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-content-link"
					>
						{`Run ${snapshot.runId}`}
					</div>
				)}
				{runError !== "" && (
					<ForkWorkflowLongText label="Error" text={runError} isError />
				)}
				{snapshot && snapshot.steps.length > 0 && (
					<ForkWorkflowSteps
						steps={snapshot.steps}
						phases={snapshot.phases}
						currentPhase={snapshot.currentPhase}
						runStatus={snapshot.status}
					/>
				)}
				{snapshot &&
					snapshot.steps.length === 0 &&
					snapshot.phases.length > 0 && (
						<ForkWorkflowPhaseRail
							phases={snapshot.phases}
							currentPhase={snapshot.currentPhase}
							steps={snapshot.steps}
							runStatus={snapshot.status}
						/>
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

/**
 * Stop and Resume on the live card. A tool result alone does not know the
 * chat's owner or organization, so the controls wait for the cached chat and
 * its update check; a viewer without write access sees neither.
 */
const ForkWorkflowRunControls: React.FC<{
	chatId: string | undefined;
	detail: WorkflowRunDetail | undefined;
	snapshot: ForkWorkflowRunSnapshot;
	fallbackFocusRef: React.RefObject<HTMLElement | null>;
}> = ({ chatId, detail, snapshot, fallbackFocusRef }) => {
	// The chat page already loads this chat, so this reads the cached copy.
	const chatQuery = useQuery({ ...chat(chatId ?? ""), enabled: !!chatId });
	const canWriteChat = useCanWriteChat(chatQuery.data);
	// Plan mode turns omit run_workflow, so a resume could not run it.
	const isResumeAvailable = chatQuery.data?.plan_mode !== "plan";
	const run: WorkflowRunDetail | undefined =
		detail &&
		detail.chat_id === chatId &&
		detail.id === snapshot.runId &&
		detail.status === snapshot.status
			? detail
			: undefined;
	if (!chatId || !run || canWriteChat !== true) {
		return null;
	}
	return (
		<div className="flex items-center gap-1.5">
			<ForkWorkflowStopButton run={run} fallbackFocusRef={fallbackFocusRef} />
			{run.status === "interrupted" && isResumeAvailable && (
				<ForkWorkflowResumeButton runId={run.id} chatId={chatId} />
			)}
		</div>
	);
};
