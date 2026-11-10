// This file is fork code: it finds the secret questions that descendant chats
// of the fork-nested-subagents experiment ask, so Tool.tsx, an upstream file,
// only calls it.

import { asRecord, parseArgs } from "./utils";

/**
 * Reports whether an ask_user_question call holds a secret question. The
 * root chat's dock takes its answer, which must never be a chat message.
 */
export const hasSecretQuestion = (args: unknown): boolean => {
	const questions = parseArgs(args)?.questions;
	return (
		Array.isArray(questions) &&
		questions.some((question) => asRecord(question)?.secret === true)
	);
};
