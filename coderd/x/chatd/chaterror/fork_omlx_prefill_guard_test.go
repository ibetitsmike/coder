package chaterror_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/x/chatd/chaterror"
	"github.com/coder/coder/v2/codersdk"
)

// omlxPrefillGuardMessage is the rejection text from chat
// db6d878a-10ea-411f-94a5-ffd76d626115 (ibetitsmike/coder#164).
const omlxPrefillGuardMessage = "oMLX prefill memory guard rejected this prompt: " +
	"Prefill would require ~207.67 GB peak (current 193.19 GB + KV+SDPA 14.47 GB) " +
	"but the prefill limit is 206.20 GB (dynamic ceiling 224.13 GB - safety margin 17.93 GB). " +
	"To continue, set Memory Guard to aggressive, raise the custom memory guard ceiling, " +
	"free system memory, or compact/reduce context."

func TestClassifyOMLXPrefillGuard(t *testing.T) {
	t.Parallel()

	preflightBody, err := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"message":   omlxPrefillGuardMessage,
			"type":      "invalid_request_error",
			"code":      "prefill_memory_exceeded",
			"omlx_code": "prefill_memory_exceeded",
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name          string
		err           error
		wantKind      codersdk.ChatErrorKind
		wantRetryable bool
		wantStatus    int
	}{
		{
			name: "PreflightHTTP400IsRetryable",
			err: &fantasy.ProviderError{
				Title:        fantasy.ErrorTitleForStatusCode(http.StatusBadRequest),
				Message:      omlxPrefillGuardMessage,
				StatusCode:   http.StatusBadRequest,
				ResponseBody: testProviderResponseDump(string(preflightBody)),
			},
			wantKind:      codersdk.ChatErrorKindOverloaded,
			wantRetryable: true,
			wantStatus:    http.StatusBadRequest,
		},
		{
			name:          "InStreamErrorEventIsRetryable",
			err:           testStreamError(t, `{"message": "`+omlxPrefillGuardMessage+`"}`),
			wantKind:      codersdk.ChatErrorKindOverloaded,
			wantRetryable: true,
			wantStatus:    0,
		},
		{
			name: "OtherHTTP400StaysNonRetryable",
			err: &fantasy.ProviderError{
				Title:      fantasy.ErrorTitleForStatusCode(http.StatusBadRequest),
				Message:    "messages: at least one message is required",
				StatusCode: http.StatusBadRequest,
			},
			wantKind:      codersdk.ChatErrorKindGeneric,
			wantRetryable: false,
			wantStatus:    http.StatusBadRequest,
		},
		{
			name: "ContextLengthHTTP400StaysNonRetryable",
			err: &fantasy.ProviderError{
				Title:      fantasy.ErrorTitleForStatusCode(http.StatusBadRequest),
				Message:    "This model's maximum context length is 131072 tokens. Please reduce the length of the messages.",
				StatusCode: http.StatusBadRequest,
			},
			wantKind:      codersdk.ChatErrorKindConfig,
			wantRetryable: false,
			wantStatus:    http.StatusBadRequest,
		},
		{
			name:          "OtherInStreamErrorEventStaysNonRetryable",
			err:           testStreamError(t, `{"message": "invalid tool call arguments for function read_file"}`),
			wantKind:      codersdk.ChatErrorKindGeneric,
			wantRetryable: false,
			wantStatus:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			classified := chaterror.Classify(tt.err)
			require.Equal(t, tt.wantKind, classified.Kind)
			require.Equal(t, tt.wantRetryable, classified.Retryable)
			require.Equal(t, tt.wantStatus, classified.StatusCode)
		})
	}
}

// testStreamError builds the error chatd receives when an OpenAI-compatible
// stream carries an error event relayed through aibridge: fantasy turns the
// SSE event into a status-less ProviderError whose body is the relayed
// envelope.
func testStreamError(t *testing.T, upstreamPayload string) error {
	t.Helper()
	message := "unknown stream error: received error while streaming: " + upstreamPayload
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "error",
		},
	})
	require.NoError(t, err)
	return &fantasy.ProviderError{
		Title:        "stream error",
		Message:      message,
		ResponseBody: body,
		ErrorType:    "error",
	}
}
