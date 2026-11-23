package coderd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// fakeAPNs is an HTTP/2 APNs that records each push and answers with the
// next queued response, else 200.
type fakeAPNs struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	requests  []fakeAPNsRequest
	responses []fakeAPNsAnswer
}

type fakeAPNsRequest struct {
	Path    string
	Proto   int
	Header  http.Header
	Payload map[string]any
}

type fakeAPNsAnswer struct {
	Status int
	Reason string
	// Timestamp becomes the 410 body timestamp when not zero.
	Timestamp time.Time
}

func newFakeAPNs(t *testing.T) *fakeAPNs {
	f := &fakeAPNs{t: t}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.srv.EnableHTTP2 = true
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPNs) serve(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if !(assertNoError(f.t, err)) {
		return
	}
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	f.mu.Lock()
	f.requests = append(f.requests, fakeAPNsRequest{Path: r.URL.Path, Proto: r.ProtoMajor, Header: r.Header.Clone(), Payload: payload})
	answer := fakeAPNsAnswer{Status: http.StatusOK}
	if len(f.responses) > 0 {
		answer = f.responses[0]
		f.responses = f.responses[1:]
	}
	f.mu.Unlock()
	if answer.Status != http.StatusOK {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(answer.Status)
		body := map[string]any{"reason": answer.Reason}
		if !answer.Timestamp.IsZero() {
			body["timestamp"] = answer.Timestamp.UnixMilli()
		}
		_ = json.NewEncoder(rw).Encode(body)
		return
	}
	rw.WriteHeader(http.StatusOK)
}

func assertNoError(t *testing.T, err error) bool {
	if err != nil {
		t.Errorf("fake APNs: %v", err)
		return false
	}
	return true
}

func (f *fakeAPNs) answer(answers ...fakeAPNsAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, answers...)
}

func (f *fakeAPNs) received() []fakeAPNsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeAPNsRequest(nil), f.requests...)
}

func writeTestAPNsKey(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "AuthKey_TESTKEY123.p8")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	return path, key
}

// newTestForkAPNsClient returns a client of fake, which serves sandbox
// under /sandbox and production under /production.
func newTestForkAPNsClient(t *testing.T, fake *fakeAPNs, clock quartz.Clock) (*forkAPNsClient, *ecdsa.PrivateKey) {
	t.Helper()
	path, key := writeTestAPNsKey(t)
	client, err := newForkAPNsClient(path, "TESTKEY123", "TEAM123456", "com.coder.ios", clock)
	require.NoError(t, err)
	client.client = fake.srv.Client()
	client.baseURLs = map[codersdk.ChatAPNsEnvironment]string{
		codersdk.ChatAPNsEnvironmentSandbox:    fake.srv.URL + "/sandbox",
		codersdk.ChatAPNsEnvironmentProduction: fake.srv.URL + "/production",
	}
	return client, key
}

const testAPNsToken = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

func TestForkChatAPNsClientSendsAnAlert(t *testing.T) {
	t.Parallel()
	fake := newFakeAPNs(t)
	clock := quartz.NewMock(t)
	client, key := newTestForkAPNsClient(t, fake, clock)

	res, err := client.send(testutil.Context(t, testutil.WaitShort), codersdk.ChatAPNsEnvironmentSandbox, testAPNsToken, forkAPNsPush{
		Title:      "Fix the build",
		Body:       "Done: the build passes",
		ChatID:     "6f1f3a52-4d5e-4b0e-9a49-4e2b8a6f2b11",
		Deployment: "https://coder.example.com",
		Sound:      true,
	})
	require.NoError(t, err)
	require.True(t, res.ok())

	got := fake.received()
	require.Len(t, got, 1)
	req := got[0]
	require.Equal(t, 2, req.Proto, "APNs needs HTTP/2")
	require.Equal(t, "/sandbox/3/device/"+testAPNsToken, req.Path)
	require.Equal(t, "com.coder.ios", req.Header.Get("apns-topic"))
	require.Equal(t, "alert", req.Header.Get("apns-push-type"))
	require.Equal(t, "10", req.Header.Get("apns-priority"))
	require.Equal(t, "6f1f3a52-4d5e-4b0e-9a49-4e2b8a6f2b11", req.Header.Get("apns-collapse-id"))
	require.Equal(t, strconv.FormatInt(clock.Now().Add(24*time.Hour).Unix(), 10), req.Header.Get("apns-expiration"))
	require.Equal(t, map[string]any{
		"aps": map[string]any{
			"alert":     map[string]any{"title": "Fix the build", "body": "Done: the build passes"},
			"sound":     "chime.caf",
			"thread-id": "6f1f3a52-4d5e-4b0e-9a49-4e2b8a6f2b11",
		},
		"link":       "coder-ios://chat/6f1f3a52-4d5e-4b0e-9a49-4e2b8a6f2b11",
		"deployment": "https://coder.example.com",
	}, req.Payload)

	bearer, ok := strings.CutPrefix(req.Header.Get("Authorization"), "bearer ")
	require.True(t, ok)
	parsed, err := jwt.Parse(bearer, func(token *jwt.Token) (any, error) {
		require.Equal(t, jwt.SigningMethodES256, token.Method)
		return &key.PublicKey, nil
	}, jwt.WithoutClaimsValidation())
	require.NoError(t, err)
	require.Equal(t, "TESTKEY123", parsed.Header["kid"])
	claims := parsed.Claims.(jwt.MapClaims)
	require.Equal(t, "TEAM123456", claims["iss"])
	require.EqualValues(t, clock.Now().Unix(), claims["iat"])
}

