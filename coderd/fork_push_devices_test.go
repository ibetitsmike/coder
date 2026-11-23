package coderd_test

// This file is fork code: it covers the APNs device routes of the
// fork-apns-push experiment. The push dispatcher itself is covered by
// internal tests.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

const (
	forkAPNsTestToken      = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	forkAPNsTestOtherToken = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0" //nolint:gosec // A fake APNs device token.
)

type forkAPNsTestEnv struct {
	client *codersdk.Client
	api    *coderd.API
	db     database.Store
	fdb    forkdb.Store
	user   codersdk.CreateFirstUserResponse
}

type forkAPNsTestOptions struct {
	experimentOff bool
	noKey         bool
}

func newForkAPNsTestEnv(t *testing.T, opts forkAPNsTestOptions) forkAPNsTestEnv {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	values := coderdtest.DeploymentValues(t)
	if !opts.experimentOff {
		values.Experiments = serpent.StringArray{string(codersdk.ExperimentForkAPNsPush)}
	}
	if !opts.noKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		der, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "AuthKey.p8")
		require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
		require.NoError(t, values.AI.Chat.Fork.APNsKeyFile.Set(path))
		require.NoError(t, values.AI.Chat.Fork.APNsKeyID.Set("TESTKEY123"))
		require.NoError(t, values.AI.Chat.Fork.APNsTeamID.Set("TEAM123456"))
		require.NoError(t, values.AI.Chat.Fork.APNsTopic.Set("com.coder.ios"))
	}
	client, _, api := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues: values,
		Database:         db,
		Pubsub:           ps,
	})
	user := coderdtest.CreateFirstUser(t, client)
	return forkAPNsTestEnv{client: client, api: api, db: db, fdb: fdb, user: user}
}

func (e forkAPNsTestEnv) devices(t *testing.T, userID uuid.UUID) []forkdb.ChatApnsDevice {
	t.Helper()
	devices, err := e.fdb.GetChatAPNsDevicesByUserID(context.Background(), userID)
	require.NoError(t, err)
	return devices
}

func requireForkSDKStatus(t *testing.T, err error, status int) {
	t.Helper()
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, status, sdkErr.StatusCode())
}

func TestForkChatAPNsRoutesNeedTheExperiment(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{experimentOff: true})
	ctx := testutil.Context(t, testutil.WaitShort)

	err := env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken, Environment: codersdk.ChatAPNsEnvironmentSandbox})
	requireForkSDKStatus(t, err, http.StatusForbidden)
}

func TestForkChatAPNsRoutesWithoutKey(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{noKey: true})
	ctx := testutil.Context(t, testutil.WaitShort)

	err := env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken, Environment: codersdk.ChatAPNsEnvironmentSandbox})
	requireForkSDKStatus(t, err, http.StatusNotFound)
	require.ErrorContains(t, err, "APNs is not configured.")
	requireForkSDKStatus(t, env.client.DeleteChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}), http.StatusNotFound)
	requireForkSDKStatus(t, env.client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}), http.StatusNotFound)
}

