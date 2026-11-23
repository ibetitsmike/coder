package coderd

// This file is fork code: it sends the chat pushes of the fork-apns-push
// experiment to iOS devices over APNs, next to upstream's web push. chatd
// receives the dispatcher in place of upstream's, so chatd.go is unchanged.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/webpush"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/quartz"
)

// forkAPNsOnlyPublicKey is what the dispatcher reports as its public key
// when web push is off. chatd sends a push only when the key is not empty
// and reads it for nothing else, while the browser routes and buildinfo read
// upstream's dispatcher, so no client ever sees this value.
const forkAPNsOnlyPublicKey = "fork-apns-push"

// forkAPNsSendTimeout bounds the APNs sends of one push, so a slow APNs
// never holds a turn or shutdown for long.
const forkAPNsSendTimeout = 10 * time.Second

// forkChatPushDispatcher returns the push dispatcher for chatd: inner itself
// unless the fork-apns-push experiment is on and the APNs options are set.
// A missing option or a key that does not load turns APNs off with an error
// log rather than stopping the server, so a config edit never takes the
// deployment down.
func (api *API) forkChatPushDispatcher(inner webpush.Dispatcher) webpush.Dispatcher {
	fork := api.DeploymentValues.AI.Chat.Fork
	if !api.Experiments.Enabled(codersdk.ExperimentForkAPNsPush) || !fork.APNsConfigured() {
		return inner
	}
	if missing := fork.APNsMissing(); len(missing) > 0 {
		api.Logger.Error(api.ctx, "turned APNs push off: an APNs option is not set", slog.F("missing", missing))
		return inner
	}
	client, err := newForkAPNsClient(fork.APNsKeyFile.Value(), fork.APNsKeyID.Value(), fork.APNsTeamID.Value(), fork.APNsTopic.Value(), quartz.NewReal())
	if err != nil {
		api.Logger.Error(api.ctx, "turned APNs push off: the APNs key does not load", slog.F("key_file", fork.APNsKeyFile.Value()), slog.Error(err))
		return inner
	}
	if inner == nil {
		inner = &webpush.NoopWebpusher{}
	}
	api.forkPush = newForkPushDispatcher(inner, client, api.Database, api.Logger.Named("apns"), api.AccessURL.String())
	return api.forkPush
}

// closeForkPushDispatcher waits for the APNs sends in flight.
func (api *API) closeForkPushDispatcher() {
	if api.forkPush != nil {
		api.forkPush.close()
	}
}

type forkPushDispatcher struct {
	inner      webpush.Dispatcher
	apns       *forkAPNsClient
	db         database.Store
	logger     slog.Logger
	deployment string
	timeout    time.Duration
	// clock runs the send timeout.
	clock quartz.Clock

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

var _ webpush.Dispatcher = (*forkPushDispatcher)(nil)

func newForkPushDispatcher(inner webpush.Dispatcher, apns *forkAPNsClient, db database.Store, logger slog.Logger, deployment string) *forkPushDispatcher {
	return &forkPushDispatcher{
		inner:      inner,
		apns:       apns,
		db:         db,
		logger:     logger,
		deployment: deployment,
		timeout:    forkAPNsSendTimeout,
		clock:      apns.clock,
	}
}

// withSendTimeout returns ctx bounded by the send timeout.
func (d *forkPushDispatcher) withSendTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	timer := d.clock.AfterFunc(d.timeout, cancel, "fork-apns", "send")
	return ctx, func() {
		timer.Stop()
		cancel()
	}
}

func (d *forkPushDispatcher) PublicKey() string {
	if key := d.inner.PublicKey(); key != "" {
		return key
	}
	return forkAPNsOnlyPublicKey
}

func (d *forkPushDispatcher) Test(ctx context.Context, req codersdk.WebpushSubscription) error {
	return d.inner.Test(ctx, req)
}

// Dispatch starts the APNs sends, then sends the web push and returns. The
// web push result is the result: APNs never fails it, and a slow web push
// never delays APNs.
func (d *forkPushDispatcher) Dispatch(ctx context.Context, userID uuid.UUID, msg codersdk.WebpushMessage) error {
	d.startAPNs(ctx, userID, msg)
	if d.inner.PublicKey() == "" {
		return nil
	}
	return d.inner.Dispatch(ctx, userID, msg)
}

func (d *forkPushDispatcher) startAPNs(ctx context.Context, userID uuid.UUID, msg codersdk.WebpushMessage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	// chatd stops its context once Dispatch returns.
	sendCtx, cancel := d.withSendTimeout(context.WithoutCancel(ctx))
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		d.dispatchAPNs(sendCtx, userID, msg)
	}()
}

func (d *forkPushDispatcher) close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	d.wg.Wait()
}

// forkPushKind is the chat event that a push reports.
type forkPushKind int

const (
	forkPushFinishedTurn forkPushKind = iota
	forkPushQuestion
	forkPushAgentError
)

// forkPushKindOf tells the event of a push by the chat's status, since a
// web push message does not carry it. Only the question push fires while a
// turn runs. If a new turn starts before a finished turn's push is read, that
// push counts as a question.
func forkPushKindOf(status database.ChatStatus) forkPushKind {
	switch status {
	case database.ChatStatusError:
		return forkPushAgentError
	case database.ChatStatusWaiting, database.ChatStatusPaused:
		return forkPushFinishedTurn
	default:
		return forkPushQuestion
	}
}

