import type { ChatDescendantQuestion } from "#/api/typesGenerated";
import { MOCK_TIMESTAMP } from "./chatEntities";

export const MockChatDescendantQuestion: ChatDescendantQuestion = {
	chat_id: "grandchild-chat",
	parent_chat_id: "child-chat",
	chat_title: "Migrate the billing tables",
	tool_call_id: "call-1",
	created_at: MOCK_TIMESTAMP,
	questions: [
		{
			header: "Backfill",
			question: "Should the migration backfill rows written before 2024?",
			options: [{ label: "Backfill every row" }, { label: "Skip old rows" }],
		},
	],
};
