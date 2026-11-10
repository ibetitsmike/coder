// This file is fork code: the fork-nested-subagents experiment's dock for
// questions that descendant chats ask the user. Upstream's ChatPageContent.tsx
// renders it once, next to AskUserQuestionDock.

import { cn } from "cn";
import { MessageCircleQuestionIcon } from "lucide-react";
import type React from "react";
import { useId, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { Link, useLocation } from "react-router";
import {
	answerChatDescendantQuestion,
	chatDescendantQuestions,
} from "#/api/forkChatQuestions";
import type { Chat } from "#/api/typesGenerated";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { chatWidthClass, useChatFullWidth } from "../hooks/useChatFullWidth";
import { AskUserQuestionForm } from "./ChatElements/tools/AskUserQuestionTool";
import { getQuestionCallKey } from "./ChatElements/tools/questionDraft";

type ForkDescendantQuestionDockProps = {
	chat: Pick<Chat, "id" | "parent_chat_id">;
	/** The viewer cannot act on this chat: it is archived, or another user's. */
	isReadOnly: boolean;
};

/**
 * Shows a root chat's oldest pending descendant question. The answer goes to
 * the descendant chat that asked it, not to the root chat.
 */
export const ForkDescendantQuestionDock: React.FC<
	ForkDescendantQuestionDockProps
> = ({ chat, isReadOnly }) => {
	const { experiments } = useDashboard();
	if (
		!experiments.includes("fork-nested-subagents") ||
		chat.parent_chat_id ||
		isReadOnly
	) {
		return null;
	}
	return <DescendantQuestionDock rootChatId={chat.id} />;
};

const DescendantQuestionDock: React.FC<{ rootChatId: string }> = ({
	rootChatId,
}) => {
	const [chatFullWidth] = useChatFullWidth();
	const location = useLocation();
	const scrollerRef = useRef<HTMLDivElement>(null);
	const headingId = useId();
	const queryClient = useQueryClient();
	const questionsQuery = useQuery(chatDescendantQuestions(rootChatId));
	const answerMutation = useMutation(
		answerChatDescendantQuestion(queryClient, rootChatId),
	);

	const pendingQuestions = questionsQuery.data ?? [];
	const question = pendingQuestions.at(0);
	if (!question) {
		return null;
	}
	const questionKey = getQuestionCallKey(
		question.tool_call_id,
		question.created_at,
	);

	return (
		<div
			// Keying the scroller by call gives the next question a fresh form
			// that starts scrolled to the top.
			key={questionKey}
			ref={scrollerRef}
			className="flex max-h-[60vh] min-h-0 scroll-pb-14 flex-col overflow-y-auto px-4 scrollbar-gutter-stable scrollbar-thin"
		>
			<section
				aria-labelledby={headingId}
				className={cn(
					"mx-auto mb-2 w-full shrink-0 rounded-lg border border-solid border-border bg-surface-primary shadow-sm",
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
					<AskUserQuestionForm
						questions={question.questions.map((item) => ({
							header: item.header,
							question: item.question,
							options: [...item.options],
						}))}
						asyncToolCallId={question.tool_call_id}
						draftKey={questionKey}
						showSubmittedAnswer
						onSubmitAnswer={async (text) => {
							await answerMutation.mutateAsync({
								chatId: question.chat_id,
								text,
							});
						}}
						hasStickyActions
						onStepChange={() => {
							if (scrollerRef.current) {
								scrollerRef.current.scrollTop = 0;
							}
						}}
					/>
				</div>
			</section>
		</div>
	);
};
