package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
	"golang.org/x/xerrors"
	"google.golang.org/protobuf/proto"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
	"github.com/coder/safedial"
)

const (
	// scanInterval finds chat events that pubsub lost.
	scanInterval = 15 * time.Second
	// maxWebhookBodyBytes caps the body of one webhook request.
	maxWebhookBodyBytes = 1 << 20
	// maxQRCodeBytes and maxInstructionsBytes cap a connect prompt, which
	// coderd shows on its own origin.
	maxQRCodeBytes       = 256 << 10
	maxInstructionsBytes = 2 << 10
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// WebhookResponse is what on_http returned.
type WebhookResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// runner runs one plugin in one plugin-host process.
type runner struct {
	m        *Manager
	plugin   forkdb.Plugin
	manifest codersdk.PluginManifest
	grants   codersdk.PluginGrants
	config   map[string]string
	logger   slog.Logger
	http     *http.Client

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}

	deliverWake chan struct{}
	linkLimiter *rate.Limiter
	logLimiter  *rate.Limiter

	// callMu serializes export calls: one drpc connection runs one
	// stream at a time, and a Go reactor runs one call at a time.
	callMu sync.Mutex
	client *pluginhost.Client

	keysMu sync.Mutex
	keys   map[uuid.UUID]userClient

	subsMu sync.Mutex
	subs   map[uuid.UUID]func()
}

func (m *Manager) startRunner(p forkdb.Plugin, prev *runner) *runner {
	ctx, cancel := context.WithCancel(m.ctx)
	r := &runner{
		m:           m,
		plugin:      p,
		logger:      m.logger.With(slog.F("plugin_id", p.ID), slog.F("plugin_name", p.Name)),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		ready:       make(chan struct{}),
		deliverWake: make(chan struct{}, 1),
		linkLimiter: rate.NewLimiter(rate.Every(12*time.Second), 5),
		logLimiter:  rate.NewLimiter(20, 100),
		keys:        map[uuid.UUID]userClient{},
		subs:        map[uuid.UUID]func(){},
	}
	// Decode before the goroutine starts: webhook calls read these fields
	// from other goroutines.
	initErr := r.decode()
	go func() {
		defer close(r.done)
		defer r.unsubscribeAll()
		if prev != nil {
			<-prev.done
		}
		err := initErr
		if err == nil {
			err = r.run(ctx)
		}
		if err != nil && ctx.Err() == nil {
			r.logger.Error(ctx, "plugin process stopped", slog.Error(err))
			r.setLastError(err.Error())
		}
	}()
	return r
}

func (r *runner) stop() { r.cancel() }

func (r *runner) exited() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// sys returns a system context for runner database work. Plugins act for
// many users across organizations, and every host call is authorized against
// the plugin grants and chat links before it reaches the database.
func sys(ctx context.Context) context.Context {
	//nolint:gocritic // Host calls are authorized by grants and links first.
	return dbauthz.AsSystemRestricted(ctx)
}

func (r *runner) setLastError(msg string) {
	if len(msg) > 2048 {
		msg = msg[:2048]
	}
	// The run context can be done; the error must still be stored.
	ctx, cancel := context.WithTimeout(sys(context.Background()), 5*time.Second)
	defer cancel()
	err := r.m.fdb.UpdatePluginLastError(ctx, forkdb.UpdatePluginLastErrorParams{ID: r.plugin.ID, LastError: msg})
	if err != nil {
		r.logger.Warn(ctx, "store plugin error", slog.Error(err))
	}
}

func (r *runner) decode() error {
	if err := json.Unmarshal(r.plugin.Manifest, &r.manifest); err != nil {
		return xerrors.Errorf("decode manifest: %w", err)
	}
	if err := json.Unmarshal(r.plugin.Grants, &r.grants); err != nil {
		return xerrors.Errorf("decode grants: %w", err)
	}
	r.config = map[string]string{}
	if r.plugin.Config != "" {
		if err := json.Unmarshal([]byte(r.plugin.Config), &r.config); err != nil {
			return xerrors.Errorf("decode config: %w", err)
		}
	}
	cidrs, err := ParsePrivateCIDRs(r.grants.PrivateCIDRs)
	if err != nil {
		return err
	}
	r.http = safedial.NewHTTPClient(r.m.opts.HTTPClient,
		safedial.WithAllowedPrefixes(cidrs...),
		safedial.WithRedirectPolicy(safedial.RedirectSameOrigin),
	)
	r.http.Timeout = 10 * time.Second
	return nil
}

