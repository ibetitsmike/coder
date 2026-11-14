-- name: UpsertChatDescendantQuestion :one
-- A new question of the chat replaces the old one and is pending. The root
-- comes from chats.root_chat_id; a chat without one names itself.
INSERT INTO chat_descendant_questions (chat_id, root_chat_id, tool_call_id, questions)
SELECT c.id, COALESCE(c.root_chat_id, c.id), @tool_call_id::text, @questions::jsonb
FROM chats c
WHERE c.id = @chat_id::uuid
ON CONFLICT (chat_id) DO UPDATE SET
    root_chat_id = EXCLUDED.root_chat_id,
    tool_call_id = EXCLUDED.tool_call_id,
    questions = EXCLUDED.questions,
    created_at = now(),
    answered_at = NULL,
    answered_by = NULL
RETURNING *;

-- name: GetPendingChatDescendantQuestion :one
SELECT *
FROM chat_descendant_questions
WHERE chat_id = @chat_id::uuid
    AND answered_at IS NULL;

-- name: RetireChatDescendantQuestion :execrows
-- Marks the pending question of @chat_id answered by the user messages that
-- the transaction inserts into the chat's history. @compressed is true when
-- one of them is a context boundary, a clear or a compaction, which retires
-- any question. @visible is true when one is user-visible, which answers a
-- question that is not secret; it fails closed like
-- ChatDescendantQuestion.Secret. @answers holds the text of the visible
-- messages' text parts after chatstate.QuestionAnswerPrefix. Only an answer
-- that names the question's tool call answers a secret question.
UPDATE chat_descendant_questions q
SET answered_at = now(),
    answered_by = CASE
        WHEN EXISTS (SELECT 1 FROM unnest(@answers::text[]) AS a WHERE starts_with(a, q.tool_call_id || ':'))
            THEN 'secret_answer'
        WHEN @visible::boolean
                AND jsonb_typeof(q.questions) = 'array'
                AND NOT jsonb_path_exists(q.questions, '$[*] ? (@.secret == true)')
            THEN 'message'
        ELSE 'context_boundary'
    END
WHERE q.chat_id = @chat_id::uuid
    AND q.answered_at IS NULL
    AND (
        @compressed::boolean
        OR (@visible::boolean
            AND jsonb_typeof(q.questions) = 'array'
            AND NOT jsonb_path_exists(q.questions, '$[*] ? (@.secret == true)'))
        OR EXISTS (SELECT 1 FROM unnest(@answers::text[]) AS a WHERE starts_with(a, q.tool_call_id || ':'))
    );

-- name: ListPendingChatDescendantQuestionsByRoot :many
-- Lists the unanswered questions of the unarchived descendants of
-- @root_chat_id, oldest first.
SELECT q.chat_id, q.tool_call_id, q.questions, q.created_at,
    c.title AS chat_title, c.parent_chat_id
FROM chat_descendant_questions q
JOIN chats c ON c.id = q.chat_id
WHERE q.root_chat_id = @root_chat_id::uuid
    AND q.answered_at IS NULL
    AND q.chat_id <> @root_chat_id::uuid
    AND c.archived = false
ORDER BY q.created_at, q.chat_id;
