package plugins

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
)

// RelaySourceHeader carries the ID of the replica that forwarded a webhook.
const RelaySourceHeader = "X-Coder-Plugin-Relay-Source-Replica"

// webhookForwardTimeout bounds a webhook that another replica handles.
const webhookForwardTimeout = 30 * time.Second

// hopHeaders are the hop-by-hop headers of RFC 9110, which a proxy must not
// forward.
var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// forwardWebhook sends a webhook to the replica that holds the lease of the
// plugin and returns that replica's response. A replica it can't reach
// gives ErrNotRunningHere, so the sender retries.
func (m *Manager) forwardWebhook(ctx context.Context, p database.Plugin, req *http.Request) (*WebhookResponse, error) {
	body, err := readWebhookBody(req)
	if err != nil {
		return nil, err
	}
	holder := p.RunnerID.UUID
	logger := m.logger.With(slog.F("plugin_id", p.ID), slog.F("replica_id", holder))
	address, ok := m.opts.ReplicaAddress(ctx, holder)
	if !ok {
		logger.Warn(ctx, "no address for the replica that runs the plugin")
		return nil, ErrNotRunningHere
	}
	target, err := url.Parse(address)
	if err != nil {
		logger.Warn(ctx, "parse replica address", slog.F("address", address), slog.Error(err))
		return nil, ErrNotRunningHere
	}
	target.Path, target.RawPath, target.RawQuery = req.URL.Path, req.URL.RawPath, req.URL.RawQuery

	ctx, cancel := context.WithTimeout(ctx, webhookForwardTimeout)
	defer cancel()
	out, err := http.NewRequestWithContext(ctx, req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, xerrors.Errorf("build forwarded webhook: %w", err)
	}
	out.Header = req.Header.Clone()
	for _, v := range out.Header.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			out.Header.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		out.Header.Del(name)
	}
	out.Header.Set(RelaySourceHeader, m.opts.ReplicaID.String())

	res, err := m.opts.ReplicaHTTPClient.Do(out)
	if err != nil {
		logger.Warn(ctx, "forward plugin webhook", slog.Error(err))
		return nil, ErrNotRunningHere
	}
	defer res.Body.Close()
	resBody, err := io.ReadAll(io.LimitReader(res.Body, maxWebhookBodyBytes+1))
	if err != nil {
		logger.Warn(ctx, "read forwarded webhook response", slog.Error(err))
		return nil, ErrNotRunningHere
	}
	if len(resBody) > maxWebhookBodyBytes {
		return nil, xerrors.New("forwarded webhook response is too large")
	}
	return &WebhookResponse{StatusCode: res.StatusCode, Header: res.Header, Body: resBody}, nil
}

// readWebhookBody reads the body of a webhook, up to maxWebhookBodyBytes.
func readWebhookBody(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(req.Body, maxWebhookBodyBytes+1))
	if err != nil {
		return nil, xerrors.Errorf("read webhook body: %w", err)
	}
	if len(body) > maxWebhookBodyBytes {
		return nil, xerrors.New("webhook body is too large")
	}
	return body, nil
}