func (r *runner) run(ctx context.Context) error {
	file, err := r.m.db.GetFileByID(sys(ctx), r.plugin.FileID)
	if err != nil {
		return xerrors.Errorf("get module file: %w", err)
	}
	client, err := r.m.opts.StartHost(ctx)
	if err != nil {
		return xerrors.Errorf("start plugin host: %w", err)
	}
	defer client.Close()
	loaded, err := client.Load(ctx, file.Data, r.m.opts.Limits)
	if err != nil {
		return xerrors.Errorf("load module: %w", err)
	}
	// The module bytes are immutable, but compare anyway: the grants
	// were accepted for the stored manifest only.
	if err := ValidateGrants(ManifestFromProto(loaded), r.grants); err != nil {
		return xerrors.Errorf("module manifest changed: %w", err)
	}
	r.client = client
	close(r.ready)
	r.setLastError("")
	r.logger.Info(ctx, "plugin process started")

	var tick <-chan time.Time
	if r.grants.TickSeconds > 0 {
		t := r.m.clock.NewTicker(time.Duration(r.grants.TickSeconds)*time.Second, "plugins", "tick")
		defer t.Stop()
		tick = t.C
	}
	scan := r.m.clock.NewTicker(scanInterval, "plugins", "scan")
	defer scan.Stop()

	r.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-client.Done():
			return xerrors.Errorf("plugin host exited: %w", client.Wait())
		case <-tick:
			r.tick(ctx)
		case <-scan.C:
			r.scan(ctx)
		case <-r.deliverWake:
			r.deliver(ctx)
		}
	}
}

// invoke calls one export and decodes its output.
func (r *runner) invoke(ctx context.Context, export string, in, out proto.Message) error {
	var input []byte
	if in != nil {
		var err error
		input, err = proto.Marshal(in)
		if err != nil {
			return xerrors.Errorf("encode input: %w", err)
		}
	}
	r.callMu.Lock()
	defer r.callMu.Unlock()
	output, err := r.client.Invoke(ctx, export, input, r.hostCall)
	if err != nil {
		return xerrors.Errorf("call %s: %w", export, err)
	}
	if err := proto.Unmarshal(output, out); err != nil {
		return xerrors.Errorf("decode output of %s: %w", export, err)
	}
	return nil
}

func (r *runner) invokeResult(ctx context.Context, export string, in proto.Message) error {
	var res pluginproto.Result
	if err := r.invoke(ctx, export, in, &res); err != nil {
		return err
	}
	if res.GetError() != "" {
		return xerrors.Errorf("%s: %s", export, res.GetError())
	}
	return nil
}

func (r *runner) tick(ctx context.Context) {
	if err := r.invokeResult(ctx, "coder_on_tick", nil); err != nil && ctx.Err() == nil {
		r.logger.Warn(ctx, "plugin tick failed", slog.Error(err))
		r.setLastError(err.Error())
	}
}

// scan keeps one chat watch subscription for each linked user and then
// delivers. A watch event only wakes delivery; the scan finds lost events.
func (r *runner) scan(ctx context.Context) {
	if r.delivers() {
		r.syncSubscriptions(ctx)
	}
	r.deliver(ctx)
}

func (r *runner) wakeDelivery() {
	select {
	case r.deliverWake <- struct{}{}:
	default:
	}
}