func TestForkChatAPNsDevices(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)

	for _, req := range []codersdk.RegisterChatAPNsDeviceRequest{
		{DeviceToken: "", Environment: codersdk.ChatAPNsEnvironmentSandbox},
		{DeviceToken: "not-hex", Environment: codersdk.ChatAPNsEnvironmentSandbox},
		{DeviceToken: strings.Repeat("ab", 101), Environment: codersdk.ChatAPNsEnvironmentSandbox},
		{DeviceToken: forkAPNsTestToken, Environment: "staging"},
	} {
		requireForkSDKStatus(t, env.client.RegisterChatAPNsDevice(ctx, req), http.StatusBadRequest)
	}

	register := codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken:   forkAPNsTestToken,
		Environment:   codersdk.ChatAPNsEnvironmentSandbox,
		FinishedTurns: true,
		Questions:     true,
	}
	require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, register))
	register.FinishedTurns = false
	register.AgentErrors = true
	register.Sound = true
	require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, register))
	devices := env.devices(t, env.user.UserID)
	require.Len(t, devices, 1)
	require.Equal(t, forkdb.ChatApnsDevice{
		DeviceToken:   forkAPNsTestToken,
		UserID:        env.user.UserID,
		Environment:   "sandbox",
		FinishedTurns: false,
		Questions:     true,
		AgentErrors:   true,
		Sound:         true,
		CreatedAt:     devices[0].CreatedAt,
		UpdatedAt:     devices[0].UpdatedAt,
	}, devices[0])

	// A member cannot remove another user's device.
	member, memberUser := coderdtest.CreateAnotherUser(t, env.client, env.user.OrganizationID)
	require.NoError(t, member.DeleteChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}))
	require.Len(t, env.devices(t, env.user.UserID), 1)
	requireForkSDKStatus(t, member.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}), http.StatusNotFound)

	// The phone signs in as the member: the token moves.
	register.Environment = codersdk.ChatAPNsEnvironmentProduction
	require.NoError(t, member.RegisterChatAPNsDevice(ctx, register))
	require.Empty(t, env.devices(t, env.user.UserID))
	moved := env.devices(t, memberUser.ID)
	require.Len(t, moved, 1)
	require.Equal(t, "production", moved[0].Environment)

	require.NoError(t, member.DeleteChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}))
	require.Empty(t, env.devices(t, memberUser.ID))
	require.NoError(t, member.DeleteChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}), "deleting twice succeeds")
}

func TestForkChatAPNsDeviceTokenIsCanonical(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)

	// A device registered with an upper case token is stored lower case.
	require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken:   strings.ToUpper(forkAPNsTestToken),
		Environment:   codersdk.ChatAPNsEnvironmentSandbox,
		FinishedTurns: true,
	}))
	devices := env.devices(t, env.user.UserID)
	require.Len(t, devices, 1)
	require.Equal(t, forkAPNsTestToken, devices[0].DeviceToken)

	// The lower case form is the same device: it moves to a member who
	// registers it.
	member, memberUser := coderdtest.CreateAnotherUser(t, env.client, env.user.OrganizationID)
	require.NoError(t, member.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken:   forkAPNsTestToken,
		Environment:   codersdk.ChatAPNsEnvironmentSandbox,
		FinishedTurns: true,
	}))
	require.Empty(t, env.devices(t, env.user.UserID), "the token moved to the member")
	memberDevices := env.devices(t, memberUser.ID)
	require.Len(t, memberDevices, 1)

	// Deleting by the upper case form removes the lower case row.
	require.NoError(t, member.DeleteChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{
		DeviceToken: strings.ToUpper(forkAPNsTestToken),
	}))
	require.Empty(t, env.devices(t, memberUser.ID))
}

func TestForkChatAPNsTestPushNeedsNotificationPermission(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)

	// The scope is internal, so it cannot be requested through
	// CreateToken. The test writes the scoped key straight into the
	// database instead.
	_, scopedToken := dbgen.APIKey(t, env.db, database.APIKey{
		UserID: env.user.UserID,
		Scopes: []database.APIKeyScope{database.APIKeyScope(codersdk.APIKeyScopeWebpushSubscriptionAll)},
	})
	scoped := codersdk.New(env.client.URL)
	scoped.SetSessionToken(scopedToken)

	// The scoped token reaches the fork handlers: registration works.
	require.NoError(t, scoped.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken:   forkAPNsTestOtherToken,
		Environment:   codersdk.ChatAPNsEnvironmentSandbox,
		FinishedTurns: true,
	}))

	// The test push route checks notification_message:create before it
	// reads the device. An unregistered token also keeps the test from
	// ever sending a real push.
	err := scoped.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken})
	requireForkSDKStatus(t, err, http.StatusForbidden)
}
