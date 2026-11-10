import { screen } from "@testing-library/react";
import { QueryClientProvider } from "react-query";
import { describe, expect, it, vi } from "vitest";
import {
	createTestQueryClient,
	renderComponent,
} from "#/testHelpers/renderHelpers";
import { Tool } from "./Tool";

const renderQuestion = (question: Record<string, unknown>) =>
	renderComponent(
		<QueryClientProvider client={createTestQueryClient()}>
			<Tool
				name="ask_user_question"
				toolCallId="call-1"
				status="completed"
				args={{
					questions: [
						{ header: "Token", question: "Paste the token.", ...question },
					],
				}}
				result={{}}
				isError={false}
				isChatCompleted
				isLatestAskUserQuestion
				onSendAskUserQuestionResponse={vi.fn()}
				organizationId="organization-id"
				mcpServers={[]}
				subagentTitles={new Map()}
				subagentVariants={new Map()}
				shellToolDisplayMode="auto"
				codeDiffDisplayMode="auto"
			/>
		</QueryClientProvider>,
	);

describe("ask_user_question rows", () => {
	it("take an answer to an ordinary question", () => {
		renderQuestion({ options: [{ label: "Paste it later" }] });

		expect(screen.getByRole("button", { name: "Submit" })).toBeInTheDocument();
	});

	// The root chat's dock takes a secret answer. A chat message would put
	// the value in the transcript, and a draft would keep it in local storage.
	it("take no answer to a secret question", () => {
		renderQuestion({ secret: true });

		expect(screen.getByText("Paste the token.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Submit" })).toBeNull();
		expect(screen.queryByRole("textbox")).toBeNull();
	});
});
