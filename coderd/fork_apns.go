package coderd

// This file is fork code: it is the APNs client of the fork-apns-push
// experiment. It signs provider tokens with the deployment's auth key and
// sends alert pushes over HTTP/2.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/quartz"
)

const (
	forkAPNsSandboxURL    = "https://api.sandbox.push.apple.com"
	forkAPNsProductionURL = "https://api.push.apple.com"
	// Apple refuses a provider token refreshed more often than every 20
	// minutes and expires one after 60.
	forkAPNsTokenLifetime = 40 * time.Minute
	// forkAPNsMaxPayload is the size limit of an alert push.
	forkAPNsMaxPayload = 4096
	// forkAPNsSound names the chime that the iOS app bundles.
	forkAPNsSound = "chime.caf"
)

// forkAPNsClient sends pushes to APNs. It is safe for concurrent use.
type forkAPNsClient struct {
	keyID  string
	teamID string
	topic  string
	key    *ecdsa.PrivateKey
	client *http.Client
	clock  quartz.Clock
	// baseURLs are the hosts of each environment. Tests point them at a
	// fake APNs.
	baseURLs map[codersdk.ChatAPNsEnvironment]string

	mu       sync.Mutex
	token    string
	issuedAt time.Time
}

func newForkAPNsClient(keyFile, keyID, teamID, topic string, clock quartz.Clock) (*forkAPNsClient, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, xerrors.Errorf("read APNs key file: %w", err)
	}
	key, err := parseForkAPNsKey(data)
	if err != nil {
		return nil, err
	}
	return &forkAPNsClient{
		keyID:  keyID,
		teamID: teamID,
		topic:  topic,
		key:    key,
		client: &http.Client{},
		clock:  clock,
		baseURLs: map[codersdk.ChatAPNsEnvironment]string{
			codersdk.ChatAPNsEnvironmentSandbox:    forkAPNsSandboxURL,
			codersdk.ChatAPNsEnvironmentProduction: forkAPNsProductionURL,
		},
	}, nil
}

// parseForkAPNsKey reads an APNs auth key: a PEM PKCS #8 P-256 key, as
// Apple's .p8 file holds it.
func parseForkAPNsKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, xerrors.New("APNs key file is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, xerrors.Errorf("APNs key file is not a PKCS #8 key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, xerrors.New("APNs key file is not a P-256 key")
	}
	return key, nil
}

// providerToken returns the cached signed provider token, made again when
// it is older than forkAPNsTokenLifetime.
func (c *forkAPNsClient) providerToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.clock.Now().Sub(c.issuedAt) < forkAPNsTokenLifetime {
		return c.token, nil
	}
	return c.signTokenLocked(c.clock.Now())
}

// refreshProviderToken re-signs the provider token only when stale is the
// one it holds, so pushes that saw the same expired token rotate it once.
// Otherwise it returns the cached token.
func (c *forkAPNsClient) refreshProviderToken(stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != stale {
		return c.token, nil
	}
	return c.signTokenLocked(c.clock.Now())
}

// signTokenLocked signs a provider token and caches it. c.mu must be held.
func (c *forkAPNsClient) signTokenLocked(now time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.teamID,
		"iat": now.Unix(),
	})
	token.Header["kid"] = c.keyID
	signed, err := token.SignedString(c.key)
	if err != nil {
		return "", xerrors.Errorf("sign APNs provider token: %w", err)
	}
	c.token = signed
	c.issuedAt = now
	return signed, nil
}

// forkAPNsPush is one alert push.
type forkAPNsPush struct {
	Title string
	Body  string
	// ChatID groups and collapses the pushes of one chat. Empty for a
	// test push.
	ChatID     string
	Deployment string
	Sound      bool
}

// payload encodes the push. A body too long for APNs's size limit is cut
// to the longest prefix of whole runes that fits with an ellipsis.
func (p forkAPNsPush) payload() ([]byte, error) {
	data, err := p.encode(p.Body)
	if err != nil || len(data) <= forkAPNsMaxPayload {
		return data, err
	}
	// JSON escapes make the encoded length of a prefix uneven, so search
	// the cut instead of computing it.
	runes := []rune(p.Body)
	lo, hi := 0, len(runes)-1
	var fit []byte
	for lo <= hi {
		mid := (lo + hi) / 2
		data, err := p.encode(string(runes[:mid]) + "…")
		if err != nil {
			return nil, err
		}
		if len(data) <= forkAPNsMaxPayload {
			fit, lo = data, mid+1
		} else {
			hi = mid - 1
		}
	}
	if fit == nil {
		return nil, xerrors.New("APNs push is too large without its body")
	}
	return fit, nil
}

