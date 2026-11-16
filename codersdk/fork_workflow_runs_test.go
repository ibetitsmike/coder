package codersdk

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkflowRunDetailUnmarshalExactNumbers pins that Args and Result keep
// a number outside the float64 exact range through an SDK round trip, and
// that an absent result key stays nil instead of becoming a JSON null.
func TestWorkflowRunDetailUnmarshalExactNumbers(t *testing.T) {
	t.Parallel()

	const body = `{
		"id": "497f6eca-6276-4993-bfeb-53cbbbba6f08",
		"chat_id": "efc9fe20-a1e5-4a8c-9c48-f1b30c1e4f86",
		"status": "completed",
		"error": "",
		"created_at": "2019-08-24T14:15:22Z",
		"updated_at": "2019-08-24T14:15:22Z",
		"title": "",
		"script": "def main(args):\n    return args\n",
		"args": {"big": 9007199254740993},
		"result": {"big": 9007199254740993},
		"logs": [],
		"phases": [],
		"current_phase": "",
		"steps": []
	}`
	var detail WorkflowRunDetail
	require.NoError(t, json.Unmarshal([]byte(body), &detail))

	// float64 rounds 9007199254740993 to 9007199254740992, so the raw
	// bytes must survive for the value to be exact.
	for _, name := range []string{"Args", "Result"} {
		raw, ok := fieldValue(detail, name).(json.RawMessage)
		require.True(t, ok, "%s must carry the raw JSON", name)
		assert.Contains(t, string(raw), "9007199254740993",
			"%s must keep the big integer exactly", name)
		assert.NotContains(t, string(raw), "9007199254740992",
			"%s must not round the big integer", name)
	}

	var absent WorkflowRunDetail
	withoutResult := `{"id":"497f6eca-6276-4993-bfeb-53cbbbba6f08","chat_id":"efc9fe20-a1e5-4a8c-9c48-f1b30c1e4f86","status":"failed","error":"boom","created_at":"2019-08-24T14:15:22Z","updated_at":"2019-08-24T14:15:22Z","title":"","script":"","args":{},"logs":[],"phases":[],"current_phase":"","steps":[]}`
	require.NoError(t, json.Unmarshal([]byte(withoutResult), &absent))
	assert.True(t, absent.Result == nil, "an absent result key must stay nil")
	// Re-marshal: a nil Result must not reappear as a key. A JSON null
	// value would serialize as "result":null, which a client could tell
	// from an absent key.
	remarshaled, err := json.Marshal(absent)
	require.NoError(t, err)
	assert.NotContains(t, string(remarshaled), "\"result\"",
		"an absent result key must not reappear as null")
}

// TestWorkflowRunDetailUnmarshalArgsAbsent pins that an absent args key
// keeps Args nil instead of becoming a JSON null.
func TestWorkflowRunDetailUnmarshalArgsAbsent(t *testing.T) {
	t.Parallel()

	var absent WorkflowRunDetail
	withoutArgs := `{"id":"497f6eca-6276-4993-bfeb-53cbbbba6f08","chat_id":"efc9fe20-a1e5-4a8c-9c48-f1b30c1e4f86","status":"completed","error":"","created_at":"2019-08-24T14:15:22Z","updated_at":"2019-08-24T14:15:22Z","title":"","script":"","logs":[],"phases":[],"current_phase":"","steps":[]}`
	require.NoError(t, json.Unmarshal([]byte(withoutArgs), &absent))
	assert.True(t, absent.Args == nil, "an absent args key must stay nil")
}

func fieldValue(detail WorkflowRunDetail, name string) any {
	switch name {
	case "Args":
		return detail.Args
	case "Result":
		return detail.Result
	}
	return nil
}