func TestForkChatAPNsClientWithoutSoundOrChat(t *testing.T) {
	t.Parallel()
	fake := newFakeAPNs(t)
	client, _ := newTestForkAPNsClient(t, fake, quartz.NewMock(t))

	_, err := client.send(testutil.Context(t, testutil.WaitShort), codersdk.ChatAPNsEnvironmentProduction, testAPNsToken, forkAPNsPush{
		Title:      "It's working!",
		Body:       "You've subscribed to push notifications.",
		Deployment: "https://coder.example.com",
	})
	require.NoError(t, err)
	req := fake.received()[0]
	require.Equal(t, "/production/3/device/"+testAPNsToken, req.Path)
	require.Empty(t, req.Header.Get("apns-collapse-id"))
	require.Equal(t, map[string]any{
		"aps":        map[string]any{"alert": map[string]any{"title": "It's working!", "body": "You've subscribed to push notifications."}},
		"deployment": "https://coder.example.com",
	}, req.Payload)
}

func TestForkChatAPNsClientProviderToken(t *testing.T) {
	t.Parallel()
	fake := newFakeAPNs(t)
	clock := quartz.NewMock(t)
	client, _ := newTestForkAPNsClient(t, fake, clock)
	ctx := testutil.Context(t, testutil.WaitShort)
	send := func() {
		t.Helper()
		_, err := client.send(ctx, codersdk.ChatAPNsEnvironmentSandbox, testAPNsToken, forkAPNsPush{Title: "t", Body: "b"})
		require.NoError(t, err)
	}
	bearer := func(i int) string { return fake.received()[i].Header.Get("Authorization") }

	send()
	clock.Advance(39 * time.Minute)
	send()
	require.Equal(t, bearer(0), bearer(1), "a token is reused for 40 minutes")

	clock.Advance(time.Minute)
	send()
	require.NotEqual(t, bearer(1), bearer(2), "an older token is made again")

	// An expired token is made again and the push sent once more.
	fake.answer(fakeAPNsAnswer{Status: http.StatusForbidden, Reason: "ExpiredProviderToken"})
	clock.Advance(time.Second)
	send()
	got := fake.received()
	require.Len(t, got, 5)
	require.Equal(t, bearer(2), bearer(3))
	require.NotEqual(t, bearer(3), bearer(4))
}

// TestForkChatAPNsClientRefreshesAnExpiredTokenOnce coalesces refreshes:
// callers that saw the same stale token share one re-signed token.
func TestForkChatAPNsClientRefreshesAnExpiredTokenOnce(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	client, _ := newTestForkAPNsClient(t, newFakeAPNs(t), clock)

	stale, err := client.providerToken()
	require.NoError(t, err)
	// iat holds seconds, so the mock clock moves before a re-signing.
	clock.Advance(time.Second)

	first, err := client.refreshProviderToken(stale)
	require.NoError(t, err)
	require.NotEqual(t, stale, first, "the stale token is re-signed")
	second, err := client.refreshProviderToken(stale)
	require.NoError(t, err)
	require.Equal(t, first, second, "a second caller of the same stale token gets the refreshed one")

	other, err := client.refreshProviderToken("not the cached token")
	require.NoError(t, err)
	require.Equal(t, first, other, "a token that is not the cached one is not re-signed")
}

