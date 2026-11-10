// This file is fork code: helpers shared by the fork tool cards.

import { asString, parseArgs } from "./utils";

/** Returns the error text of a failed tool call's result. */
export const forkToolErrorMessage = (result: unknown): string => {
	const record = parseArgs(result);
	return record ? asString(record.error || record.message) : asString(result);
};
