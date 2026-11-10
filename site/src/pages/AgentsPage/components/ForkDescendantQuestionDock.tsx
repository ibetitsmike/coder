// This file is fork code: the fork-nested-subagents experiment's dock for
// questions that descendant chats ask the user. Upstream's ChatPageContent.tsx
// renders it once, next to AskUserQuestionDock.

import { cn } from "cn";
import {
	LoaderIcon,
	MessageCircleQuestionIcon,
	TriangleAlertIcon,
} from "lucide-react";
import type React from "react";
import { useId, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { Link, useLocation } from "react-router";
import { getErrorMessage } from "#/api/errors";
import {
	answerChatDescendantQuestion,
	answerChatDescendantSecretQuestion,
	chatDescendantQuestions,
} from "#/api/queries/forkChatQuestions";
import type {
	Chat,
	ChatDescendantQuestion,
	ChatQuestion,
} from "#/api/typesGenerated";
import { Alert, AlertDescription, AlertTitle } from "#/components/Alert/Alert";
import { Button } from "#/components/Button/Button";
import { Input } from "#/components/Input/Input";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { passwordManagerIgnoreProps } from "#/utils/formUtils";
import { chatWidthClass, useChatFullWidth } from "../hooks/useChatFullWidth";
import { Response } from "./ChatElements/Response";
import { AskUserQuestionForm } from "./ChatElements/tools/AskUserQuestionTool";
import { getQuestionCallKey } from "./ChatElements/tools/questionDraft";

type ForkDescendantQuestionDockProps = {
	chat: Pick<Chat, "id" | "parent_chat_id">;
	/** The viewer cannot act on this chat: it is archived, or another user's. */
	isReadOnly: boolean;
	/** The page accepts a turn now, so the question takes an answer. */
	canAnswer: boolean;
};

/**
 * Shows a root chat's oldest pending descendant question. The answer goes to
 * the descendant chat that asked it, not to the root chat.
 */
export const ForkDescendantQuestionDock: React.FC<
	ForkDescendantQuestionDockProps
> = ({ chat, isReadOnly, canAnswer }) => {
	const { experiments } = useDashboard();
	if (
		!experiments.includes("fork-nested-subagents") ||
		chat.parent_chat_id ||
		isReadOnly
	) {
		return null;
	}
	return <DescendantQuestionDock rootChatId={chat.id} canAnswer={canAnswer} />;
};

const DescendantQuestionDock: React.FC<{
	rootChatId: string;
	canAnswer: boolean;
}> = ({ rootChatId, canAnswer }) => {
	const [chatFullWidth] = useChatFullWidth();
	const location = useLocation();
	const scrollerRef = useRef<HTMLDivElement>(null);
	const hadFocusRef = useRef(false);
	const headingId = useId();
	const queryClient = useQueryClient();
	const questionsQuery = useQuery(chatDescendantQuestions(rootChatId));
	const answerMutation = useMutation(
		answerChatDescendantQuestion(queryClient, rootChatId),
	);

	// A failed poll keeps the questions of the last successful one, so the
	// alert shows above them: they may be stale.
	const loadErrorAlert = questionsQuery.isError && (
		<Alert
			severity="error"
			className={cn(
				"mx-auto mb-2 w-full shrink-0",
				chatWidthClass(chatFullWidth),
			)}
		>
			<AlertTitle>Questions from subagents failed to load</AlertTitle>
			<AlertDescription>
				{getErrorMessage(questionsQuery.error, "Something went wrong.")}
			</AlertDescription>
		</Alert>
	);
	const pendingQuestions = questionsQuery.data ?? [];
	const question = pendingQuestions.at(0);
	if (!question) {
		return loadErrorAlert ? <div className="px-4">{loadErrorAlert}</div> : null;
	}
	const questionKey = getQuestionCallKey(
		question.tool_call_id,
		question.created_at,
	);
	const secretQuestion = question.questions.find((item) => item.secret);

	return (
		<div
			// Keying the scroller by call gives the next question a fresh form
			// that starts scrolled to the top.
			key={questionKey}
			ref={scrollerRef}
			className="flex max-h-[60vh] min-h-0 scroll-pb-14 flex-col overflow-y-auto px-4 scrollbar-gutter-stable scrollbar-thin"
		>
			{loadErrorAlert}
			<section
				// A section that replaces a focused one takes the focus, as in
				// AskUserQuestionDock, so Tab reaches the next question's form.
				ref={(section: HTMLElement) => {
					if (hadFocusRef.current && document.activeElement === document.body) {
						section.focus({ preventScroll: true });
					}
					return () => {
						hadFocusRef.current = section.contains(document.activeElement);
					};
				}}
				aria-labelledby={headingId}
				tabIndex={-1}
				className={cn(
					"mx-auto mb-2 w-full shrink-0 rounded-lg border border-solid border-border bg-surface-primary shadow-sm focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-content-link",
					chatWidthClass(chatFullWidth),
				)}
			>
				<div className="flex items-center gap-1.5 px-3 py-2.5 text-xs text-content-secondary">
					<MessageCircleQuestionIcon
						aria-hidden="true"
						className="size-3.5 shrink-0"
					/>
					<span id={headingId} className="min-w-0 flex-1 truncate">
						Question from{" "}
						<Link
							to={{
								pathname: `/agents/${question.chat_id}`,
								search: location.search,
							}}
							className="text-content-link hover:underline"
						>
							{question.chat_title || "a subagent"}
						</Link>
					</span>
					{pendingQuestions.length > 1 && (
						<span className="shrink-0">1 of {pendingQuestions.length}</span>
					)}
				</div>
				<div className="px-3 pb-3">
					{secretQuestion ? (
						<SecretQuestionForm
							rootChatId={rootChatId}
							question={question}
							secretQuestion={secretQuestion}
							canAnswer={canAnswer}
						/>
					) : (
						<AskUserQuestionForm
							questions={question.questions.map((item) => ({
								header: item.header,
								question: item.question,
								options: [...item.options],
							}))}
							asyncToolCallId={question.tool_call_id}
							draftKey={questionKey}
							showSubmittedAnswer
							onSubmitAnswer={
								canAnswer
									? async (text) => {
											await answerMutation.mutateAsync({ question, text });
										}
									: undefined
							}
							hasStickyActions
							onStepChange={() => {
								if (scrollerRef.current) {
									scrollerRef.current.scrollTop = 0;
								}
							}}
						/>
					)}
				</div>
			</section>
		</div>
	);
};

/**
 * Takes the value of a secret question in a masked field. The value never
 * reaches a draft, local storage or a chat message.
 */
const SecretQuestionForm: React.FC<{
	rootChatId: string;
	question: ChatDescendantQuestion;
	secretQuestion: ChatQuestion;
	canAnswer: boolean;
}> = ({ rootChatId, question, secretQuestion, canAnswer }) => {
	const idPrefix = useId();
	const headerId = `${idPrefix}-header`;
	const textId = `${idPrefix}-text`;
	const helperId = `${idPrefix}-helper`;
	const queryClient = useQueryClient();
	const [value, setValue] = useState("");
	const answerMutation = useMutation(
		answerChatDescendantSecretQuestion(queryClient, rootChatId),
	);
	const isSubmitting = answerMutation.isPending;

	// The question stays in the dock until the questions reload. The status
	// takes the focus of the submit button it replaces, so the section still
	// holds focus when the next question replaces it.
	if (answerMutation.isSuccess) {
		return (
			<p
				ref={(status) => {
					if (status && document.activeElement === document.body) {
						status.focus({ preventScroll: true });
					}
				}}
				role="status"
				tabIndex={-1}
				className="text-[13px] text-content-secondary focus:outline-hidden"
			>
				Secret answer sent.
			</p>
		);
	}

	return (
		<form
			onSubmit={(event) => {
				event.preventDefault();
				if (!canAnswer || isSubmitting || value === "") {
					return;
				}
				answerMutation.mutate(
					{ question, value },
					{ onSuccess: () => setValue("") },
				);
			}}
		>
			<p
				id={headerId}
				className="m-0 text-[13px] font-medium text-content-primary"
			>
				{secretQuestion.header}
			</p>
			<Response id={textId} className="mt-1 text-[13px] text-content-secondary">
				{secretQuestion.question}
			</Response>
			<Input
				type="password"
				{...passwordManagerIgnoreProps}
				aria-labelledby={`${headerId} ${textId}`}
				aria-describedby={helperId}
				spellCheck={false}
				className="mt-2"
				value={value}
				onChange={(event) => setValue(event.target.value)}
				disabled={!canAnswer || isSubmitting}
			/>
			<p id={helperId} className="mt-1 text-xs text-content-secondary">
				The subagent's tools get this value, but the model never sees it.
			</p>
			{answerMutation.isError && (
				<div
					role="alert"
					className="mt-3 flex items-center gap-1.5 text-[13px] text-content-destructive"
				>
					<TriangleAlertIcon className="size-3.5 shrink-0" />
					<span>
						{getErrorMessage(
							answerMutation.error,
							"The answer failed to send.",
						)}
					</span>
				</div>
			)}
			<Button
				type="submit"
				size="sm"
				variant="outline"
				className="mt-3"
				disabled={!canAnswer || isSubmitting || value === ""}
			>
				{isSubmitting && (
					<LoaderIcon className="size-3.5 animate-spin motion-reduce:animate-none" />
				)}
				{isSubmitting ? "Submitting..." : "Submit"}
			</Button>
		</form>
	);
};
