package coderd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/webpush"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// With the experiment off or no key, chatd gets upstream's dispatcher
// itself, so web push behaves exactly as upstream's.
func TestForkChatPushDispatcherUnwrapped(t *testing.T) {
	t.Parallel()

	inner := &webpush.NoopWebpusher{}
	for _, tc := range []struct {
		name       string
		experiment bool
		key        bool
	}{
		{name: "Off"},
		{name: "NoKey", experiment: true},
		{name: "KeyWithoutExperiment", key: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := &codersdk.DeploymentValues{}
			if tc.key {
				require.NoError(t, values.AI.Chat.Fork.APNsKeyFile.Set("/missing/AuthKey.p8"))
			}
			api := &API{Options: &Options{DeploymentValues: values}}
			if tc.experiment {
				api.Experiments = codersdk.Experiments{codersdk.ExperimentForkAPNsPush}
			}
			require.Same(t, inner, api.forkChatPushDispatcher(inner))
			require.Nil(t, api.forkPush)
			api.closeForkPushDispatcher()
		})
	}
}

func TestForkChatPushKind(t *testing.T) {
	t.Parallel()

	require.Equal(t, forkPushFinishedTurn, forkPushKindOf("waiting"))
	require.Equal(t, forkPushFinishedTurn, forkPushKindOf("paused"))
	require.Equal(t, forkPushAgentError, forkPushKindOf("error"))
	require.Equal(t, forkPushQuestion, forkPushKindOf("running"))
	require.Equal(t, forkPushQuestion, forkPushKindOf("requires_action"))
}

// A key that does not load stops the server from starting.
// A missing option or a key that does not load turns APNs off and leaves
// chatd with upstream's dispatcher, so a bad APNs config never stops the
// server.
func TestForkChatPushDispatcherBadConfig(t *testing.T) {
	t.Parallel()

	garbage := filepath.Join(t.TempDir(), "AuthKey.p8")
	require.NoError(t, os.WriteFile(garbage, []byte("SECRETGARBAGE"), 0o600))
	for _, tc := range []struct {
		name    string
		keyFile string
		keyID   string
		message string
		reason  string
	}{
		{name: "Unreadable", keyFile: filepath.Join(t.TempDir(), "missing.p8"), keyID: "TESTKEY123", message: "turned APNs push off: the APNs key does not load", reason: "read APNs key file"},
		{name: "NotPEM", keyFile: garbage, keyID: "TESTKEY123", message: "turned APNs push off: the APNs key does not load", reason: "APNs key file is not PEM"},
		{name: "MissingKeyID", keyFile: garbage, message: "turned APNs push off: an APNs option is not set", reason: "--chat-apns-key-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := &codersdk.DeploymentValues{}
			require.NoError(t, values.AI.Chat.Fork.APNsKeyFile.Set(tc.keyFile))
			require.NoError(t, values.AI.Chat.Fork.APNsKeyID.Set(tc.keyID))
			require.NoError(t, values.AI.Chat.Fork.APNsTeamID.Set("TEAM123456"))
			require.NoError(t, values.AI.Chat.Fork.APNsTopic.Set(codersdk.DefaultChatAPNsTopic))
			sink := testutil.NewFakeSink(t)
			api := &API{
				ctx:         context.Background(),
				Options:     &Options{DeploymentValues: values, Logger: sink.Logger()},
				Experiments: codersdk.Experiments{codersdk.ExperimentForkAPNsPush},
			}
			inner := &webpush.NoopWebpusher{}
			require.Same(t, inner, api.forkChatPushDispatcher(inner))
			require.Nil(t, api.forkChatPushDispatcher(nil))
			require.Nil(t, api.forkPush)

			errors := sink.Entries(func(e slog.SinkEntry) bool { return e.Level == slog.LevelError })
			require.Len(t, errors, 2)
			require.Equal(t, tc.message, errors[0].Message)
			fields := fmt.Sprint(errors[0].Fields)
			require.Contains(t, fields, tc.reason)
			require.NotContains(t, fields, "SECRETGARBAGE")
		})
	}
}

// forkPushTestWebpush stands in for upstream's web push dispatcher.
type forkPushTestWebpush struct {
	publicKey string

	mu    sync.Mutex
	count int
}

