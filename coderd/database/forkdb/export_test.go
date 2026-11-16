package forkdb

// ListReadyChatWakeupWatchesSQL is the statement of ListReadyChatWakeupWatches,
// so a test can EXPLAIN it.
const ListReadyChatWakeupWatchesSQL = listReadyChatWakeupWatches

// ListDueChatWakeupsSQL is the statement of ListDueChatWakeups, so a test
// can EXPLAIN it.
const ListDueChatWakeupsSQL = listDueChatWakeups

// ClaimDuePullRequestWatchesSQL is the statement of
// ClaimDuePullRequestWatches, so a test can EXPLAIN it.
const ClaimDuePullRequestWatchesSQL = claimDuePullRequestWatches

// DeleteOldChatWakeupsSQL and DeleteOldWorkflowRunsSQL are the statements of
// the dbpurge batches, so a test can EXPLAIN them.
const (
	DeleteOldChatWakeupsSQL  = deleteOldChatWakeups
	DeleteOldWorkflowRunsSQL = deleteOldWorkflowRuns
)