// wantedBy reports whether a device takes a push of kind. A turn that ends
// with a question looks like a finished turn, so a finished turn goes to a
// device that takes either.
func (k forkPushKind) wantedBy(device forkdb.ChatApnsDevice) bool {
	switch k {
	case forkPushAgentError:
		return device.AgentErrors
	case forkPushQuestion:
		return device.Questions
	default:
		return device.FinishedTurns || device.Questions
	}
}

func (d *forkPushDispatcher) dispatchAPNs(ctx context.Context, userID uuid.UUID, msg codersdk.WebpushMessage) {
	chatID, ok := forkPushChatID(msg)
	if !ok {
		return
	}
	//nolint:gocritic // Pushes run outside any request: chatd reads the chat it pushes about.
	chat, err := d.db.GetChatByID(dbauthz.AsChatd(ctx), chatID)
	if err != nil {
		d.logger.Warn(ctx, "read chat for APNs push", slog.F("chat_id", chatID), slog.Error(err))
		return
	}
	kind := forkPushKindOf(chat.Status)
	fdb, err := forkdb.From(d.db)
	if err != nil {
		d.logger.Error(ctx, "fork queries for APNs push", slog.Error(err))
		return
	}
	//nolint:gocritic // The notifier reads the user's devices and prunes the ones APNs rejects, like upstream's web push.
	notifierCtx := dbauthz.AsNotifier(ctx)
	devices, err := fdb.GetChatAPNsDevicesByUserID(notifierCtx, userID)
	if err != nil {
		d.logger.Warn(ctx, "list APNs devices", slog.F("user_id", userID), slog.Error(err))
		return
	}
	// Registration keeps a user's devices under the limit, but two
	// registrations at once can pass it, so a push also sends only to the
	// newest.
	if len(devices) > forkAPNsMaxDevicesPerUser {
		slices.SortFunc(devices, func(a, b forkdb.ChatApnsDevice) int {
			return b.UpdatedAt.Compare(a.UpdatedAt)
		})
		devices = devices[:forkAPNsMaxDevicesPerUser]
	}
	// Each device gets its own send, so a device whose APNs host stalls
	// does not use up the time of the others.
	var wg sync.WaitGroup
	for _, device := range devices {
		if !kind.wantedBy(device) {
			continue
		}
		push := forkAPNsPush{
			Title:      msg.Title,
			Body:       msg.Body,
			ChatID:     chatID.String(),
			Deployment: d.deployment,
			Sound:      device.Sound,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.sendToDevice(notifierCtx, fdb, device, push)
		}()
	}
	wg.Wait()
}

// sendToDevice sends a push to one device and keeps its row in line with
// APNs's answer: a dead token is deleted, and a token that APNs knows only in
// the other environment moves there. A token rejected in both environments
// is deleted.
func (d *forkPushDispatcher) sendToDevice(ctx context.Context, fdb forkdb.Store, device forkdb.ChatApnsDevice, push forkAPNsPush) (forkAPNsResponse, error) {
	logger := d.logger.With(slog.F("device", forkAPNsTokenTag(device.DeviceToken)), slog.F("chat_id", push.ChatID))
	env := codersdk.ChatAPNsEnvironment(device.Environment)
	res, err := d.apns.send(ctx, env, device.DeviceToken, push)
	if err != nil {
		logger.Warn(ctx, "failed to send APNs push", slog.Error(err))
		return res, err
	}
	if res.Status == http.StatusBadRequest && res.Reason == "BadDeviceToken" {
		other := otherForkAPNsEnvironment(env)
		retry, err := d.apns.send(ctx, other, device.DeviceToken, push)
		if err != nil {
			logger.Warn(ctx, "failed to send APNs push", slog.Error(err))
			return res, err
		}
		if retry.ok() {
			if err := fdb.UpdateChatAPNsDeviceEnvironment(ctx, forkdb.UpdateChatAPNsDeviceEnvironmentParams{
				DeviceToken: device.DeviceToken,
				Environment: string(other),
				UpdatedAt:   d.apns.clock.Now(),
				NotAfter:    device.UpdatedAt,
			}); err != nil {
				logger.Warn(ctx, "update APNs device environment", slog.Error(err))
			}
			return retry, nil
		}
		res = retry
	}
	switch {
	case res.ok():
	case res.Status == http.StatusGone || (res.Status == http.StatusBadRequest && (res.Reason == "BadDeviceToken" || res.Reason == "DeviceTokenNotForTopic")):
		logger.Info(ctx, "deleting a device token that APNs rejected", slog.F("response", res.String()))
		if err := fdb.DeleteChatAPNsDeviceByToken(ctx, forkdb.DeleteChatAPNsDeviceByTokenParams{
			DeviceToken: device.DeviceToken,
			NotAfter:    forkAPNsDeleteNotAfter(device, res),
		}); err != nil {
			logger.Warn(ctx, "delete APNs device", slog.Error(err))
		}
	default:
		logger.Warn(ctx, "dropped a push that APNs rejected", slog.F("response", res.String()))
	}
	return res, nil
}

// forkAPNsDeleteNotAfter bounds the delete of a rejected token. APNs's 410
// gives the time the token went invalid, and only a registration after that
// time is valid again. Without that time, a device that registered again
// after the row this push read keeps its row.
func forkAPNsDeleteNotAfter(device forkdb.ChatApnsDevice, res forkAPNsResponse) time.Time {
	if !res.Timestamp.IsZero() {
		return res.Timestamp
	}
	return device.UpdatedAt
}

// forkPushChatID reads the chat of a chatd push from its /agents/<id> URL.
func forkPushChatID(msg codersdk.WebpushMessage) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(msg.Data["url"], "/agents/")
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	return id, err == nil
}
