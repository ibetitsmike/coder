import { describe, expect, it } from "vitest";
import { hasSecretQuestion } from "./forkSecretQuestions";

const secretQuestions = [
	{ header: "Token", question: "Paste the token.", options: [], secret: true },
];
const ordinaryQuestions = [
	{
		header: "Token",
		question: "Paste the token?",
		options: [{ label: "Now" }, { label: "Later" }],
	},
];

describe("hasSecretQuestion", () => {
	it("finds a secret question in the arguments", () => {
		expect(hasSecretQuestion({ questions: secretQuestions }, {})).toBe(true);
		expect(
			hasSecretQuestion(JSON.stringify({ questions: secretQuestions }), {}),
		).toBe(true);
	});

	// getAskUserQuestions renders the result's questions when the arguments
	// have none, so the row must not take an answer for those either.
	it("finds a secret question in the result when the arguments have none", () => {
		expect(hasSecretQuestion("not json", { questions: secretQuestions })).toBe(
			true,
		);
		expect(
			hasSecretQuestion(undefined, {
				output: JSON.stringify({ questions: secretQuestions }),
			}),
		).toBe(true);
	});

	it("leaves ordinary questions answerable", () => {
		expect(
			hasSecretQuestion(
				{ questions: ordinaryQuestions },
				{ questions: ordinaryQuestions },
			),
		).toBe(false);
		expect(hasSecretQuestion(undefined, undefined)).toBe(false);
	});
});
