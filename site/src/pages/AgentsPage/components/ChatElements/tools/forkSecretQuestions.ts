// This file is fork code: it finds the secret questions that descendant chats
// of the fork-nested-subagents experiment ask, so Tool.tsx, an upstream file,
// only calls it.

import { asRecord, findAskUserQuestionResultRecord, parseArgs } from "./utils";

const holdsSecret = (questions: unknown): boolean =>
	Array.isArray(questions) &&
	questions.some((question) => asRecord(question)?.secret === true);

/**
 * Reports whether an ask_user_question call holds a secret question. The
 * root chat's dock takes its answer, which must never be a chat message. It
 * reads the result too, because getAskUserQuestions renders the result's
 * questions when the arguments have none.
 */
export const hasSecretQuestion = (args: unknown, result: unknown): boolean =>
	holdsSecret(parseArgs(args)?.questions) ||
	holdsSecret(findAskUserQuestionResultRecord(result)?.questions);