func (p forkAPNsPush) encode(body string) ([]byte, error) {
	aps := map[string]any{
		"alert": map[string]string{"title": p.Title, "body": body},
	}
	if p.Sound {
		aps["sound"] = forkAPNsSound
	}
	payload := map[string]any{"aps": aps, "deployment": p.Deployment}
	if p.ChatID != "" {
		aps["thread-id"] = p.ChatID
		payload["link"] = "coder-ios://chat/" + p.ChatID
	}
	return json.Marshal(payload)
}

// forkAPNsResponse is APNs's answer to one push.
type forkAPNsResponse struct {
	Status int
	// Reason is APNs's error reason, such as "BadDeviceToken".
	Reason string
	// Timestamp is when APNs last confirmed the token was invalid, set on
	// a 410.
	Timestamp time.Time
}

func (r forkAPNsResponse) ok() bool { return r.Status == http.StatusOK }

// send sends a push to one device. An error means the push never reached
// APNs; a reached APNs answers with a response, rejected or not. A push
// rejected for an expired token retries once with a freshly signed one.
func (c *forkAPNsClient) send(ctx context.Context, env codersdk.ChatAPNsEnvironment, deviceToken string, push forkAPNsPush) (forkAPNsResponse, error) {
	baseURL, ok := c.baseURLs[env]
	if !ok {
		return forkAPNsResponse{}, xerrors.Errorf("unknown APNs environment %q", env)
	}
	payload, err := push.payload()
	if err != nil {
		return forkAPNsResponse{}, err
	}
	token, err := c.providerToken()
	if err != nil {
		return forkAPNsResponse{}, err
	}
	res, err := c.post(ctx, baseURL, deviceToken, push.ChatID, payload, token)
	if err != nil {
		return forkAPNsResponse{}, err
	}
	if res.Status == http.StatusForbidden && res.Reason == "ExpiredProviderToken" {
		refreshed, err := c.refreshProviderToken(token)
		if err != nil {
			return forkAPNsResponse{}, err
		}
		return c.post(ctx, baseURL, deviceToken, push.ChatID, payload, refreshed)
	}
	return res, nil
}

// post sends the payload with the given provider token.
func (c *forkAPNsClient) post(ctx context.Context, baseURL, deviceToken, collapseID string, payload []byte, token string) (forkAPNsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/3/device/"+deviceToken, bytes.NewReader(payload))
	if err != nil {
		return forkAPNsResponse{}, xerrors.Errorf("build APNs request: %w", err)
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", strconv.FormatInt(c.clock.Now().Add(24*time.Hour).Unix(), 10))
	if collapseID != "" {
		req.Header.Set("apns-collapse-id", collapseID)
	}
	res, err := c.client.Do(req)
	if err != nil {
		// The URL holds the device token, which never goes in a log.
		var urlErr *url.Error
		if xerrors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return forkAPNsResponse{}, xerrors.Errorf("send APNs push: %w", err)
	}
	defer res.Body.Close()
	response := forkAPNsResponse{Status: res.StatusCode}
	if res.StatusCode != http.StatusOK {
		var body struct {
			Reason    string `json:"reason"`
			Timestamp int64  `json:"timestamp"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body)
		response.Reason = body.Reason
		if body.Timestamp > 0 {
			response.Timestamp = time.UnixMilli(body.Timestamp)
		}
	}
	return response, nil
}

// forkAPNsTokenTag names a device token in logs without revealing it: the
// first 8 hex characters of its SHA-256.
func forkAPNsTokenTag(deviceToken string) string {
	sum := sha256.Sum256([]byte(deviceToken))
	return hex.EncodeToString(sum[:4])
}

// otherForkAPNsEnvironment is the environment a token was not reported in.
func otherForkAPNsEnvironment(env codersdk.ChatAPNsEnvironment) codersdk.ChatAPNsEnvironment {
	if env == codersdk.ChatAPNsEnvironmentProduction {
		return codersdk.ChatAPNsEnvironmentSandbox
	}
	return codersdk.ChatAPNsEnvironmentProduction
}

func (r forkAPNsResponse) String() string {
	if r.Reason == "" {
		return strconv.Itoa(r.Status)
	}
	return fmt.Sprintf("%d %s", r.Status, r.Reason)
}
