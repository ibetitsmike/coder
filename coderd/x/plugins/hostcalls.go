package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/apikey"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

const (
	maxChatTextBytes     = 32 << 10
	maxFetchBodyBytes    = 1 << 20
	maxLogMessageBytes   = 2 << 10
	pluginKeyLifetime    = time.Hour
	pluginKeyRenewMargin = 10 * time.Minute
	// ChatLabelPlugin labels each chat a plugin starts with the plugin name.
	ChatLabelPlugin = "coder.com/plugin"
)

var errNotLinked = xerrors.New("this account is not linked to a Coder user")

// hostCall serves one coder.call of the module. Each grant check happens
// here, in coderd, so a module cannot skip it.
func (r *runner) hostCall(ctx context.Context, call *pluginproto.HostCall) *pluginproto.HostResult {
	var (
		res *pluginproto.HostResult
		err error
	)
	switch c := call.GetCall().(type) {
	case *pluginproto.HostCall_ChatSend:
		var out *pluginproto.ChatSendResult
		out, err = r.chatSend(ctx, c.ChatSend)
		res = &pluginproto.HostResult{Result: &pluginproto.HostResult_ChatSend{ChatSend: out}}
	case *pluginproto.HostCall_LinkComplete:
		var out *pluginproto.LinkCompleteResult
		out, err = r.linkComplete(ctx, c.LinkComplete)
		res = &pluginproto.HostResult{Result: &pluginproto.HostResult_LinkComplete{LinkComplete: out}}
	case *pluginproto.HostCall_HttpFetch:
		var out *pluginproto.HTTPResponse
		out, err = r.httpFetch(ctx, c.HttpFetch)
		res = &pluginproto.HostResult{Result: &pluginproto.HostResult_HttpFetch{HttpFetch: out}}
	case *pluginproto.HostCall_ConfigGet:
		var value string
		value, err = r.configGet(c.ConfigGet)
		res = &pluginproto.HostResult{Result: &pluginproto.HostResult_ConfigValue{ConfigValue: value}}
	case *pluginproto.HostCall_Log:
		r.log(ctx, c.Log)
		res = &pluginproto.HostResult{}
	default:
		err = xerrors.New("unknown host call")
	}
	if err != nil {
		return &pluginproto.HostResult{Error: err.Error()}
	}
	return res
}

func (r *runner) hasChats(capability string) bool {
	return slices.Contains(r.grants.Chats, capability)
}

func (r *runner) chatSend(ctx context.Context, req *pluginproto.ChatSend) (*pluginproto.ChatSendResult, error) {
	if !r.hasChats(codersdk.PluginChatSend) {
		return nil, xerrors.New("the plugin has no chats send grant")
	}
	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return nil, xerrors.New("text is empty")
	}
	if len(text) > maxChatTextBytes {
		return nil, xerrors.Errorf("text is longer than %d bytes", maxChatTextBytes)
	}
	link, err := r.m.fdb.GetPluginUserLink(sys(ctx), forkdb.GetPluginUserLinkParams{
		PluginID:   r.plugin.ID,
		ExternalID: req.GetExternalId(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotLinked
	}
	if err != nil {
		return nil, xerrors.Errorf("get link: %w", err)
	}
	client, err := r.clientFor(ctx, link.UserID)
	if err != nil {
		return nil, err
	}
	content := []codersdk.ChatInputPart{{Type: codersdk.ChatInputPartTypeText, Text: text}}

	if !req.GetNewChat() && link.ActiveChatID.Valid {
		_, err := client.CreateChatMessage(ctx, link.ActiveChatID.UUID, codersdk.CreateChatMessageRequest{Content: content})
		if err == nil {
			return &pluginproto.ChatSendResult{ChatId: link.ActiveChatID.UUID.String()}, nil
		}
		var sdkErr *codersdk.Error
		if !errors.As(err, &sdkErr) || (sdkErr.StatusCode() != http.StatusNotFound && sdkErr.StatusCode() != http.StatusGone) {
			return nil, xerrors.Errorf("send message: %w", err)
		}
		// The active chat is gone or archived: start a new chat.
	}

	if !r.hasChats(codersdk.PluginChatCreate) {
		return nil, xerrors.New("the plugin has no chats create grant")
	}
	chat, err := client.CreateChat(ctx, codersdk.CreateChatRequest{
		OrganizationID: r.plugin.OrganizationID,
		Content:        content,
		Labels:         map[string]string{ChatLabelPlugin: r.plugin.Name},
		ClientType:     codersdk.ChatClientTypeAPI,
	})
	if err != nil {
		return nil, xerrors.Errorf("create chat: %w", err)
	}
	err = r.m.fdb.SetPluginUserLinkActiveChat(sys(ctx), forkdb.SetPluginUserLinkActiveChatParams{
		PluginID:     r.plugin.ID,
		ExternalID:   link.ExternalID,
		ActiveChatID: uuid.NullUUID{UUID: chat.ID, Valid: true},
	})
	if err != nil {
		return nil, xerrors.Errorf("set active chat: %w", err)
	}
	if err := r.m.fdb.EnsurePluginChatBindings(sys(ctx), r.plugin.ID); err != nil {
		return nil, xerrors.Errorf("bind chat: %w", err)
	}
	return &pluginproto.ChatSendResult{ChatId: chat.ID.String()}, nil
}

// NormalizeLinkCode removes the separators and case of a link code, so
// "k7q-2mx" and "K7Q2MX" are the same code.
func NormalizeLinkCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(code)))
}

