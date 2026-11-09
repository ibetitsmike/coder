-- name: GetChatTodosByChatID :one
SELECT * FROM chat_todos WHERE chat_id = @chat_id::uuid;

-- name: UpsertChatTodos :one
INSERT INTO chat_todos (chat_id, items, version, updated_at)
VALUES (@chat_id, @items, 1, @updated_at)
ON CONFLICT (chat_id) DO UPDATE SET items = EXCLUDED.items, version = chat_todos.version + 1, updated_at = EXCLUDED.updated_at
RETURNING *;
