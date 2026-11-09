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
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbmock"
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
		"GetChatSubagentByChatID": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			sub := testutil.Fake(t, faker, forkdb.ChatSubagent{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().GetChatSubagentByChatID(gomock.Any(), chat.ID).Return(sub, nil).AnyTimes()
			check.Args(chat.ID).Asserts(chat, policy.ActionRead).Returns(sub)
		},
		"InsertChatSubagent": func(t *testing.T, db *dbmock.MockStore, fdb *forkdbmock.MockStore, faker *gofakeit.Faker, check *expects) {
			chat := testutil.Fake(t, faker, database.Chat{})
			arg := testutil.Fake(t, faker, forkdb.InsertChatSubagentParams{ChatID: chat.ID})
			sub := testutil.Fake(t, faker, forkdb.ChatSubagent{ChatID: chat.ID})
			db.EXPECT().GetChatByID(gomock.Any(), chat.ID).Return(chat, nil).AnyTimes()
			fdb.EXPECT().InsertChatSubagent(gomock.Any(), arg).Return(sub, nil).AnyTimes()
			check.Args(arg).Asserts(chat, policy.ActionUpdate).Returns(sub)
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
