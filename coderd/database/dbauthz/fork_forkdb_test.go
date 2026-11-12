package dbauthz_test

// This file is fork code: it tests the authorization of the fork queries in
// fork_forkdb.go like MethodTestSuite tests upstream's queries. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"testing"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/forkdb/forkdbmock"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/coderd/rbac/rolestore"
	"github.com/coder/coder/v2/testutil"
)

func init() {
	// forkdb.From calls these to reach the fork queries, which
	// TestForkQuerier tests.
	skipMethods["ForkUnwrap"] = "Not relevant"
	skipMethods["ForkWrap"] = "Not relevant"
}

// forkMethodCase gets the upstream store too, because a fork query of a
// chat-owned table loads the chat from it to authorize.
type forkMethodCase func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects)

// TestForkQuerier runs the checks of MethodTestSuite.SubtestWithDB on every
// fork query: it fails when the actor may not run the query or is missing,
// and it asserts the exact RBAC checks on success.
func TestForkQuerier(t *testing.T) {
	t.Parallel()

	cases := map[string]forkMethodCase{
		"CountActiveSubagentsForCapacity": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := testutil.Fake(t, faker, forkdb.CountActiveSubagentsForCapacityParams{})
			row := testutil.Fake(t, faker, forkdb.CountActiveSubagentsForCapacityRow{})
			fdb.EXPECT().CountActiveSubagentsForCapacity(gomock.Any(), arg).Return(row, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceChat, policy.ActionRead).Returns(row)
		},
		"CountLiveSubagentsInFamily": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().CountLiveSubagentsInFamily(gomock.Any(), chat.ID).Return(int64(3), nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(int64(3))
		},
		"GetFamilySubagentsAwaitingWorker": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.GetFamilySubagentsAwaitingWorkerParams{RootChatID: chat.ID, OrganizationID: chat.OrganizationID, StaleSeconds: 30}
			rows := []forkdb.GetFamilySubagentsAwaitingWorkerRow{{ChatID: uuid.New(), ActiveFamilyCount: 1}}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetFamilySubagentsAwaitingWorker(gomock.Any(), arg).Return(rows, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionRead).Returns(rows)
		},
		"GetChatAwaitingWorker": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := testutil.Fake(t, faker, forkdb.GetChatAwaitingWorkerParams{})
			fdb.EXPECT().GetChatAwaitingWorker(gomock.Any(), arg).Return(true, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceChat, policy.ActionRead).Returns(true)
		},
		"GetChatOrganizationSubagentLimits": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			limits := testutil.Fake(t, faker, forkdb.ChatOrganizationSubagentLimit{})
			fdb.EXPECT().GetChatOrganizationSubagentLimits(gomock.Any(), limits.OrganizationID).Return(limits, nil).AnyTimes()
			check.Args(limits.OrganizationID).
				Asserts(rbac.ResourceChatModelConfig.InOrg(limits.OrganizationID), policy.ActionRead).
				Returns(limits)
		},
		"GetChatOrganizationWakeupCaps": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			caps := testutil.Fake(t, faker, forkdb.ChatOrganizationWakeupCap{})
			fdb.EXPECT().GetChatOrganizationWakeupCaps(gomock.Any(), caps.OrganizationID).Return(caps, nil).AnyTimes()
			check.Args(caps.OrganizationID).
				Asserts(rbac.ResourceChatModelConfig.InOrg(caps.OrganizationID), policy.ActionRead).
				Returns(caps)
		},
		"UpsertChatOrganizationWakeupCaps": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			caps := testutil.Fake(t, faker, forkdb.ChatOrganizationWakeupCap{})
			arg := forkdb.UpsertChatOrganizationWakeupCapsParams{OrganizationID: caps.OrganizationID, MaxChatRunsPerDay: caps.MaxChatRunsPerDay}
			fdb.EXPECT().UpsertChatOrganizationWakeupCaps(gomock.Any(), arg).Return(caps, nil).AnyTimes()
			check.Args(arg).
				Asserts(rbac.ResourceChatModelConfig.InOrg(caps.OrganizationID), policy.ActionUpdate).
				Returns(caps)
		},
		"UpsertChatOrganizationSubagentLimits": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			limits := testutil.Fake(t, faker, forkdb.ChatOrganizationSubagentLimit{})
			arg := forkdb.UpsertChatOrganizationSubagentLimitsParams{OrganizationID: limits.OrganizationID, MaxDepth: limits.MaxDepth}
			fdb.EXPECT().UpsertChatOrganizationSubagentLimits(gomock.Any(), arg).Return(limits, nil).AnyTimes()
			check.Args(arg).
				Asserts(rbac.ResourceChatModelConfig.InOrg(limits.OrganizationID), policy.ActionUpdate).
				Returns(limits)
		},
		"GetChatSubagentByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			sub := testutil.Fake(t, faker, forkdb.ChatSubagent{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatSubagentByChatID(gomock.Any(), chat.ID).Return(sub, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(sub)
		},
		"GetChatSubagentTypesByChatIDs": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			rows := []forkdb.GetChatSubagentTypesByChatIDsRow{testutil.Fake(t, faker, forkdb.GetChatSubagentTypesByChatIDsRow{})}
			ids := []uuid.UUID{rows[0].ChatID}
			fdb.EXPECT().GetChatSubagentTypesByChatIDs(gomock.Any(), ids).Return(rows, nil).AnyTimes()
			check.Args(ids).Asserts(rbac.ResourceSystem, policy.ActionRead).Returns(rows)
		},
		"GetChatSubagentsByParentChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			subs := []forkdb.ChatSubagent{testutil.Fake(t, faker, forkdb.ChatSubagent{})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatSubagentsByParentChatID(gomock.Any(), chat.ID).Return(subs, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(subs)
		},
		"GetChatSubtreeIDs": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			rows := []forkdb.GetChatSubtreeIDsRow{testutil.Fake(t, faker, forkdb.GetChatSubtreeIDsRow{})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatSubtreeIDs(gomock.Any(), chat.ID).Return(rows, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(rows)
		},
		"GetChatTodosByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			todos := testutil.Fake(t, faker, forkdb.ChatTodo{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatTodosByChatID(gomock.Any(), chat.ID).Return(todos, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(todos)
		},
		"GetPendingChatDescendantQuestion": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			question := testutil.Fake(t, faker, forkdb.ChatDescendantQuestion{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetPendingChatDescendantQuestion(gomock.Any(), chat.ID).Return(question, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(question)
		},
		"ListPendingChatDescendantQuestionsByRoot": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			root := testutil.Fake(t, faker, database.Chat{})
			rows := []forkdb.ListPendingChatDescendantQuestionsByRootRow{testutil.Fake(t, faker, forkdb.ListPendingChatDescendantQuestionsByRootRow{})}
			db.EXPECT().GetChatByID(gomock.Any(), root.ID).Return(root, nil).AnyTimes()
			fdb.EXPECT().ListPendingChatDescendantQuestionsByRoot(gomock.Any(), root.ID).Return(rows, nil).AnyTimes()
			check.Args(root.ID).Asserts(root, policy.ActionRead).Returns(rows)
		},
		"UpsertChatDescendantQuestion": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.UpsertChatDescendantQuestionParams{ChatID: chat.ID})
			question := testutil.Fake(t, faker, forkdb.ChatDescendantQuestion{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().UpsertChatDescendantQuestion(gomock.Any(), arg).Return(question, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(question)
		},
		"InsertChatSecretAnswer": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.InsertChatSecretAnswerParams{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatSecretAnswer(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"GetChatSecretAnswersByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			answers := []forkdb.ChatSecretAnswer{testutil.Fake(t, faker, forkdb.ChatSecretAnswer{ChatID: chat.ID})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatSecretAnswersByChatID(gomock.Any(), chat.ID).Return(answers, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionUpdate).Returns(answers)
		},
		"DeleteChatSecretAnswer": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.DeleteChatSecretAnswerParams{ChatID: chat.ID, ToolCallID: "call_1"}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().DeleteChatSecretAnswer(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"DeleteOldChatSecretAnswers": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			fdb.EXPECT().DeleteOldChatSecretAnswers(gomock.Any(), int32(1000)).Return(int64(1), nil).AnyTimes()
			check.Args(int32(1000)).Asserts(rbac.ResourceSystem, policy.ActionDelete).Returns(int64(1))
		},
		"LockChatForNoKeyUpdate": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().LockChatForNoKeyUpdate(gomock.Any(), chat.ID).Return(chat.ID, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionUpdate).Returns(chat.ID)
		},
		"InsertChatSubagent": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.InsertChatSubagentParams{ChatID: chat.ID})
			sub := testutil.Fake(t, faker, forkdb.ChatSubagent{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatSubagent(gomock.Any(), arg).Return(sub, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(sub)
		},
		"UpdateChatSubagentWorkspaceID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.UpdateChatSubagentWorkspaceIDParams{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().UpdateChatSubagentWorkspaceID(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"UpsertChatTodos": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.UpsertChatTodosParams{ChatID: chat.ID})
			todos := testutil.Fake(t, faker, forkdb.ChatTodo{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().UpsertChatTodos(gomock.Any(), arg).Return(todos, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(todos)
		},
		"InsertChatWakeup": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.InsertChatWakeupParams{ChatID: chat.ID})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatWakeup(gomock.Any(), arg).Return(wakeup, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(wakeup)
		},
		"GetChatWakeupByID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByID(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			check.Args(wakeup.ID).Asserts(chat, policy.ActionRead).Returns(wakeup)
		},
		"GetChatWakeupByIDForUpdate": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByID(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByIDForUpdate(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			check.Args(wakeup.ID).Asserts(chat, policy.ActionUpdate).Returns(wakeup)
		},
		"ListChatWakeupsByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeups := []forkdb.ChatWakeup{testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().ListChatWakeupsByChatID(gomock.Any(), chat.ID).Return(wakeups, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(wakeups)
		},
		"ListChildChatIDsWithActiveEventsWakeups": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			parent := testutil.Fake(t, faker, database.Chat{})
			ids := []uuid.UUID{uuid.New()}
			db.EXPECT().GetChatByID(gomock.Any(), parent.ID).Return(parent, nil).AnyTimes()
			fdb.EXPECT().ListChildChatIDsWithActiveEventsWakeups(gomock.Any(), parent.ID).Return(ids, nil).AnyTimes()
			check.Args(parent.ID).Asserts(parent, policy.ActionRead).Returns(ids)
		},
		"UpdateChatWakeupStatus": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			arg := testutil.Fake(t, faker, forkdb.UpdateChatWakeupStatusParams{ID: wakeup.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByID(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			fdb.EXPECT().UpdateChatWakeupStatus(gomock.Any(), arg).Return(wakeup, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(wakeup)
		},
		"CancelChatWakeupsByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.CancelChatWakeupsByChatIDParams{ChatID: chat.ID})
			wakeups := []forkdb.ChatWakeup{testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().CancelChatWakeupsByChatID(gomock.Any(), arg).Return(wakeups, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(wakeups)
		},
		"ClaimDueChatWakeups": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := testutil.Fake(t, faker, forkdb.ClaimDueChatWakeupsParams{})
			wakeups := []forkdb.ChatWakeup{testutil.Fake(t, faker, forkdb.ChatWakeup{})}
			fdb.EXPECT().ClaimDueChatWakeups(gomock.Any(), arg).Return(wakeups, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceChat, policy.ActionUpdate).Returns(wakeups)
		},
		"FinishChatWakeupClaim": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			arg := testutil.Fake(t, faker, forkdb.FinishChatWakeupClaimParams{ID: wakeup.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByID(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			fdb.EXPECT().FinishChatWakeupClaim(gomock.Any(), arg).Return(wakeup, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(wakeup)
		},
		"InsertChatWakeupRun": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			arg := testutil.Fake(t, faker, forkdb.InsertChatWakeupRunParams{WakeupID: wakeup.ID})
			run := testutil.Fake(t, faker, forkdb.ChatWakeupRun{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByID(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			fdb.EXPECT().InsertChatWakeupRun(gomock.Any(), arg).Return(run, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(run)
		},
		"ListChatWakeupRunKeys": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			wakeup := testutil.Fake(t, faker, forkdb.ChatWakeup{ChatID: chat.ID})
			keys := []string{"child_done:" + uuid.NewString()}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatWakeupByID(gomock.Any(), wakeup.ID).Return(wakeup, nil).AnyTimes()
			fdb.EXPECT().ListChatWakeupRunKeys(gomock.Any(), wakeup.ID).Return(keys, nil).AnyTimes()
			check.Args(wakeup.ID).Asserts(chat, policy.ActionRead).Returns(keys)
		},
		"CountChatWakeupRunsSince": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.CountChatWakeupRunsSinceParams{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().CountChatWakeupRunsSince(gomock.Any(), arg).Return(int64(4), nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionRead).Returns(int64(4))
		},
		"CountOrganizationChatWakeupRunsSince": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := testutil.Fake(t, faker, forkdb.CountOrganizationChatWakeupRunsSinceParams{})
			fdb.EXPECT().CountOrganizationChatWakeupRunsSince(gomock.Any(), arg).Return(int64(9), nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceChat, policy.ActionRead).Returns(int64(9))
		},
		"SumChatCostMicrosSince": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.SumChatCostMicrosSinceParams{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().SumChatCostMicrosSince(gomock.Any(), arg).Return(int64(1500), nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionRead).Returns(int64(1500))
		},
		"SumOrganizationWakeupChatCostMicrosSince": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := testutil.Fake(t, faker, forkdb.SumOrganizationWakeupChatCostMicrosSinceParams{})
			fdb.EXPECT().SumOrganizationWakeupChatCostMicrosSince(gomock.Any(), arg).Return(int64(2500), nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceChat, policy.ActionRead).Returns(int64(2500))
		},
		"CountChatUserNotificationsSince": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := testutil.Fake(t, faker, forkdb.CountChatUserNotificationsSinceParams{})
			fdb.EXPECT().CountChatUserNotificationsSince(gomock.Any(), arg).Return(int64(3), nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceNotificationMessage, policy.ActionRead).Returns(int64(3))
		},
		"InsertPluginPack": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			arg := forkdb.InsertPluginPackParams{ID: pack.ID, OrganizationID: pack.OrganizationID, Name: pack.Name}
			fdb.EXPECT().InsertPluginPack(gomock.Any(), arg).Return(pack, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceOrganizationSkill.InOrg(pack.OrganizationID), policy.ActionCreate).Returns(pack)
		},
		"GetPluginPackByID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			fdb.EXPECT().GetPluginPackByID(gomock.Any(), pack.ID).Return(pack, nil).AnyTimes()
			check.Args(pack.ID).Asserts(pluginPackObject(pack), policy.ActionRead).Returns(pack)
		},
		"GetPluginPacksByOrganizationID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			fdb.EXPECT().GetPluginPacksByOrganizationID(gomock.Any(), pack.OrganizationID).Return([]forkdb.PluginPack{pack}, nil).AnyTimes()
			check.Args(pack.OrganizationID).
				Asserts(rbac.ResourceOrganizationSkill.InOrg(pack.OrganizationID), policy.ActionRead).
				Returns([]forkdb.PluginPack{pack})
		},
		"InsertPluginPackProject": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			arg := forkdb.InsertPluginPackProjectParams{ProjectID: project.ID, PackID: uuid.New()}
			row := forkdb.PluginPackProject{ProjectID: project.ID, PackID: arg.PackID}
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().InsertPluginPackProject(gomock.Any(), arg).Return(row, nil).AnyTimes()
			check.Args(arg).Asserts(project, policy.ActionUpdate).Returns(row)
		},
		"DeletePluginPackProject": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			arg := forkdb.DeletePluginPackProjectParams{ProjectID: project.ID, PackID: uuid.New()}
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().DeletePluginPackProject(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(project, policy.ActionUpdate).Returns()
		},
		"GetPluginPacksByProjectID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			rows := []forkdb.GetPluginPacksByProjectIDRow{testutil.Fake(t, faker, forkdb.GetPluginPacksByProjectIDRow{})}
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().GetPluginPacksByProjectID(gomock.Any(), project.ID).Return(rows, nil).AnyTimes()
			check.Args(project.ID).Asserts(project, policy.ActionRead).Returns(rows)
		},
		"GetChatPinnedSkillsByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			rows := []forkdb.ChatPinnedSkill{{ChatID: chat.ID, SkillSource: "personal", SkillName: "deploy"}}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatPinnedSkillsByChatID(gomock.Any(), chat.ID).Return(rows, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(rows)
		},
		"DeleteChatPinnedSkills": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().DeleteChatPinnedSkills(gomock.Any(), chat.ID).Return(nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"InsertChatPinnedSkills": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.InsertChatPinnedSkillsParams{ChatID: chat.ID, SkillSources: []string{"personal"}, SkillNames: []string{"deploy"}}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatPinnedSkills(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"InsertChatExcludedPluginPack": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.InsertChatExcludedPluginPackParams{ChatID: chat.ID, PackID: uuid.New()}
			row := forkdb.ChatExcludedPluginPack{ChatID: chat.ID, PackID: arg.PackID}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatExcludedPluginPack(gomock.Any(), arg).Return(row, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(row)
		},
		"DeleteChatExcludedPluginPack": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.DeleteChatExcludedPluginPackParams{ChatID: chat.ID, PackID: uuid.New()}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().DeleteChatExcludedPluginPack(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"GetExcludedPluginPacksByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			rows := []forkdb.GetExcludedPluginPacksByChatIDRow{testutil.Fake(t, faker, forkdb.GetExcludedPluginPacksByChatIDRow{})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetExcludedPluginPacksByChatID(gomock.Any(), chat.ID).Return(rows, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(rows)
		},
		"InsertChatPluginPack": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.InsertChatPluginPackParams{ChatID: chat.ID, PackID: uuid.New()}
			row := forkdb.ChatPluginPack{ChatID: chat.ID, PackID: arg.PackID}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatPluginPack(gomock.Any(), arg).Return(row, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(row)
		},
		"DeleteChatPluginPack": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := forkdb.DeleteChatPluginPackParams{ChatID: chat.ID, PackID: uuid.New()}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().DeleteChatPluginPack(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"GetPluginPacksByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			rows := []forkdb.GetPluginPacksByChatIDRow{testutil.Fake(t, faker, forkdb.GetPluginPacksByChatIDRow{})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetPluginPacksByChatID(gomock.Any(), chat.ID).Return(rows, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(rows)
		},
		"UpdatePluginPackCommit": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			arg := forkdb.UpdatePluginPackCommitParams{ID: pack.ID, Ref: "v2", CommitSha: pack.CommitSha}
			fdb.EXPECT().GetPluginPackByID(gomock.Any(), pack.ID).Return(pack, nil).AnyTimes()
			fdb.EXPECT().UpdatePluginPackCommit(gomock.Any(), arg).Return(pack, nil).AnyTimes()
			check.Args(arg).Asserts(pluginPackObject(pack), policy.ActionUpdate).Returns(pack)
		},
		"DeletePluginPackByID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			fdb.EXPECT().GetPluginPackByID(gomock.Any(), pack.ID).Return(pack, nil).AnyTimes()
			fdb.EXPECT().DeletePluginPackByID(gomock.Any(), pack.ID).Return(nil).AnyTimes()
			check.Args(pack.ID).Asserts(pluginPackObject(pack), policy.ActionDelete).Returns()
		},
		"InsertPluginPackResources": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			arg := forkdb.InsertPluginPackResourcesParams{PackID: pack.ID, Paths: []string{"SKILL.md"}, Contents: [][]byte{[]byte("x")}, ContentHashes: []string{"h"}}
			fdb.EXPECT().GetPluginPackByID(gomock.Any(), pack.ID).Return(pack, nil).AnyTimes()
			fdb.EXPECT().InsertPluginPackResources(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(pluginPackObject(pack), policy.ActionUpdate).Returns()
		},
		"DeletePluginPackResources": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			fdb.EXPECT().GetPluginPackByID(gomock.Any(), pack.ID).Return(pack, nil).AnyTimes()
			fdb.EXPECT().DeletePluginPackResources(gomock.Any(), pack.ID).Return(nil).AnyTimes()
			check.Args(pack.ID).Asserts(pluginPackObject(pack), policy.ActionUpdate).Returns()
		},
		"GetPluginPackResources": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			pack := testutil.Fake(t, faker, forkdb.PluginPack{})
			resources := []forkdb.PluginPackResource{{PackID: pack.ID, Path: "SKILL.md", Content: []byte("x"), ContentHash: "h"}}
			fdb.EXPECT().GetPluginPackByID(gomock.Any(), pack.ID).Return(pack, nil).AnyTimes()
			fdb.EXPECT().GetPluginPackResources(gomock.Any(), pack.ID).Return(resources, nil).AnyTimes()
			check.Args(pack.ID).Asserts(pluginPackObject(pack), policy.ActionRead).Returns(resources)
		},
		"GetPluginPackSources": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			row := testutil.Fake(t, faker, forkdb.PluginPackSource{})
			fdb.EXPECT().GetPluginPackSources(gomock.Any(), row.OrganizationID).Return(row, nil).AnyTimes()
			check.Args(row.OrganizationID).Asserts(rbac.ResourceOrganizationSkill.InOrg(row.OrganizationID), policy.ActionRead).Returns(row)
		},
		"UpsertPluginPackSources": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			row := testutil.Fake(t, faker, forkdb.PluginPackSource{})
			arg := forkdb.UpsertPluginPackSourcesParams{OrganizationID: row.OrganizationID, Hosts: []string{"github.com"}}
			fdb.EXPECT().UpsertPluginPackSources(gomock.Any(), arg).Return(row, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceOrganizationSkill.InOrg(row.OrganizationID), policy.ActionUpdate).Returns(row)
		},
		"InsertPlugin": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			arg := forkdb.InsertPluginParams{ID: plugin.ID, OrganizationID: plugin.OrganizationID, Name: plugin.Name}
			fdb.EXPECT().InsertPlugin(gomock.Any(), arg).Return(plugin, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourcePlugin.InOrg(plugin.OrganizationID), policy.ActionCreate).Returns(plugin)
		},
		"GetPluginByID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			fdb.EXPECT().GetPluginByID(gomock.Any(), plugin.ID).Return(plugin, nil).AnyTimes()
			check.Args(plugin.ID).Asserts(plugin, policy.ActionRead).Returns(plugin)
		},
		"GetPluginsByOrganizationID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			fdb.EXPECT().GetPluginsByOrganizationID(gomock.Any(), plugin.OrganizationID).Return([]forkdb.Plugin{plugin}, nil).AnyTimes()
			check.Args(plugin.OrganizationID).
				Asserts(rbac.ResourcePlugin.InOrg(plugin.OrganizationID), policy.ActionRead).
				Returns([]forkdb.Plugin{plugin})
		},
		"UpdatePluginConfig": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			arg := forkdb.UpdatePluginConfigParams{ID: plugin.ID, Config: `{"k":"v"}`}
			fdb.EXPECT().GetPluginByID(gomock.Any(), plugin.ID).Return(plugin, nil).AnyTimes()
			fdb.EXPECT().UpdatePluginConfig(gomock.Any(), arg).Return(plugin, nil).AnyTimes()
			check.Args(arg).Asserts(plugin, policy.ActionUpdate).Returns(plugin)
		},
		"UpdatePluginEnabled": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			arg := forkdb.UpdatePluginEnabledParams{ID: plugin.ID, Enabled: false}
			fdb.EXPECT().GetPluginByID(gomock.Any(), plugin.ID).Return(plugin, nil).AnyTimes()
			fdb.EXPECT().UpdatePluginEnabled(gomock.Any(), arg).Return(plugin, nil).AnyTimes()
			check.Args(arg).Asserts(plugin, policy.ActionUpdate).Returns(plugin)
		},
		"DeletePluginByID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			fdb.EXPECT().GetPluginByID(gomock.Any(), plugin.ID).Return(plugin, nil).AnyTimes()
			fdb.EXPECT().DeletePluginByID(gomock.Any(), plugin.ID).Return(nil).AnyTimes()
			check.Args(plugin.ID).Asserts(plugin, policy.ActionDelete).Returns()
		},
		"UpdatePluginLastError": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.UpdatePluginLastErrorParams{ID: uuid.New(), LastError: "boom"}
			fdb.EXPECT().UpdatePluginLastError(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns()
		},
		"AcquirePluginLeases": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			plugin := testutil.Fake(t, faker, forkdb.Plugin{})
			arg := forkdb.AcquirePluginLeasesParams{RunnerID: uuid.New(), LeaseSeconds: 30, MaxPlugins: 10}
			fdb.EXPECT().AcquirePluginLeases(gomock.Any(), arg).Return([]forkdb.Plugin{plugin}, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns([]forkdb.Plugin{plugin})
		},
		"ReleasePluginLeases": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			runnerID := uuid.New()
			fdb.EXPECT().ReleasePluginLeases(gomock.Any(), runnerID).Return(nil).AnyTimes()
			check.Args(runnerID).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns()
		},
		"InsertPluginLinkCode": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.InsertPluginLinkCodeParams{CodeHash: []byte("hash"), PluginID: uuid.New(), UserID: uuid.New(), ExpiresAt: dbtime.Now()}
			fdb.EXPECT().InsertPluginLinkCode(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionCreate).Returns()
		},
		"ConsumePluginLinkCode": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			code := testutil.Fake(t, faker, forkdb.PluginLinkCode{})
			arg := forkdb.ConsumePluginLinkCodeParams{PluginID: code.PluginID, CodeHash: code.CodeHash}
			fdb.EXPECT().ConsumePluginLinkCode(gomock.Any(), arg).Return(code, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionDelete).Returns(code)
		},
		"DeletePluginLinkCodesByUser": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.DeletePluginLinkCodesByUserParams{PluginID: uuid.New(), UserID: uuid.New()}
			fdb.EXPECT().DeletePluginLinkCodesByUser(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionDelete).Returns()
		},
		"UpsertPluginUserLink": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			link := testutil.Fake(t, faker, forkdb.PluginUserLink{})
			arg := forkdb.UpsertPluginUserLinkParams{PluginID: link.PluginID, ExternalID: link.ExternalID, UserID: link.UserID}
			fdb.EXPECT().UpsertPluginUserLink(gomock.Any(), arg).Return(link, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns(link)
		},
		"GetPluginUserLink": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			link := testutil.Fake(t, faker, forkdb.PluginUserLink{})
			arg := forkdb.GetPluginUserLinkParams{PluginID: link.PluginID, ExternalID: link.ExternalID}
			fdb.EXPECT().GetPluginUserLink(gomock.Any(), arg).Return(link, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionRead).Returns(link)
		},
		"GetPluginUserLinksByPluginID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			link := testutil.Fake(t, faker, forkdb.PluginUserLink{})
			fdb.EXPECT().GetPluginUserLinksByPluginID(gomock.Any(), link.PluginID).Return([]forkdb.PluginUserLink{link}, nil).AnyTimes()
			check.Args(link.PluginID).Asserts(rbac.ResourceSystem, policy.ActionRead).Returns([]forkdb.PluginUserLink{link})
		},
		"GetPluginUserLinksByUserID": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			link := testutil.Fake(t, faker, forkdb.PluginUserLink{})
			fdb.EXPECT().GetPluginUserLinksByUserID(gomock.Any(), link.UserID).Return([]forkdb.PluginUserLink{link}, nil).AnyTimes()
			check.Args(link.UserID).Asserts(rbac.ResourceSystem, policy.ActionRead).Returns([]forkdb.PluginUserLink{link})
		},
		"GetPluginUserLinksByPluginAndUser": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			link := testutil.Fake(t, faker, forkdb.PluginUserLink{})
			arg := forkdb.GetPluginUserLinksByPluginAndUserParams{PluginID: link.PluginID, UserID: link.UserID}
			fdb.EXPECT().GetPluginUserLinksByPluginAndUser(gomock.Any(), arg).Return([]forkdb.PluginUserLink{link}, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionRead).Returns([]forkdb.PluginUserLink{link})
		},
		"DeletePluginUserLink": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.DeletePluginUserLinkParams{PluginID: uuid.New(), ExternalID: "+15550100"}
			fdb.EXPECT().DeletePluginUserLink(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionDelete).Returns()
		},
		"SetPluginUserLinkActiveChat": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.SetPluginUserLinkActiveChatParams{PluginID: uuid.New(), ExternalID: "+15550100", ActiveChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true}}
			fdb.EXPECT().SetPluginUserLinkActiveChat(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns()
		},
		"EnsurePluginChatBindings": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			pluginID := uuid.New()
			fdb.EXPECT().EnsurePluginChatBindings(gomock.Any(), pluginID).Return(nil).AnyTimes()
			check.Args(pluginID).Asserts(rbac.ResourceSystem, policy.ActionCreate).Returns()
		},
		"GetPluginChatBindingsDue": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			row := testutil.Fake(t, faker, forkdb.GetPluginChatBindingsDueRow{})
			arg := forkdb.GetPluginChatBindingsDueParams{PluginID: row.PluginID, LimitCount: 10}
			fdb.EXPECT().GetPluginChatBindingsDue(gomock.Any(), arg).Return([]forkdb.GetPluginChatBindingsDueRow{row}, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionRead).Returns([]forkdb.GetPluginChatBindingsDueRow{row})
		},
		"UpdatePluginChatBindingDelivered": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.UpdatePluginChatBindingDeliveredParams{
				PluginID:      uuid.New(),
				ChatID:        uuid.New(),
				SentMessageID: 42,
				SentStatus:    database.NullChatStatus{ChatStatus: database.ChatStatusWaiting, Valid: true},
			}
			fdb.EXPECT().UpdatePluginChatBindingDelivered(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns()
		},
		"UpdatePluginChatBindingFailed": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.UpdatePluginChatBindingFailedParams{PluginID: uuid.New(), ChatID: uuid.New(), NextAttemptAt: dbtime.Now()}
			fdb.EXPECT().UpdatePluginChatBindingFailed(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns()
		},
		"InsertUserAgentDefinition": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := userAgentDefinition(uuid.New(), uuid.New(), "reviewer")
			arg := forkdb.InsertUserAgentDefinitionParams{OrganizationID: def.OrganizationID, UserID: def.UserID.UUID, Name: def.Name, Content: def.Content}
			fdb.EXPECT().InsertUserAgentDefinition(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(
				rbac.ResourceOrganization.WithID(def.OrganizationID).InOrg(def.OrganizationID), policy.ActionRead,
				rbac.ResourceUserSkill.WithOwner(def.UserID.UUID.String()), policy.ActionCreate,
			).Returns(def)
		},
		"GetUserAgentDefinitionByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := userAgentDefinition(uuid.New(), uuid.New(), "reviewer")
			arg := forkdb.GetUserAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, UserID: def.UserID.UUID, Name: def.Name}
			fdb.EXPECT().GetUserAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(
				rbac.ResourceOrganization.WithID(def.OrganizationID).InOrg(def.OrganizationID), policy.ActionRead,
				rbac.ResourceUserSkill.WithOwner(def.UserID.UUID.String()), policy.ActionRead,
			).Returns(def)
		},
		"ListUserAgentDefinitionMetadata": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			arg := forkdb.ListUserAgentDefinitionMetadataParams{OrganizationID: uuid.New(), UserID: uuid.New()}
			rows := []forkdb.ListUserAgentDefinitionMetadataRow{testutil.Fake(t, faker, forkdb.ListUserAgentDefinitionMetadataRow{
				OrganizationID: arg.OrganizationID,
				UserID:         uuid.NullUUID{UUID: arg.UserID, Valid: true},
			})}
			fdb.EXPECT().ListUserAgentDefinitionMetadata(gomock.Any(), arg).Return(rows, nil).AnyTimes()
			check.Args(arg).Asserts(
				rbac.ResourceOrganization.WithID(arg.OrganizationID).InOrg(arg.OrganizationID), policy.ActionRead,
				rbac.ResourceUserSkill.WithOwner(arg.UserID.String()), policy.ActionRead,
			).Returns(rows)
		},
		"UpdateUserAgentDefinitionByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := userAgentDefinition(uuid.New(), uuid.New(), "reviewer")
			arg := forkdb.UpdateUserAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, UserID: def.UserID.UUID, Name: def.Name, Enabled: sql.NullBool{Bool: false, Valid: true}}
			fdb.EXPECT().UpdateUserAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(
				rbac.ResourceOrganization.WithID(def.OrganizationID).InOrg(def.OrganizationID), policy.ActionRead,
				rbac.ResourceUserSkill.WithOwner(def.UserID.UUID.String()), policy.ActionUpdate,
			).Returns(def)
		},
		"DeleteUserAgentDefinitionByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := userAgentDefinition(uuid.New(), uuid.New(), "reviewer")
			arg := forkdb.DeleteUserAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, UserID: def.UserID.UUID, Name: def.Name}
			fdb.EXPECT().DeleteUserAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(
				rbac.ResourceOrganization.WithID(def.OrganizationID).InOrg(def.OrganizationID), policy.ActionRead,
				rbac.ResourceUserSkill.WithOwner(def.UserID.UUID.String()), policy.ActionDelete,
			).Returns(def)
		},
		"InsertOrganizationAgentDefinition": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			arg := forkdb.InsertOrganizationAgentDefinitionParams{OrganizationID: def.OrganizationID, Name: def.Name, Content: def.Content, GroupAcl: def.GroupAcl, UserAcl: def.UserAcl}
			fdb.EXPECT().InsertOrganizationAgentDefinition(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(rbac.ResourceOrganizationSkill.InOrg(def.OrganizationID), policy.ActionCreate).Returns(def)
		},
		"GetOrganizationAgentDefinitionByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			arg := forkdb.GetOrganizationAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, Name: def.Name}
			fdb.EXPECT().GetOrganizationAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(organizationAgentDefinitionObject(def), policy.ActionRead).Returns(def)
		},
		"GetOrganizationAgentDefinitionByNameForUpdate": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			arg := forkdb.GetOrganizationAgentDefinitionByNameForUpdateParams{OrganizationID: def.OrganizationID, Name: def.Name}
			fdb.EXPECT().GetOrganizationAgentDefinitionByNameForUpdate(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(organizationAgentDefinitionObject(def), policy.ActionRead).Returns(def)
		},
		"UpdateOrganizationAgentDefinitionACLByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			arg := forkdb.UpdateOrganizationAgentDefinitionACLByNameParams{OrganizationID: def.OrganizationID, Name: def.Name, GroupAcl: database.ChatACL{}, UserAcl: def.UserAcl}
			fdb.EXPECT().GetOrganizationAgentDefinitionByName(gomock.Any(), forkdb.GetOrganizationAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, Name: def.Name}).Return(def, nil).AnyTimes()
			fdb.EXPECT().UpdateOrganizationAgentDefinitionACLByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(organizationAgentDefinitionObject(def), policy.ActionShare).Returns(def)
		},
		// The NotAuthorized subtest gets no rows back: the post-filter drops
		// every row that the actor cannot read.
		"ListOrganizationAgentDefinitionMetadata": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			row := forkdb.ListOrganizationAgentDefinitionMetadataRow{ID: def.ID, OrganizationID: def.OrganizationID, Name: def.Name, GroupAcl: def.GroupAcl, UserAcl: def.UserAcl}
			fdb.EXPECT().ListOrganizationAgentDefinitionMetadata(gomock.Any(), def.OrganizationID).Return([]forkdb.ListOrganizationAgentDefinitionMetadataRow{row}, nil).AnyTimes()
			check.Args(def.OrganizationID).Asserts(organizationAgentDefinitionObject(def), policy.ActionRead).Returns([]forkdb.ListOrganizationAgentDefinitionMetadataRow{row})
		},
		"UpdateOrganizationAgentDefinitionByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			arg := forkdb.UpdateOrganizationAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, Name: def.Name, Enabled: sql.NullBool{Bool: false, Valid: true}}
			fdb.EXPECT().GetOrganizationAgentDefinitionByName(gomock.Any(), forkdb.GetOrganizationAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, Name: def.Name}).Return(def, nil).AnyTimes()
			fdb.EXPECT().UpdateOrganizationAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(organizationAgentDefinitionObject(def), policy.ActionUpdate).Returns(def)
		},
		"DeleteOrganizationAgentDefinitionByName": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			def := organizationAgentDefinition(uuid.New(), "reviewer")
			arg := forkdb.DeleteOrganizationAgentDefinitionByNameParams{OrganizationID: def.OrganizationID, Name: def.Name}
			fdb.EXPECT().GetOrganizationAgentDefinitionByName(gomock.Any(), forkdb.GetOrganizationAgentDefinitionByNameParams(arg)).Return(def, nil).AnyTimes()
			fdb.EXPECT().DeleteOrganizationAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(organizationAgentDefinitionObject(def), policy.ActionDelete).Returns(def)
		},
		"InsertProjectAgentDefinition": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			arg := forkdb.InsertProjectAgentDefinitionParams{OrganizationID: project.OrganizationID, ProjectID: project.ID, Name: "reviewer"}
			def := projectAgentDefinition(project, arg.Name)
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().InsertProjectAgentDefinition(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(project, policy.ActionUpdate).Returns(def)
		},
		"GetProjectAgentDefinitionByName": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			arg := forkdb.GetProjectAgentDefinitionByNameParams{ProjectID: project.ID, Name: "reviewer"}
			def := projectAgentDefinition(project, arg.Name)
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().GetProjectAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(project, policy.ActionRead).Returns(def)
		},
		"ListProjectAgentDefinitionMetadata": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			rows := []forkdb.ListProjectAgentDefinitionMetadataRow{
				testutil.Fake(t, faker, forkdb.ListProjectAgentDefinitionMetadataRow{ProjectID: uuid.NullUUID{UUID: project.ID, Valid: true}}),
			}
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().ListProjectAgentDefinitionMetadata(gomock.Any(), project.ID).Return(rows, nil).AnyTimes()
			check.Args(project.ID).Asserts(project, policy.ActionRead).Returns(rows)
		},
		"UpdateProjectAgentDefinitionByName": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			arg := forkdb.UpdateProjectAgentDefinitionByNameParams{ProjectID: project.ID, Name: "reviewer"}
			def := projectAgentDefinition(project, arg.Name)
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().UpdateProjectAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(project, policy.ActionUpdate).Returns(def)
		},
		"DeleteProjectAgentDefinitionByName": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			project := testutil.Fake(t, faker, database.ChatProject{})
			arg := forkdb.DeleteProjectAgentDefinitionByNameParams{ProjectID: project.ID, Name: "reviewer"}
			def := projectAgentDefinition(project, arg.Name)
			db.EXPECT().GetChatProjectByID(gomock.Any(), project.ID).Return(project, nil).AnyTimes()
			fdb.EXPECT().DeleteProjectAgentDefinitionByName(gomock.Any(), arg).Return(def, nil).AnyTimes()
			check.Args(arg).Asserts(project, policy.ActionUpdate).Returns(def)
		},
		// Workflow journal queries authorize through the chat that owns the
		// run, fetched by ID on the fork store.
		"GetWorkflowRun": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			check.Args(run.ID).Asserts(chat, policy.ActionRead).Returns(run)
		},
		"GetWorkflowStepsByRun": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			steps := []forkdb.WorkflowStep{testutil.Fake(t, faker, forkdb.WorkflowStep{RunID: run.ID})}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowStepsByRun(gomock.Any(), run.ID).Return(steps, nil).AnyTimes()
			check.Args(run.ID).Asserts(chat, policy.ActionRead).Returns(steps)
		},
		"GetWorkflowStep": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			step := testutil.Fake(t, faker, forkdb.WorkflowStep{RunID: run.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowStep(gomock.Any(), step.ID).Return(step, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			check.Args(step.ID).Asserts(chat, policy.ActionRead).Returns(step)
		},
		"InsertWorkflowRun": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.InsertWorkflowRunParams{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertWorkflowRun(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"InsertWorkflowStep": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			arg := testutil.Fake(t, faker, forkdb.InsertWorkflowStepParams{RunID: run.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			fdb.EXPECT().InsertWorkflowStep(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"MarkOpenWorkflowRunsInterrupted": func(t *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			fdb.EXPECT().MarkOpenWorkflowRunsInterrupted(gomock.Any(), "the coderd process restarted before the run ended").Return(int64(2), nil).AnyTimes()
			check.Args("the coderd process restarted before the run ended").Asserts(rbac.ResourceSystem, policy.ActionUpdate).Returns(int64(2))
		},
		"UpdateWorkflowRunStatus": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			arg := testutil.Fake(t, faker, forkdb.UpdateWorkflowRunStatusParams{ID: run.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			fdb.EXPECT().UpdateWorkflowRunStatus(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"UpdateWorkflowStep": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			step := testutil.Fake(t, faker, forkdb.WorkflowStep{RunID: run.ID})
			arg := testutil.Fake(t, faker, forkdb.UpdateWorkflowStepParams{ID: step.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowStep(gomock.Any(), step.ID).Return(step, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			fdb.EXPECT().UpdateWorkflowStep(gomock.Any(), arg).Return(nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns()
		},
		"GetWorkflowRunsByChat": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			arg := testutil.Fake(t, faker, forkdb.GetWorkflowRunsByChatParams{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRunsByChat(gomock.Any(), arg).Return([]forkdb.WorkflowRun{run}, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionRead).Returns([]forkdb.WorkflowRun{run})
		},
		"GetWorkflowRunStepCountsByChat": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			counts := []forkdb.GetWorkflowRunStepCountsByChatRow{{RunID: run.ID, Status: "reported", StepCount: 1}}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRunStepCountsByChat(gomock.Any(), chat.ID).Return(counts, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(counts)
		},
		"FailOpenWorkflowStepsOfRun": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			run := testutil.Fake(t, faker, forkdb.WorkflowRun{ChatID: chat.ID})
			arg := forkdb.FailOpenWorkflowStepsOfRunParams{RunID: run.ID, ErrText: "stopped by user"}
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetWorkflowRun(gomock.Any(), run.ID).Return(run, nil).AnyTimes()
			fdb.EXPECT().FailOpenWorkflowStepsOfRun(gomock.Any(), arg).Return(int64(2), nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(int64(2))
		},
	}

	// Every fork query needs a case, like MethodTestSuite's accounting of
	// upstream's queries.
	storeType := reflect.TypeOf((*forkdb.Store)(nil)).Elem()
	for i := range storeType.NumMethod() {
		name := storeType.Method(i).Name
		require.Contains(t, cases, name, "fork query %q has no authorization test", name)
	}
	for name := range cases {
		_, ok := storeType.MethodByName(name)
		require.True(t, ok, "test case %q is not a fork query", name)
	}

	for name, testCaseF := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runForkMethodCase(t, name, testCaseF)
		})
	}
}

func runForkMethodCase(t *testing.T, methodName string, testCaseF forkMethodCase) {
	ctrl := gomock.NewController(t)
	fdb := forkdbmock.NewMockStore(ctrl)
	mDB := dbmock.NewMockStore(ctrl)
	mDB.EXPECT().Wrappers().Return([]string{}).AnyTimes()
	var testCase expects
	// Use a constant seed to prevent flakes from random data generation.
	testCaseF(t, mDB, fdb, gofakeit.New(0), &testCase)
	require.NotNil(t, testCase.assertions, "rbac assertions not set, use the 'check' parameter")

	fakeAuthorizer := &coderdtest.FakeAuthorizer{}
	rec := &coderdtest.RecordingAuthorizer{Wrapped: fakeAuthorizer}
	az := dbauthz.New(mDB, rec, slog.Make(), coderdtest.AccessControlStorePointer())
	wrapper, ok := az.(forkdb.Wrapper)
	require.True(t, ok, "dbauthz store does not wrap fork queries")
	fq := wrapper.ForkWrap(fdb)

	methodF := reflect.ValueOf(fq).MethodByName(methodName)
	callMethod := func(ctx context.Context) ([]reflect.Value, error) {
		resp := methodF.Call(append([]reflect.Value{reflect.ValueOf(ctx)}, testCase.inputs...))
		return splitResp(t, resp)
	}

	actor := rbac.Subject{
		ID:     testActorID.String(),
		Roles:  rbac.RoleIdentifiers{rbac.RoleOwner()},
		Groups: []string{},
		Scope:  rbac.ScopeAll,
	}
	ctx := dbauthz.As(context.Background(), actor)

	// The suite runs the NotAuthorized, Canceled and AsRemoveActor subtests
	// on t.
	s := &MethodTestSuite{}
	s.SetT(t)
	s.NotAuthorizedErrorTest(ctx, fakeAuthorizer, testCase, callMethod)
	s.NoActorErrorTest(callMethod)

	t.Run("Success", func(t *testing.T) {
		rec.Reset()
		fakeAuthorizer.AlwaysReturn(nil)

		outputs, err := callMethod(ctx)
		require.NoError(t, err, "method %q returned an error", methodName)
		if testCase.outputs != nil {
			require.Len(t, outputs, len(testCase.outputs), "method %q returned unexpected number of outputs", methodName)
			for i := range outputs {
				diff := cmp.Diff(testCase.outputs[i].Interface(), outputs[i].Interface(), cmpopts.EquateEmpty())
				require.Empty(t, diff, "method %q returned unexpected output %d (-want +got)", methodName, i)
			}
		}

		var pairs []coderdtest.ActionObjectPair
		for _, assrt := range testCase.assertions {
			for _, action := range assrt.Actions {
				pairs = append(pairs, coderdtest.ActionObjectPair{Action: action, Object: assrt.Object})
			}
		}
		rec.AssertActor(t, actor, pairs...)
		require.NoError(t, rec.AllAsserted(), "all rbac calls must be asserted")
	})
}

func userAgentDefinition(organizationID, userID uuid.UUID, name string) forkdb.AgentDefinition {
	def := organizationAgentDefinition(organizationID, name)
	def.UserID = uuid.NullUUID{UUID: userID, Valid: true}
	def.GroupAcl = database.ChatACL{}
	def.UserAcl = database.ChatACL{}
	return def
}

func organizationAgentDefinition(organizationID uuid.UUID, name string) forkdb.AgentDefinition {
	return forkdb.AgentDefinition{
		ID:             uuid.New(),
		OrganizationID: organizationID,
		Name:           name,
		Content:        "---\nname: " + name + "\n---\nReview the change.",
		Enabled:        true,
		GroupAcl:       database.ChatACL{uuid.NewString(): {Permissions: []policy.Action{policy.ActionRead}}},
		UserAcl:        database.ChatACL{uuid.NewString(): {Permissions: []policy.Action{policy.ActionRead, policy.ActionUpdate}}},
	}
}

func projectAgentDefinition(project database.ChatProject, name string) forkdb.AgentDefinition {
	return forkdb.AgentDefinition{
		ID:             uuid.New(),
		OrganizationID: project.OrganizationID,
		ProjectID:      uuid.NullUUID{UUID: project.ID, Valid: true},
		Name:           name,
		Content:        "---\nname: " + name + "\n---\nReview the change.",
		Enabled:        true,
	}
}

func organizationAgentDefinitionObject(def forkdb.AgentDefinition) rbac.Object {
	return rbac.ResourceOrganizationSkill.WithID(def.ID).InOrg(def.OrganizationID).
		WithGroupACL(def.GroupAcl.RBACACL()).
		WithACLUserList(def.UserAcl.RBACACL())
}

// TestUserAgentDefinitionOrganizationMembership runs the user-tier queries
// against the real authorizer. Their RBAC object has no organization, so
// only the organization check of dbauthz keeps out users who are not members.
func TestUserAgentDefinitionOrganizationMembership(t *testing.T) {
	t.Parallel()

	setupCtx := testutil.Context(t, testutil.WaitLong)
	store, _ := dbtestutil.NewDB(t)
	db := dbauthz.New(store, rbac.NewAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, &slogtest.Options{
		IgnoreErrors: true,
	}), coderdtest.AccessControlStorePointer())
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	org := dbgen.Organization(t, store, database.Organization{})
	member := dbgen.User(t, store, database.User{})
	outsider := dbgen.User(t, store, database.User{})
	subject := func(userID uuid.UUID, roles ...rbac.RoleIdentifier) rbac.Subject {
		return rbac.Subject{
			ID:     userID.String(),
			Roles:  must(rolestore.Expand(setupCtx, store, roles)),
			Groups: []string{},
			Scope:  rbac.ExpandableScope(rbac.ScopeAll),
		}
	}
	memberSubject := subject(member.ID, rbac.RoleMember(), rbac.ScopedRoleOrgMember(org.ID))

	// The removed member created this row while they were a member.
	_, err = fdb.InsertUserAgentDefinition(dbauthz.As(setupCtx, memberSubject), forkdb.InsertUserAgentDefinitionParams{
		OrganizationID: org.ID,
		UserID:         member.ID,
		Name:           "removed-member",
		Content:        "Review the change.",
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		subject rbac.Subject
		allowed bool
	}{
		{name: "member", subject: memberSubject, allowed: true},
		{name: "non-member", subject: subject(outsider.ID, rbac.RoleMember()), allowed: false},
		{name: "removed-member", subject: subject(member.ID, rbac.RoleMember()), allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := dbauthz.As(testutil.Context(t, testutil.WaitLong), tc.subject)
			userID := uuid.MustParse(tc.subject.ID)
			ops := []struct {
				name string
				run  func() error
			}{
				{"create", func() error {
					_, err := fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{OrganizationID: org.ID, UserID: userID, Name: tc.name, Content: "Review the change."})
					return err
				}},
				{"read", func() error {
					_, err := fdb.GetUserAgentDefinitionByName(ctx, forkdb.GetUserAgentDefinitionByNameParams{OrganizationID: org.ID, UserID: userID, Name: tc.name})
					return err
				}},
				{"list", func() error {
					rows, err := fdb.ListUserAgentDefinitionMetadata(ctx, forkdb.ListUserAgentDefinitionMetadataParams{OrganizationID: org.ID, UserID: userID})
					if err == nil && !slices.ContainsFunc(rows, func(r forkdb.ListUserAgentDefinitionMetadataRow) bool { return r.Name == tc.name }) {
						return xerrors.Errorf("list misses %q", tc.name)
					}
					return err
				}},
				{"update", func() error {
					_, err := fdb.UpdateUserAgentDefinitionByName(ctx, forkdb.UpdateUserAgentDefinitionByNameParams{OrganizationID: org.ID, UserID: userID, Name: tc.name, Content: sql.NullString{String: "Review it again.", Valid: true}})
					return err
				}},
				{"delete", func() error {
					_, err := fdb.DeleteUserAgentDefinitionByName(ctx, forkdb.DeleteUserAgentDefinitionByNameParams{OrganizationID: org.ID, UserID: userID, Name: tc.name})
					return err
				}},
			}
			for _, op := range ops {
				err := op.run()
				if tc.allowed {
					require.NoError(t, err, op.name)
				} else {
					require.True(t, dbauthz.IsNotAuthorizedError(err), "%s: expected authorization failure, got: %v", op.name, err)
				}
			}
		})
	}
}

// TestOrganizationAgentDefinitionOrganizationMembership runs the
// organization-tier reads against the real authorizer: RBAC applies ACL
// grants only to organization members, so a direct grant stops working once
// its user leaves the organization.
func TestOrganizationAgentDefinitionOrganizationMembership(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	store, _ := dbtestutil.NewDB(t)
	db := dbauthz.New(store, rbac.NewAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, &slogtest.Options{
		IgnoreErrors: true,
	}), coderdtest.AccessControlStorePointer())
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	rawFDB, err := forkdb.From(store)
	require.NoError(t, err)

	org := dbgen.Organization(t, store, database.Organization{})
	grantee := dbgen.User(t, store, database.User{})
	_, err = rawFDB.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{
		OrganizationID: org.ID,
		Name:           "shared",
		Content:        "Review the change.",
		GroupAcl:       database.ChatACL{},
		UserAcl:        database.ChatACL{grantee.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
	})
	require.NoError(t, err)
	subject := func(roles ...rbac.RoleIdentifier) rbac.Subject {
		return rbac.Subject{
			ID:     grantee.ID.String(),
			Roles:  must(rolestore.Expand(ctx, store, roles)),
			Groups: []string{},
			Scope:  rbac.ExpandableScope(rbac.ScopeAll),
		}
	}

	for _, tc := range []struct {
		name    string
		subject rbac.Subject
		allowed bool
	}{
		{name: "member", subject: subject(rbac.RoleMember(), rbac.ScopedRoleOrgMember(org.ID)), allowed: true},
		{name: "removed-member", subject: subject(rbac.RoleMember()), allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := dbauthz.As(testutil.Context(t, testutil.WaitLong), tc.subject)
			_, getErr := fdb.GetOrganizationAgentDefinitionByName(ctx, forkdb.GetOrganizationAgentDefinitionByNameParams{OrganizationID: org.ID, Name: "shared"})
			rows, listErr := fdb.ListOrganizationAgentDefinitionMetadata(ctx, org.ID)
			if tc.allowed {
				require.NoError(t, getErr)
				require.NoError(t, listErr)
				require.Len(t, rows, 1)
				return
			}
			require.True(t, dbauthz.IsNotAuthorizedError(getErr), "get: %v", getErr)
			require.NoError(t, listErr)
			require.Empty(t, rows)
		})
	}
}

func pluginPackObject(pack forkdb.PluginPack) rbac.Object {
	return rbac.ResourceOrganizationSkill.WithID(pack.ID).InOrg(pack.OrganizationID)
}