func (w *forkPushTestWebpush) Dispatch(context.Context, uuid.UUID, codersdk.WebpushMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.count++
	return nil
}

func (*forkPushTestWebpush) Test(context.Context, codersdk.WebpushSubscription) error { return nil }

func (w *forkPushTestWebpush) PublicKey() string { return w.publicKey }

func (w *forkPushTestWebpush) dispatched() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

type forkPushTestEnv struct {
	raw database.Store
	// db authorizes like the server's database.
	db      database.Store
	fdb     forkdb.Store
	apns    *fakeAPNs
	webpush *forkPushTestWebpush
	sink    *testutil.FakeSink
	d       *forkPushDispatcher
	user    database.User
	org     database.Organization
}

func newForkPushTestEnv(t *testing.T, webpushKey string) *forkPushTestEnv {
	t.Helper()
	raw, _ := dbtestutil.NewDB(t)
	db := dbauthzTestStore(t, raw)
	fdb, err := forkdb.From(raw)
	require.NoError(t, err)
	fake := newFakeAPNs(t)
	client, _ := newTestForkAPNsClient(t, fake, quartz.NewReal())
	sink := testutil.NewFakeSink(t)
	wp := &forkPushTestWebpush{publicKey: webpushKey}
	return &forkPushTestEnv{
		raw:     raw,
		db:      db,
		fdb:     fdb,
		apns:    fake,
		webpush: wp,
		sink:    sink,
		d:       newForkPushDispatcher(wp, client, db, sink.Logger(), "https://coder.example.com"),
		user:    dbgen.User(t, raw, database.User{}),
		org:     dbgen.Organization(t, raw, database.Organization{}),
	}
}

// register stores a device of the env's user, in sandbox unless set.
func (e *forkPushTestEnv) register(t *testing.T, params forkdb.UpsertChatAPNsDeviceParams) forkdb.ChatApnsDevice {
	t.Helper()
	params.UserID = e.user.ID
	if params.Environment == "" {
		params.Environment = string(codersdk.ChatAPNsEnvironmentSandbox)
	}
	if params.Now.IsZero() {
		params.Now = dbtime.Now()
	}
	require.NoError(t, e.fdb.UpsertChatAPNsDevice(context.Background(), params))
	device, err := e.fdb.GetChatAPNsDevice(context.Background(), forkdb.GetChatAPNsDeviceParams{UserID: e.user.ID, DeviceToken: params.DeviceToken})
	require.NoError(t, err)
	return device
}

func (e *forkPushTestEnv) devices(t *testing.T) []forkdb.ChatApnsDevice {
	t.Helper()
	devices, err := e.fdb.GetChatAPNsDevicesByUserID(context.Background(), e.user.ID)
	require.NoError(t, err)
	return devices
}

func (e *forkPushTestEnv) chat(t *testing.T, status database.ChatStatus) database.Chat {
	t.Helper()
	return dbgen.Chat(t, e.raw, database.Chat{
		OrganizationID: e.org.ID,
		OwnerID:        e.user.ID,
		Status:         status,
		LastModelConfigID: dbgen.ChatModelConfig(t, e.raw, database.ChatModelConfig{
			OrganizationID: e.org.ID,
		}).ID,
	})
}

// dispatch sends a push the way chatd's dispatchPush does and waits for its
// APNs sends.
func (e *forkPushTestEnv) dispatch(t *testing.T, chat database.Chat, body string) {
	t.Helper()
	err := e.d.Dispatch(testutil.Context(t, testutil.WaitShort), chat.OwnerID, codersdk.WebpushMessage{
		Title: chat.Title,
		Body:  body,
		Icon:  "/favicon.ico",
		Data:  map[string]string{"url": "/agents/" + chat.ID.String()},
	})
	require.NoError(t, err)
	e.d.wg.Wait()
}

// send sends a push to device the way a dispatch does.
func (e *forkPushTestEnv) send(t *testing.T, device forkdb.ChatApnsDevice) forkAPNsResponse {
	t.Helper()
	fdb, err := forkdb.From(e.db)
	require.NoError(t, err)
	res, err := e.d.sendToDevice(dbauthz.AsNotifier(testutil.Context(t, testutil.WaitShort)), fdb, device, forkAPNsPush{Title: "Fix the build", Body: "Done"})
	require.NoError(t, err)
	return res
}