func TestForkChatAPNsClientRejections(t *testing.T) {
	t.Parallel()
	fake := newFakeAPNs(t)
	client, _ := newTestForkAPNsClient(t, fake, quartz.NewMock(t))
	ctx := testutil.Context(t, testutil.WaitShort)

	for _, answer := range []fakeAPNsAnswer{
		{Status: http.StatusGone, Reason: "Unregistered"},
		{Status: http.StatusTooManyRequests, Reason: "TooManyRequests"},
		{Status: http.StatusInternalServerError, Reason: "InternalServerError"},
	} {
		fake.answer(answer)
		res, err := client.send(ctx, codersdk.ChatAPNsEnvironmentSandbox, testAPNsToken, forkAPNsPush{Title: "t", Body: "b"})
		require.NoError(t, err)
		require.Equal(t, forkAPNsResponse{Status: answer.Status, Reason: answer.Reason}, res)
	}
}

// TestForkChatAPNsClientUnregisteredTimestamp checks that the 410 body
// timestamp lands on the response as a time.Time.
func TestForkChatAPNsClientUnregisteredTimestamp(t *testing.T) {
	t.Parallel()
	fake := newFakeAPNs(t)
	client, _ := newTestForkAPNsClient(t, fake, quartz.NewMock(t))

	want := time.UnixMilli(1700000000000)
	fake.answer(fakeAPNsAnswer{Status: http.StatusGone, Reason: "Unregistered", Timestamp: want})
	res, err := client.send(testutil.Context(t, testutil.WaitShort), codersdk.ChatAPNsEnvironmentSandbox, testAPNsToken, forkAPNsPush{Title: "t", Body: "b"})
	require.NoError(t, err)
	require.Equal(t, http.StatusGone, res.Status)
	require.Equal(t, "Unregistered", res.Reason)
	require.True(t, res.Timestamp.Equal(want), "the 410 carries the timestamp APNs sent")
}

func TestForkChatAPNsClientNeverLogsTheToken(t *testing.T) {
	t.Parallel()
	client, _ := newTestForkAPNsClient(t, newFakeAPNs(t), quartz.NewMock(t))
	client.baseURLs[codersdk.ChatAPNsEnvironmentSandbox] = "https://127.0.0.1:1"
	_, err := client.send(testutil.Context(t, testutil.WaitShort), codersdk.ChatAPNsEnvironmentSandbox, testAPNsToken, forkAPNsPush{Title: "t", Body: "b"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), testAPNsToken)
	require.Len(t, forkAPNsTokenTag(testAPNsToken), 8)
	require.NotContains(t, testAPNsToken, forkAPNsTokenTag(testAPNsToken))
}

func TestForkChatAPNsPayloadFitsTheLimit(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		strings.Repeat("a", 5000),
		strings.Repeat("é", 3000),
		strings.Repeat("\"", 3000),
		strings.Repeat("😀", 2000),
	} {
		payload, err := forkAPNsPush{Title: "Title", Body: body, ChatID: "c", Deployment: "https://x"}.payload()
		require.NoError(t, err)
		require.LessOrEqual(t, len(payload), forkAPNsMaxPayload)
		require.Greater(t, len(payload), forkAPNsMaxPayload-64, "the cut keeps as much as fits")
		var decoded struct {
			APS struct {
				Alert struct {
					Body string `json:"body"`
				} `json:"alert"`
			} `json:"aps"`
		}
		require.NoError(t, json.Unmarshal(payload, &decoded))
		require.True(t, strings.HasSuffix(decoded.APS.Alert.Body, "…"))
		require.True(t, strings.HasPrefix(body, strings.TrimSuffix(decoded.APS.Alert.Body, "…")))
	}

	short, err := forkAPNsPush{Title: "Title", Body: "short"}.payload()
	require.NoError(t, err)
	require.Contains(t, string(short), `"body":"short"`)
}

func TestForkChatAPNsKeyFile(t *testing.T) {
	t.Parallel()

	path, _ := writeTestAPNsKey(t)
	_, err := newForkAPNsClient(path, "k", "t", "com.coder.ios", quartz.NewReal())
	require.NoError(t, err)

	_, err = newForkAPNsClient(filepath.Join(t.TempDir(), "missing.p8"), "k", "t", "com.coder.ios", quartz.NewReal())
	require.ErrorContains(t, err, "read APNs key file")

	_, err = parseForkAPNsKey([]byte("not a key"))
	require.ErrorContains(t, err, "not PEM")

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	require.NoError(t, err)
	_, err = parseForkAPNsKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	require.ErrorContains(t, err, "not a P-256 key")

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	der, err = x509.MarshalPKCS8PrivateKey(p384)
	require.NoError(t, err)
	_, err = parseForkAPNsKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	require.ErrorContains(t, err, "not a P-256 key")
}
