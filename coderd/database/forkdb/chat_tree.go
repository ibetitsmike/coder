package forkdb

import "github.com/google/uuid"

// DescendantsByLevel returns the rows reached from roots through their
// parent_chat_id links, at most levels below roots, level by level. Each
// level keeps the order of rows. A row whose parent was not reached is
// dropped, so a chat that the query or authorization filtered out hides
// its own subtree.
func DescendantsByLevel(rows []GetDescendantChatsByRootIDsRow, roots []uuid.UUID, levels int) []GetDescendantChatsByRootIDsRow {
	reached := make(map[uuid.UUID]struct{}, len(roots)+len(rows))
	level := make(map[uuid.UUID]struct{}, len(roots))
	for _, id := range roots {
		reached[id] = struct{}{}
		level[id] = struct{}{}
	}
	var out []GetDescendantChatsByRootIDsRow
	remaining := rows
	for depth := 0; len(level) > 0 && depth < levels; depth++ {
		next := make(map[uuid.UUID]struct{})
		rest := remaining[:0:0]
		for _, row := range remaining {
			chat := row.ChatsExpanded
			if _, ok := reached[chat.ID]; ok {
				continue
			}
			if _, ok := level[chat.ParentChatID.UUID]; !ok || !chat.ParentChatID.Valid {
				rest = append(rest, row)
				continue
			}
			reached[chat.ID] = struct{}{}
			next[chat.ID] = struct{}{}
			out = append(out, row)
		}
		level, remaining = next, rest
	}
	return out
}