func (r *runner) syncSubscriptions(ctx context.Context) {
	links, err := r.m.fdb.GetPluginUserLinksByPluginID(sys(ctx), r.plugin.ID)
	if err != nil {
		r.logger.Warn(ctx, "list plugin links", slog.Error(err))
		return
	}
	users := map[uuid.UUID]struct{}{}
	for _, l := range links {
		users[l.UserID] = struct{}{}
	}
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	for id, cancel := range r.subs {
		if _, ok := users[id]; !ok {
			cancel()
			delete(r.subs, id)
		}
	}
	for id := range users {
		if _, ok := r.subs[id]; ok {
			continue
		}
		cancel, err := r.m.opts.Pubsub.Subscribe(coderdpubsub.ChatWatchEventChannel(id), func(context.Context, []byte) {
			r.wakeDelivery()
		})
		if err != nil {
			r.logger.Warn(ctx, "subscribe to chat events", slog.F("user_id", id), slog.Error(err))
			continue
		}
		r.subs[id] = cancel
	}
}

func (r *runner) unsubscribeAll() {
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	for id, cancel := range r.subs {
		cancel()
		delete(r.subs, id)
	}
}

// waitReady waits until the plugin process has loaded the module.
func (r *runner) waitReady(ctx context.Context) error {
	select {
	case <-r.ready:
		return nil
	case <-r.done:
		return ErrNotRunningHere
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *runner) webhook(ctx context.Context, req *http.Request) (*WebhookResponse, error) {
	if !r.grants.Webhook {
		return nil, xerrors.New("the plugin has no webhook grant")
	}
	if err := r.waitReady(ctx); err != nil {
		return nil, err
	}
	body, err := readWebhookBody(req)
	if err != nil {
		return nil, err
	}
	in := &pluginproto.HTTPRequest{
		Method:  req.Method,
		Url:     req.URL.RequestURI(),
		Headers: headersToProto(req.Header),
		Body:    body,
	}
	var out pluginproto.HTTPResponse
	if err := r.invoke(ctx, "coder_on_http", in, &out); err != nil {
		return nil, err
	}
	status := int(out.GetStatus())
	if status == 0 {
		status = http.StatusOK
	}
	return &WebhookResponse{StatusCode: status, Header: headersFromProto(out.GetHeaders()), Body: out.GetBody()}, nil
}

func (r *runner) connect(ctx context.Context) (codersdk.PluginConnectPrompt, error) {
	if !r.grants.Connect {
		return codersdk.PluginConnectPrompt{}, xerrors.New("the plugin has no connect grant")
	}
	if err := r.waitReady(ctx); err != nil {
		return codersdk.PluginConnectPrompt{}, err
	}
	var out pluginproto.ConnectPrompt
	if err := r.invoke(ctx, "coder_on_connect", nil, &out); err != nil {
		return codersdk.PluginConnectPrompt{}, err
	}
	return connectPromptFromProto(&out)
}

// connectPromptFromProto checks the output of coder_on_connect before coderd
// shows it to a user.
func connectPromptFromProto(out *pluginproto.ConnectPrompt) (codersdk.PluginConnectPrompt, error) {
	if out.GetError() != "" {
		return codersdk.PluginConnectPrompt{}, xerrors.Errorf("coder_on_connect: %s", out.GetError())
	}
	png, instructions := out.GetQrCodePng(), out.GetInstructions()
	if len(png) == 0 && instructions == "" {
		return codersdk.PluginConnectPrompt{}, xerrors.New("the plugin returned an empty connect prompt")
	}
	if len(png) > 0 && (!bytes.HasPrefix(png, pngSignature) || len(png) > maxQRCodeBytes) {
		return codersdk.PluginConnectPrompt{}, xerrors.Errorf("the QR code of the plugin must be a PNG image of at most %d bytes", maxQRCodeBytes)
	}
	if len(instructions) > maxInstructionsBytes {
		return codersdk.PluginConnectPrompt{}, xerrors.Errorf("the connect instructions of the plugin must be at most %d bytes", maxInstructionsBytes)
	}
	return codersdk.PluginConnectPrompt{QRCodePNG: png, Instructions: instructions}, nil
}

func headersToProto(h http.Header) []*pluginproto.Header {
	out := make([]*pluginproto.Header, 0, len(h))
	for name, values := range h {
		for _, v := range values {
			out = append(out, &pluginproto.Header{Name: name, Value: v})
		}
	}
	return out
}

func headersFromProto(headers []*pluginproto.Header) http.Header {
	h := http.Header{}
	for _, hd := range headers {
		h.Add(hd.GetName(), hd.GetValue())
	}
	return h
}
