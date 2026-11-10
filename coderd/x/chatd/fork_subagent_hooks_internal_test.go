package chatd

// This file is fork code: it asserts that hook events of a nested
// subagent spawn carry the direct parent and the family root behind
// fork-nested-subagents, so a depth-2 spawn's payload names its parent,
// not the root.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/x/agenthooks/dispatch"
	"github.com/coder/coder/v2/coderd/x/chatd/chathooks"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/agenthooks"
)

// TestNestedSubagentHookPayload spawns a depth-2 family through the real
// spawn path and asserts the user_prompt_submit hook payload of each
// spawned chat: the child names the root as its parent, and the
// grandchild names the child as its parent while both name the root as
// the family root.
func TestNestedSubagentHookPayload(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t,
		codersdk.Experiments{
			codersdk.ExperimentAgentLifecycleHooks,
			codersdk.ExperimentForkNestedSubagents,
		},
		Limits{})

	var mu sync.Mutex
	var requests []agenthooks.Request
	consumer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request agenthooks.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(consumer.Close)
	f.server.hooks = chathooks.NewTrigger(dispatch.New(
		slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		consumer.Client(),
		consumer.URL,
		false,
		"test-hook-secret-32-bytes-minimum!!",
		time.Second,
		"test-deployment",
		"test-version",
		prometheus.NewRegistry(),
	))

	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, requests, "the spawns must dispatch hook events")
	refByChatID := make(map[uuid.UUID]agenthooks.ChatRef, len(requests))
	for _, request := range requests {
		refByChatID[request.Meta.ChatID] = request.Meta.ChatRef
	}

	require.Contains(t, refByChatID, child.ID)
	require.NotNil(t, refByChatID[child.ID].ParentChatID)
	require.Equal(t, f.root.ID, *refByChatID[child.ID].ParentChatID)
	require.NotNil(t, refByChatID[child.ID].RootChatID)
	require.Equal(t, f.root.ID, *refByChatID[child.ID].RootChatID)

	require.Contains(t, refByChatID, grandchild.ID)
	require.NotNil(t, refByChatID[grandchild.ID].ParentChatID)
	require.Equal(t, child.ID, *refByChatID[grandchild.ID].ParentChatID,
		"the grandchild's hook payload names the child as its parent")
	require.NotNil(t, refByChatID[grandchild.ID].RootChatID)
	require.Equal(t, f.root.ID, *refByChatID[grandchild.ID].RootChatID,
		"the grandchild's hook payload names the family root")
}