// HashLinkCode returns the stored form of a link code.
func HashLinkCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormalizeLinkCode(code)))
	return sum[:]
}

func (r *runner) linkComplete(ctx context.Context, req *pluginproto.LinkComplete) (*pluginproto.LinkCompleteResult, error) {
	if !r.hasChats(codersdk.PluginChatCreate) && !r.hasChats(codersdk.PluginChatSend) {
		return nil, xerrors.New("the plugin has no chats grant")
	}
	externalID := strings.TrimSpace(req.GetExternalId())
	if externalID == "" || len(externalID) > 256 {
		return nil, xerrors.New("external_id must be 1 to 256 bytes")
	}
	// The module can send any code, so limit guesses.
	if !r.linkLimiter.Allow() {
		return nil, xerrors.New("too many link attempts, try again later")
	}
	var user database.User
	err := forkdb.InTx(r.m.db, func(tx database.Store, ftx forkdb.Store) error {
		code, err := ftx.ConsumePluginLinkCode(sys(ctx), forkdb.ConsumePluginLinkCodeParams{
			PluginID: r.plugin.ID,
			CodeHash: HashLinkCode(req.GetCode()),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return xerrors.New("the code is not valid or it expired")
		}
		if err != nil {
			return xerrors.Errorf("consume link code: %w", err)
		}
		user, err = tx.GetUserByID(sys(ctx), code.UserID)
		if err != nil {
			return xerrors.Errorf("get user: %w", err)
		}
		_, err = ftx.UpsertPluginUserLink(sys(ctx), forkdb.UpsertPluginUserLinkParams{
			PluginID:   r.plugin.ID,
			ExternalID: externalID,
			UserID:     code.UserID,
		})
		if err != nil {
			return xerrors.Errorf("store link: %w", err)
		}
		return ftx.EnsurePluginChatBindings(sys(ctx), r.plugin.ID)
	}, nil)
	if err != nil {
		return nil, err
	}
	r.logger.Info(ctx, "linked external account", slog.F("user_id", user.ID))
	if r.delivers() {
		r.syncSubscriptions(ctx)
	}
	return &pluginproto.LinkCompleteResult{Username: user.Username}, nil
}

var allowedFetchMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// Headers the module cannot set: the transport owns them.
var blockedFetchHeaders = []string{"Host", "Connection", "Content-Length", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer"}

func (r *runner) httpFetch(ctx context.Context, req *pluginproto.HTTPRequest) (*pluginproto.HTTPResponse, error) {
	u, err := url.Parse(req.GetUrl())
	if err != nil {
		return nil, xerrors.Errorf("parse url: %w", err)
	}
	if !allowsOrigin(r.grants.HTTPOrigins, u) {
		return nil, xerrors.Errorf("the origin of %q is not granted", u.Redacted())
	}
	method := strings.ToUpper(req.GetMethod())
	if method == "" {
		method = http.MethodGet
	}
	if !slices.Contains(allowedFetchMethods, method) {
		return nil, xerrors.Errorf("method %q is not allowed", method)
	}
	if len(req.GetBody()) > maxFetchBodyBytes {
		return nil, xerrors.Errorf("request body is longer than %d bytes", maxFetchBodyBytes)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(req.GetBody()))
	if err != nil {
		return nil, xerrors.Errorf("build request: %w", err)
	}
	for _, h := range req.GetHeaders() {
		if slices.ContainsFunc(blockedFetchHeaders, func(b string) bool { return strings.EqualFold(b, h.GetName()) }) {
			continue
		}
		hreq.Header.Add(h.GetName(), h.GetValue())
	}
	resp, err := r.http.Do(hreq)
	if err != nil {
		return nil, xerrors.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBodyBytes+1))
	if err != nil {
		return nil, xerrors.Errorf("read response: %w", err)
	}
	if len(body) > maxFetchBodyBytes {
		return nil, xerrors.Errorf("response body is longer than %d bytes", maxFetchBodyBytes)
	}
	return &pluginproto.HTTPResponse{
		Status:  int32(resp.StatusCode), //nolint:gosec // HTTP status codes fit in int32.
		Headers: headersToProto(resp.Header),
		Body:    body,
	}, nil
}

func (r *runner) configGet(key string) (string, error) {
	if !slices.Contains(r.grants.ConfigKeys, key) {
		return "", xerrors.Errorf("config key %q is not granted", key)
	}
	return r.config[key], nil
}

func (r *runner) log(ctx context.Context, line *pluginproto.LogLine) {
	if !r.logLimiter.Allow() {
		return
	}
	msg := line.GetMessage()
	if len(msg) > maxLogMessageBytes {
		msg = msg[:maxLogMessageBytes]
	}
	field := slog.F("plugin_message", msg)
	switch line.GetLevel() {
	case pluginproto.LogLine_LEVEL_DEBUG:
		r.logger.Debug(ctx, "plugin log message", field)
	case pluginproto.LogLine_LEVEL_WARN:
		r.logger.Warn(ctx, "plugin log message", field)
	case pluginproto.LogLine_LEVEL_ERROR:
		r.logger.Error(ctx, "plugin log message", field)
	default:
		r.logger.Info(ctx, "plugin log message", field)
	}
}

type userClient struct {
	client    *codersdk.ExperimentalClient
	expiresAt time.Time
}

// clientFor returns a client that sends requests straight to the coderd
// router as the linked user, so a host call gets the same authorization,
// validation, rate limits and audit as a user request. The key has the
// coder:chats.converse scope and stays in coderd memory.
func (r *runner) clientFor(ctx context.Context, userID uuid.UUID) (*codersdk.ExperimentalClient, error) {
	r.keysMu.Lock()
	defer r.keysMu.Unlock()
	now := r.m.clock.Now()
	if c, ok := r.keys[userID]; ok && c.expiresAt.After(now.Add(pluginKeyRenewMargin)) {
		return c.client, nil
	}
	user, err := r.m.db.GetUserByID(sys(ctx), userID)
	if err != nil {
		return nil, xerrors.Errorf("get linked user: %w", err)
	}
	expiresAt := now.Add(pluginKeyLifetime)
	params, token, err := apikey.Generate(apikey.CreateParams{
		UserID:          userID,
		LoginType:       user.LoginType,
		ExpiresAt:       expiresAt,
		LifetimeSeconds: int64(pluginKeyLifetime.Seconds()),
		TokenName:       fmt.Sprintf("plugin_%s", r.plugin.ID),
		Scopes:          database.APIKeyScopes{forkdb.APIKeyScopeCoderChatsConverse},
	})
	if err != nil {
		return nil, xerrors.Errorf("generate key: %w", err)
	}
	if _, err := r.m.db.InsertAPIKey(sys(ctx), params); err != nil {
		return nil, xerrors.Errorf("insert key: %w", err)
	}
	sdk := codersdk.New(r.m.opts.AccessURL,
		codersdk.WithSessionToken(token),
		codersdk.WithHTTPClient(&http.Client{Transport: handlerTransport{handler: r.m.opts.Handler}}),
	)
	c := userClient{client: codersdk.NewExperimentalClient(sdk), expiresAt: expiresAt}
	r.keys[userID] = c
	return c.client, nil
}

// handlerTransport serves requests with an in-process handler. The
// requests never reach the network.
type handlerTransport struct {
	handler func() http.Handler
}

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h := t.handler()
	if h == nil {
		return nil, xerrors.New("coderd handler is not ready")
	}
	// A host call can run inside a webhook request. Serve it on a fresh
	// context: the router would reuse the chi route context of the outer
	// request, and no other request value may leak into the inner one.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(req.Context(), cancel)
	defer stop()
	req = req.Clone(ctx)
	req.RemoteAddr = "127.0.0.1:0"
	req.RequestURI = req.URL.RequestURI()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result(), nil
}
