package coderd_test

// This file is fork code: it covers the APNs device routes and the APNs
// push dispatcher of the fork-apns-push experiment.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"

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
	forkAPNsTestOtherToken = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
)

// forkTestAPNs is an HTTP/2 fake APNs. Each push gets the answer queued for
// its device token, else 200.
type forkTestAPNs struct {
	srv *httptest.Server
	// block, when set, holds every push until the request ends.
	block atomic.Bool

	mu      sync.Mutex
	pushes  []forkTestAPNsPush
	answers map[string][]forkTestAPNsAnswer
}

type forkTestAPNsPush struct {
	Environment string
	Token       string
	Title       string
	Body        string
	Sound       string
}

type forkTestAPNsAnswer struct {
	Status int
	Reason string
}

func newForkTestAPNs(t *testing.T) *forkTestAPNs {
	f := &forkTestAPNs{answers: map[string][]forkTestAPNsAnswer{}}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if f.block.Load() {
			<-r.Context().Done()
			return
		}
		env, token, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/3/device/")
		var payload struct {
			APS struct {
				Alert struct {
					Title string `json:"title"`
					Body  string `json:"body"`
				} `json:"alert"`
				Sound string `json:"sound"`
			} `json:"aps"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		f.mu.Lock()
		f.pushes = append(f.pushes, forkTestAPNsPush{Environment: env, Token: token, Title: payload.APS.Alert.Title, Body: payload.APS.Alert.Body, Sound: payload.APS.Sound})
		answer := forkTestAPNsAnswer{Status: http.StatusOK}
		if queued := f.answers[token]; len(queued) > 0 {
			answer, f.answers[token] = queued[0], queued[1:]
		}
		f.mu.Unlock()
		rw.WriteHeader(answer.Status)
		if answer.Reason != "" {
			_ = json.NewEncoder(rw).Encode(map[string]string{"reason": answer.Reason})
		}
	}))
	f.srv.EnableHTTP2 = true
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *forkTestAPNs) answer(token string, answers ...forkTestAPNsAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[token] = append(f.answers[token], answers...)
}

func (f *forkTestAPNs) received() []forkTestAPNsPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]forkTestAPNsPush(nil), f.pushes...)
}

func (f *forkTestAPNs) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = nil
}

// forkTestWebpush stands in for upstream's web push dispatcher.
type forkTestWebpush struct {
	publicKey string

	mu         sync.Mutex
	dispatched []codersdk.WebpushMessage
}

func (w *forkTestWebpush) Dispatch(_ context.Context, _ uuid.UUID, msg codersdk.WebpushMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dispatched = append(w.dispatched, msg)
	return nil
}

func (*forkTestWebpush) Test(context.Context, codersdk.WebpushSubscription) error { return nil }

func (w *forkTestWebpush) PublicKey() string { return w.publicKey }

func (w *forkTestWebpush) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.dispatched)
}

// forkTestLogSink keeps every log line as text.
type forkTestLogSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *forkTestLogSink) LogEntry(_ context.Context, e slog.SinkEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, fmt.Sprintf("%s %v", e.Message, e.Fields))
}

func (*forkTestLogSink) Sync() {}

func (s *forkTestLogSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

type forkAPNsTestEnv struct {
	client  *codersdk.Client
	api     *coderd.API
	db      database.Store
	fdb     forkdb.Store
	user    codersdk.CreateFirstUserResponse
	apns    *forkTestAPNs
	webpush *forkTestWebpush
	logs    *forkTestLogSink
}

type forkAPNsTestOptions struct {
	experimentOff bool
	noKey         bool
	webpushKey    string
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
	}
	logs := &forkTestLogSink{}
	logger := slog.Make(logs).Leveled(slog.LevelDebug)
	wp := &forkTestWebpush{publicKey: opts.webpushKey}
	client, _, api := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues:  values,
		Database:          db,
		Pubsub:            ps,
		WebpushDispatcher: wp,
		Logger:            &logger,
	})
	user := coderdtest.CreateFirstUser(t, client)
	env := forkAPNsTestEnv{client: client, api: api, db: db, fdb: fdb, user: user, webpush: wp, logs: logs}
	if coderd.ForkPushDispatcher(api) != nil {
		env.apns = newForkTestAPNs(t)
		coderd.PointForkAPNs(api, env.apns.srv.URL, env.apns.srv.Client(), testutil.WaitShort)
	}
	return env
}

func (e forkAPNsTestEnv) chat(t *testing.T, status database.ChatStatus) database.Chat {
	t.Helper()
	return dbgen.Chat(t, e.db, database.Chat{
		OrganizationID: e.user.OrganizationID,
		OwnerID:        e.user.UserID,
		Status:         status,
		LastModelConfigID: dbgen.ChatModelConfig(t, e.db, database.ChatModelConfig{
			OrganizationID: e.user.OrganizationID,
		}).ID,
	})
}

// dispatch sends a push the way chatd's dispatchPush does and waits for
// its APNs sends.
func (e forkAPNsTestEnv) dispatch(t *testing.T, chat database.Chat, body string) {
	t.Helper()
	err := coderd.ForkPushDispatcher(e.api).Dispatch(testutil.Context(t, testutil.WaitShort), chat.OwnerID, codersdk.WebpushMessage{
		Title: chat.Title,
		Body:  body,
		Icon:  "/favicon.ico",
		Data:  map[string]string{"url": "/agents/" + chat.ID.String()},
	})
	require.NoError(t, err)
	coderd.WaitForkPush(e.api)
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

	require.Nil(t, coderd.ForkPushDispatcher(env.api))
	err := env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken, Environment: codersdk.ChatAPNsEnvironmentSandbox})
	requireForkSDKStatus(t, err, http.StatusForbidden)
}

func TestForkChatAPNsRoutesWithoutKey(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{noKey: true})
	ctx := testutil.Context(t, testutil.WaitShort)

	require.Nil(t, coderd.ForkPushDispatcher(env.api))
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

func TestForkChatAPNsTestPush(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)

	requireForkSDKStatus(t, env.client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}), http.StatusNotFound)

	require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken: forkAPNsTestToken, Environment: codersdk.ChatAPNsEnvironmentSandbox, FinishedTurns: true, Sound: true,
	}))
	require.NoError(t, env.client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken}))
	require.Equal(t, []forkTestAPNsPush{{
		Environment: "sandbox", Token: forkAPNsTestToken, Title: "It's working!", Body: "You've subscribed to push notifications.", Sound: "chime.caf",
	}}, env.apns.received())

	// A token that APNs rejects in both environments is reported and
	// deleted.
	env.apns.answer(forkAPNsTestToken,
		forkTestAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"},
		forkTestAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"},
	)
	err := env.client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: forkAPNsTestToken})
	requireForkSDKStatus(t, err, http.StatusBadGateway)
	require.ErrorContains(t, err, "400 BadDeviceToken")
	require.Empty(t, env.devices(t, env.user.UserID))
}

func TestForkChatAPNsDispatchByKind(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{webpushKey: "vapid"})
	ctx := testutil.Context(t, testutil.WaitShort)

	// One device for each toggle, named by the token.
	tokens := map[string]codersdk.RegisterChatAPNsDeviceRequest{
		"0a": {FinishedTurns: true},
		"0b": {Questions: true},
		"0c": {AgentErrors: true},
	}
	for token, req := range tokens {
		req.DeviceToken = token
		req.Environment = codersdk.ChatAPNsEnvironmentSandbox
		require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, req))
	}

	for _, tc := range []struct {
		status database.ChatStatus
		want   []string
	}{
		// A turn that ends with a question looks finished, so a device
		// that takes questions gets it too.
		{status: database.ChatStatusWaiting, want: []string{"0a", "0b"}},
		{status: database.ChatStatusPaused, want: []string{"0a", "0b"}},
		{status: database.ChatStatusRunning, want: []string{"0b"}},
		{status: database.ChatStatusError, want: []string{"0c"}},
	} {
		env.apns.reset()
		chat := env.chat(t, tc.status)
		env.dispatch(t, chat, "Done")
		var got []string
		for _, push := range env.apns.received() {
			require.Equal(t, chat.Title, push.Title)
			require.Equal(t, "Done", push.Body)
			got = append(got, push.Token)
		}
		require.ElementsMatch(t, tc.want, got, "status %s", tc.status)
	}
	require.Equal(t, 4, env.webpush.count(), "web push gets every push")
	require.Equal(t, "vapid", coderd.ForkPushDispatcher(env.api).PublicKey())
}

func TestForkChatAPNsDispatchWithoutWebPush(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)

	// chatd sends only when the key is not empty, so the dispatcher
	// reports one when only APNs is set up.
	require.NotEmpty(t, coderd.ForkPushDispatcher(env.api).PublicKey())
	require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken: forkAPNsTestToken, Environment: codersdk.ChatAPNsEnvironmentSandbox, FinishedTurns: true,
	}))
	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
	require.Len(t, env.apns.received(), 1)
	require.Zero(t, env.webpush.count(), "web push is off")
}

func TestForkChatAPNsDispatchKeepsDevicesInLine(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{webpushKey: "vapid"})
	ctx := testutil.Context(t, testutil.WaitShort)
	for _, token := range []string{forkAPNsTestToken, forkAPNsTestOtherToken} {
		require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
			DeviceToken: token, Environment: codersdk.ChatAPNsEnvironmentSandbox, FinishedTurns: true,
		}))
	}

	// The first token is dead. The second was issued by production.
	env.apns.answer(forkAPNsTestToken, forkTestAPNsAnswer{Status: http.StatusGone, Reason: "Unregistered"})
	env.apns.answer(forkAPNsTestOtherToken, forkTestAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"})
	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")

	devices := env.devices(t, env.user.UserID)
	require.Len(t, devices, 1)
	require.Equal(t, forkAPNsTestOtherToken, devices[0].DeviceToken)
	require.Equal(t, "production", devices[0].Environment)

	// A busy or failing APNs drops the push and keeps the device, and web
	// push is sent all the same.
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		env.apns.answer(forkAPNsTestOtherToken, forkTestAPNsAnswer{Status: status, Reason: "Busy"})
		env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
		require.Len(t, env.devices(t, env.user.UserID), 1)
	}
	require.Equal(t, 3, env.webpush.count())

	require.NotContains(t, env.logs.text(), forkAPNsTestToken)
	require.NotContains(t, env.logs.text(), forkAPNsTestOtherToken)
	require.Contains(t, env.logs.text(), "APNs rejected the push")
}

func TestForkChatAPNsDispatchTimesOut(t *testing.T) {
	t.Parallel()
	env := newForkAPNsTestEnv(t, forkAPNsTestOptions{webpushKey: "vapid"})
	ctx := testutil.Context(t, testutil.WaitShort)
	require.NoError(t, env.client.RegisterChatAPNsDevice(ctx, codersdk.RegisterChatAPNsDeviceRequest{
		DeviceToken: forkAPNsTestToken, Environment: codersdk.ChatAPNsEnvironmentSandbox, FinishedTurns: true,
	}))
	coderd.PointForkAPNs(env.api, env.apns.srv.URL, env.apns.srv.Client(), 100*time.Millisecond)
	env.apns.block.Store(true)

	start := time.Now()
	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
	require.Less(t, time.Since(start), testutil.WaitShort)
	require.Len(t, env.devices(t, env.user.UserID), 1)
	require.Equal(t, 1, env.webpush.count())
}