type forkPushTestPush struct {
	Environment string
	Token       string
	Title       string
	Body        string
	Sound       string
}

// pushes returns what the fake APNs received, from push number from on.
func (e *forkPushTestEnv) pushes(from int) []forkPushTestPush {
	var pushes []forkPushTestPush
	for _, req := range e.apns.received()[from:] {
		env, token, _ := strings.Cut(strings.TrimPrefix(req.Path, "/"), "/3/device/")
		aps, _ := req.Payload["aps"].(map[string]any)
		alert, _ := aps["alert"].(map[string]any)
		title, _ := alert["title"].(string)
		body, _ := alert["body"].(string)
		sound, _ := aps["sound"].(string)
		pushes = append(pushes, forkPushTestPush{Environment: env, Token: token, Title: title, Body: body, Sound: sound})
	}
	return pushes
}

func TestForkChatAPNsDispatchByKind(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "vapid")

	// One device for each toggle, named by the token.
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: "0a", FinishedTurns: true})
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: "0b", Questions: true})
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: "0c", AgentErrors: true})

	for _, tc := range []struct {
		status database.ChatStatus
		want   []string
	}{
		// A turn that ends with a question looks finished, so a device that
		// takes questions gets it too.
		{status: database.ChatStatusWaiting, want: []string{"0a", "0b"}},
		{status: database.ChatStatusPaused, want: []string{"0a", "0b"}},
		{status: database.ChatStatusRunning, want: []string{"0b"}},
		{status: database.ChatStatusError, want: []string{"0c"}},
	} {
		from := len(env.apns.received())
		chat := env.chat(t, tc.status)
		env.dispatch(t, chat, "Done")
		var got []string
		for _, push := range env.pushes(from) {
			require.Equal(t, chat.Title, push.Title)
			require.Equal(t, "Done", push.Body)
			got = append(got, push.Token)
		}
		require.ElementsMatch(t, tc.want, got, "status %s", tc.status)
	}
	require.Equal(t, 4, env.webpush.dispatched(), "web push gets every push")
	require.Equal(t, "vapid", env.d.PublicKey())
}

func TestForkChatAPNsDispatchWithoutWebPush(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "")

	// chatd sends only when the key is not empty, so the dispatcher reports
	// one when only APNs is set up.
	require.NotEmpty(t, env.d.PublicKey())
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true, Sound: true})
	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
	pushes := env.pushes(0)
	require.Len(t, pushes, 1)
	require.Equal(t, "chime.caf", pushes[0].Sound)
	require.Zero(t, env.webpush.dispatched(), "web push is off")
}

func TestForkChatAPNsDispatchKeepsDevicesInLine(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "vapid")
	const otherToken = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0" //nolint:gosec // A fake APNs device token.
	now := dbtime.Now()
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true, Now: now})
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: otherToken, FinishedTurns: true, Now: now.Add(time.Millisecond)})

	// The first token is dead. The second was issued by production.
	env.apns.answerToken(testAPNsToken, fakeAPNsAnswer{Status: http.StatusGone, Reason: "Unregistered"})
	env.apns.answerToken(otherToken, fakeAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"})
	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
	var got []string
	for _, push := range env.pushes(0) {
		got = append(got, push.Environment+"/"+push.Token)
	}
	require.ElementsMatch(t, []string{"sandbox/" + testAPNsToken, "sandbox/" + otherToken, "production/" + otherToken}, got)

	devices := env.devices(t)
	require.Len(t, devices, 1)
	require.Equal(t, otherToken, devices[0].DeviceToken)
	require.Equal(t, "production", devices[0].Environment)

	// A busy or failing APNs drops the push and keeps the device, and web
	// push is sent all the same.
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		env.apns.answerToken(otherToken, fakeAPNsAnswer{Status: status, Reason: "Busy"})
		env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
		require.Len(t, env.devices(t), 1)
	}
	require.Equal(t, 3, env.webpush.dispatched())

	var logs []string
	for _, e := range env.sink.Entries() {
		logs = append(logs, fmt.Sprintf("%s %v", e.Message, e.Fields))
	}
	text := strings.Join(logs, "\n")
	require.NotContains(t, text, testAPNsToken)
	require.NotContains(t, text, otherToken)
	require.Contains(t, text, "deleting a device token that APNs rejected")
	require.Contains(t, text, "dropped a push that APNs rejected")
}

