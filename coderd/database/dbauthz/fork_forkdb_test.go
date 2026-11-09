package dbauthz_test

// This file is fork code: it tests the authorization of the fork queries in
// fork_forkdb.go like MethodTestSuite tests upstream's queries. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"context"
	"reflect"
	"testing"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/forkdb/forkdbmock"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
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
		"InsertChatSubagent": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.InsertChatSubagentParams{ChatID: chat.ID})
			sub := testutil.Fake(t, faker, forkdb.ChatSubagent{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatSubagent(gomock.Any(), arg).Return(sub, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(sub)
		},
		"UpsertChatTodos": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.UpsertChatTodosParams{ChatID: chat.ID})
			todos := testutil.Fake(t, faker, forkdb.ChatTodo{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().UpsertChatTodos(gomock.Any(), arg).Return(todos, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(todos)
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
		"DeleteExpiredPluginLinkCodes": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			fdb.EXPECT().DeleteExpiredPluginLinkCodes(gomock.Any()).Return(nil).AnyTimes()
			check.Args().Asserts(rbac.ResourceSystem, policy.ActionDelete).Returns()
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
		"SetPluginUserLinksActiveChatByUser": func(_ *testing.T, _ *dbmock.MockStore, fdb *forkdbmock.MockStore, _ *gofakeit.Faker, check *expects) {
			arg := forkdb.SetPluginUserLinksActiveChatByUserParams{PluginID: uuid.New(), UserID: uuid.New(), ActiveChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true}}
			fdb.EXPECT().SetPluginUserLinksActiveChatByUser(gomock.Any(), arg).Return(nil).AnyTimes()
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
