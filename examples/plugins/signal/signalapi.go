package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
)

// account is one item of GET /v1/accounts. Releases of signal-cli-rest-api
// up to 0.101 list phone numbers; its main branch lists objects that also
// carry the ACI.
type account struct {
	Number string
	ACI    string
}

func (a *account) UnmarshalJSON(data []byte) error {
	var number string
	if err := json.Unmarshal(data, &number); err == nil {
		*a = account{Number: number}
		return nil
	}
	var v struct {
		Number string  `json:"number"`
		ACI    *string `json:"aci"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*a = account{Number: v.Number}
	if v.ACI != nil {
		a.ACI = *v.ACI
	}
	return nil
}

// id is what the account endpoints accept: the phone number, or the ACI of
// an account without one.
func (a account) id() string {
	if a.Number != "" {
		return a.Number
	}
	return a.ACI
}

// receivedMessage is one item of GET /v1/receive/{account}.
type receivedMessage struct {
	Envelope struct {
		SyncMessage *struct {
			SentMessage *sentMessage `json:"sentMessage"`
		} `json:"syncMessage"`
	} `json:"envelope"`
}

// sentMessage is a message that another device of the account sent.
type sentMessage struct {
	// Destination is deprecated; older signal-cli versions set only it.
	Destination       string          `json:"destination"`
	DestinationNumber string          `json:"destinationNumber"`
	DestinationUUID   string          `json:"destinationUuid"`
	Message           string          `json:"message"`
	GroupInfo         json.RawMessage `json:"groupInfo"`
}

// toSelf reports whether the account sent the message to its own Note to
// Self chat.
func (m sentMessage) toSelf(a account) bool {
	if len(m.GroupInfo) > 0 {
		return false
	}
	for _, dest := range []string{m.DestinationNumber, m.DestinationUUID, m.Destination} {
		if dest != "" && (dest == a.Number || dest == a.ACI) {
			return true
		}
	}
	return false
}

type sendRequest struct {
	Message    string   `json:"message"`
	Number     string   `json:"number"`
	Recipients []string `json:"recipients"`
}

// signalAPI calls a signal-cli-rest-api server through the plugin host.
type signalAPI struct {
	h       pluginsdk.Host
	baseURL string
}

func (s signalAPI) accounts() ([]account, error) {
	var out []account
	if err := s.h.GetJSON(s.baseURL+"/v1/accounts", &out); err != nil {
		return nil, xerrors.Errorf("list accounts: %w", err)
	}
	return out, nil
}

// receive drains the messages that the server queued for an account. The
// plugin never needs attachments, stories, avatars or stickers, and skipping
// them keeps the data of contacts off the server.
func (s signalAPI) receive(id string) ([]receivedMessage, error) {
	var out []receivedMessage
	err := s.h.GetJSON(fmt.Sprintf("%s/v1/receive/%s?timeout=1&ignore_attachments=true"+
		"&ignore_stories=true&ignore_avatars=true&ignore_stickers=true",
		s.baseURL, url.PathEscape(id)), &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sendToSelf writes text to Note to Self of an account, after replyMarker.
func (s signalAPI) sendToSelf(id, text string) error {
	return s.h.PostJSON(s.baseURL+"/v2/send", sendRequest{
		Message:    replyMarker + text,
		Number:     id,
		Recipients: []string{id},
	}, nil)
}

// linkQRCode starts to link a new device to an account and returns the QR
// code that the owner of the account scans, as a PNG image.
func (s signalAPI) linkQRCode(deviceName string) ([]byte, error) {
	res, err := s.h.Fetch(pluginsdk.HTTPRequest{
		Method: http.MethodGet,
		URL:    s.baseURL + "/v1/qrcodelink?device_name=" + url.QueryEscape(deviceName),
		Header: http.Header{"Accept": {"image/png"}},
	})
	if err != nil {
		return nil, xerrors.Errorf("get link QR code: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		body := res.Body
		if len(body) > 512 {
			body = body[:512]
		}
		return nil, xerrors.Errorf("get link QR code: unexpected status %d: %s", res.StatusCode, bytes.TrimSpace(body))
	}
	return res.Body, nil
}

func trimBaseURL(raw string) string {
	return strings.TrimSuffix(raw, "/")
}