func TestForkChatAPNsDispatchTimesOut(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "vapid")
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true})

	env.apns.stall(testAPNsToken)
	env.d.timeout = testutil.IntervalFast

	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
	require.Len(t, env.devices(t), 1)
	require.Equal(t, 1, env.webpush.dispatched())
	require.Len(t, env.sink.Entries(func(e slog.SinkEntry) bool { return e.Message == "failed to send APNs push" }), 1)
}

// A device whose push stalls until the timeout does not keep the push from
// the user's other devices.
func TestForkChatAPNsDispatchStalledDevice(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "vapid")
	const otherToken = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0" //nolint:gosec // A fake APNs device token.
	now := dbtime.Now()
	// The stalled device comes first in the list.
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true, Now: now})
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: otherToken, FinishedTurns: true, Now: now.Add(time.Millisecond)})
	env.apns.stall(testAPNsToken)
	env.d.timeout = testutil.IntervalSlow

	env.dispatch(t, env.chat(t, database.ChatStatusWaiting), "Done")
	var got []string
	for _, push := range env.pushes(0) {
		got = append(got, push.Token)
	}
	require.ElementsMatch(t, []string{testAPNsToken, otherToken}, got)
	require.Len(t, env.sink.Entries(func(e slog.SinkEntry) bool { return e.Message == "failed to send APNs push" }), 1, "only the stalled device fails")
}

// A device that registers again while a push to its old registration is in
// flight keeps its row, unless APNs reports the token invalid since after
// that registration. So does a token that APNs reports invalid since before
// the device registered.
func TestForkChatAPNsRejectionKeepsANewerRegistration(t *testing.T) {
	t.Parallel()

	gone := fakeAPNsAnswer{Status: http.StatusGone, Reason: "Unregistered"}
	badToken := fakeAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"}
	for _, tc := range []struct {
		name    string
		answers []fakeAPNsAnswer
		// stale sends with the row as it was before the device registered
		// again.
		stale bool
		// invalidSince sets the 410 timestamp, relative to the registration.
		invalidSince time.Duration
		pushes       int
		wantKept     bool
		wantEnv      string
	}{
		{name: "GoneDeletes", pushes: 1, answers: []fakeAPNsAnswer{gone}},
		{name: "GoneAfterReregistering", pushes: 1, answers: []fakeAPNsAnswer{gone}, stale: true, wantKept: true, wantEnv: "sandbox"},
		{name: "GoneSinceAfterRegistering", pushes: 1, answers: []fakeAPNsAnswer{gone}, invalidSince: time.Second},
		{name: "GoneSinceBeforeRegistering", pushes: 1, answers: []fakeAPNsAnswer{gone}, invalidSince: -time.Second, wantKept: true, wantEnv: "sandbox"},
		// The device registers again at one minute.
		{name: "GoneSinceBeforeReregistering", pushes: 1, answers: []fakeAPNsAnswer{gone}, stale: true, invalidSince: 30 * time.Second, wantKept: true, wantEnv: "sandbox"},
		{name: "GoneSinceAfterReregistering", pushes: 1, answers: []fakeAPNsAnswer{gone}, stale: true, invalidSince: 2 * time.Minute},
		{name: "BadTokenDeletes", pushes: 2, answers: []fakeAPNsAnswer{badToken, badToken}},
		{name: "BadTokenAfterReregistering", pushes: 2, answers: []fakeAPNsAnswer{badToken, badToken}, stale: true, wantKept: true, wantEnv: "sandbox"},
		{name: "OtherEnvironmentMoves", pushes: 2, answers: []fakeAPNsAnswer{badToken}, wantKept: true, wantEnv: "production"},
		{name: "OtherEnvironmentAfterReregistering", pushes: 2, answers: []fakeAPNsAnswer{badToken}, stale: true, wantKept: true, wantEnv: "sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newForkPushTestEnv(t, "vapid")
			registered := dbtime.Now().Add(-time.Hour)
			device := env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true, Now: registered})
			if tc.stale {
				env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, Questions: true, Now: registered.Add(time.Minute)})
			}
			for _, answer := range tc.answers {
				if answer.Status == http.StatusGone && tc.invalidSince != 0 {
					answer.Timestamp = registered.Add(tc.invalidSince)
				}
				env.apns.answer(answer)
			}

			res := env.send(t, device)
			require.Len(t, env.pushes(0), tc.pushes)
			devices := env.devices(t)
			if !tc.wantKept {
				require.False(t, res.ok())
				require.Empty(t, devices)
				return
			}
			require.Len(t, devices, 1)
			require.Equal(t, tc.wantEnv, devices[0].Environment)
			if tc.stale {
				require.True(t, devices[0].Questions, "the newer registration is intact")
				require.False(t, devices[0].FinishedTurns)
			}
		})
	}
}

// newForkPushTestRoute serves the test push route of env's dispatcher and
// returns a client of env's user.
func newForkPushTestRoute(t *testing.T, env *forkPushTestEnv) *codersdk.Client {
	t.Helper()
	api := &API{
		Options: &Options{Database: env.db, Logger: testutil.Logger(t)},
		HTTPAuth: &HTTPAuthorizer{
			Authorizer: rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()),
			Logger:     testutil.Logger(t),
		},
		forkPush: env.d,
	}
	srv := httptest.NewServer(httpmw.ExtractAPIKeyMW(httpmw.ExtractAPIKeyConfig{DB: env.raw, Logger: testutil.Logger(t)})(
		http.HandlerFunc(api.postForkChatAPNsDeviceTest)))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	_, token := dbgen.APIKey(t, env.raw, database.APIKey{UserID: env.user.ID})
	return codersdk.New(serverURL, codersdk.WithSessionToken(token))
}

func TestForkChatAPNsTestPushRoute(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "vapid")
	client := newForkPushTestRoute(t, env)
	ctx := testutil.Context(t, testutil.WaitShort)

	err := client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: testAPNsToken})
	requireForkPushStatus(t, err, http.StatusNotFound)

	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true, Sound: true})
	// The token is looked up in lower case, whatever case the app sends.
	require.NoError(t, client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: strings.ToUpper(testAPNsToken)}))
	require.Equal(t, []forkPushTestPush{{
		Environment: "sandbox", Token: testAPNsToken, Title: "It's working!", Body: "You've subscribed to push notifications.", Sound: "chime.caf",
	}}, env.pushes(0))

	// A token that APNs rejects in both environments is reported and
	// deleted.
	env.apns.answer(
		fakeAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"},
		fakeAPNsAnswer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"},
	)
	err = client.TestChatAPNsDevice(ctx, codersdk.ChatAPNsDeviceRequest{DeviceToken: testAPNsToken})
	requireForkPushStatus(t, err, http.StatusBadGateway)
	require.ErrorContains(t, err, "400 BadDeviceToken")
	require.Empty(t, env.devices(t))
}

// A test push that APNs never answers ends in a 502 at the send timeout.
func TestForkChatAPNsTestPushTimesOut(t *testing.T) {
	t.Parallel()
	env := newForkPushTestEnv(t, "vapid")
	env.d.timeout = testutil.IntervalFast
	client := newForkPushTestRoute(t, env)
	env.register(t, forkdb.UpsertChatAPNsDeviceParams{DeviceToken: testAPNsToken, FinishedTurns: true})
	env.apns.stall(testAPNsToken)

	err := client.TestChatAPNsDevice(testutil.Context(t, testutil.WaitShort), codersdk.ChatAPNsDeviceRequest{DeviceToken: testAPNsToken})
	requireForkPushStatus(t, err, http.StatusBadGateway)
	require.ErrorContains(t, err, "Failed to send the test push.")
	require.Len(t, env.devices(t), 1)
}

func requireForkPushStatus(t *testing.T, err error, status int) {
	t.Helper()
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, status, sdkErr.StatusCode())
}
