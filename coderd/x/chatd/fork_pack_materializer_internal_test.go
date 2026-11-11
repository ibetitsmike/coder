package chatd

// This file is fork code: it tests writing enabled plugin packs into a chat's
// workspace.

import (
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestPackResourcePath(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"plugin.json", ".claude-plugin/plugin.json", "skills/review/SKILL.md"} {
		got, valid := packResourcePath(ok)
		require.True(t, valid, ok)
		require.Equal(t, ok, got)
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/../../x", "a/./b", "/etc/passwd", `a\b`, "C:x", packCommitMarker} {
		_, valid := packResourcePath(bad)
		require.False(t, valid, bad)
	}
}

// fakeWorkspaceFiles is an in-memory workspace file system behind a mock
// agent connection. writes records each written path in order.
type fakeWorkspaceFiles struct {
	mu     sync.Mutex
	files  map[string]string
	writes []string
}

func (f *fakeWorkspaceFiles) conn(t *testing.T) workspacesdk.AgentConn {
	conn := agentconnmock.NewMockAgentConn(gomock.NewController(t))
	conn.EXPECT().LS(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(workspacesdk.LSResponse{AbsolutePathString: "/home/coder"}, nil).AnyTimes()
	conn.EXPECT().ReadFile(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, path string, _, _ int64) (io.ReadCloser, string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			content, ok := f.files[path]
			if !ok {
				return nil, "", os.ErrNotExist
			}
			return io.NopCloser(strings.NewReader(content)), "", nil
		}).AnyTimes()
	conn.EXPECT().WriteFile(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, path string, reader io.Reader) error {
			content, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.files[path] = string(content)
			f.writes = append(f.writes, path)
			return nil
		}).AnyTimes()
	return conn
}

func TestMaterializePluginPacks(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	// The server reads through dbauthz, so the test covers the subjects
	// that read enablement and pack content.
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkPluginPacks}))
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)

	insertPack := func(name string, paths ...string) forkdb.PluginPack {
		now := dbtime.Now()
		pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
			ID: uuid.New(), OrganizationID: org.ID, Name: name, SourceUrl: "https://example.com/packs.git",
			Ref: "main", CommitSha: "0123456789abcdef0123456789abcdef01234567", CreatedBy: user.ID,
			CreatedAt: now, UpdatedAt: now,
		})
		require.NoError(t, err)
		contents := make([][]byte, len(paths))
		hashes := make([]string, len(paths))
		for i, path := range paths {
			contents[i] = []byte("content of " + path)
			hashes[i] = "hash"
		}
		require.NoError(t, fdb.InsertPluginPackResources(ctx, forkdb.InsertPluginPackResourcesParams{
			PackID: pack.ID, Paths: paths, Contents: contents, ContentHashes: hashes,
		}))
		return pack
	}
	newChat := func(packs ...forkdb.PluginPack) database.Chat {
		chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})
		for _, pack := range packs {
			_, err := fdb.InsertChatPluginPack(ctx, forkdb.InsertChatPluginPackParams{ChatID: chat.ID, PackID: pack.ID})
			require.NoError(t, err)
		}
		return chat
	}

	t.Run("WritesOncePerAgentAndCommit", func(t *testing.T) {
		t.Parallel()
		pack := insertPack("demo", ".claude-plugin/plugin.json", "skills/review/SKILL.md")
		chat := newChat(pack)
		files := &fakeWorkspaceFiles{files: map[string]string{}}
		dials := 0
		dial := func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			dials++
			return files.conn(t), func() {}, nil
		}

		agentID := uuid.New()
		server.materializePluginPacks(ctx, server.logger, chat, agentID, dial)
		require.Equal(t, []string{
			"/home/coder/.coder/plugins/demo/.claude-plugin/plugin.json",
			"/home/coder/.coder/plugins/demo/skills/review/SKILL.md",
			"/home/coder/.coder/plugins/demo/" + packCommitMarker,
		}, files.writes, "the commit marker is written after every pack file")
		require.Equal(t, "content of skills/review/SKILL.md", files.files["/home/coder/.coder/plugins/demo/skills/review/SKILL.md"])
		require.Equal(t, pack.ID.String()+" "+pack.CommitSha, files.files["/home/coder/.coder/plugins/demo/"+packCommitMarker],
			"the marker names the pack, so a pack reinstalled under the same name is written again")

		server.materializePluginPacks(ctx, server.logger, chat, agentID, dial)
		require.Equal(t, 1, dials, "a pack written to this agent at this commit needs no connection")

		// A new agent, as after a coderd restart, reads the marker and
		// skips the write.
		files.writes = nil
		server.materializePluginPacks(ctx, server.logger, chat, uuid.New(), dial)
		require.Equal(t, 2, dials)
		require.Empty(t, files.writes, "a matching marker skips the write")
	})

	t.Run("RepinsOnFirstDirtyTurnAfterWrite", func(t *testing.T) {
		t.Parallel()
		pack := insertPack("repin", "plugin.json")
		chat := newChat(pack)
		require.NoError(t, db.SetChatContextSnapshot(ctx, database.SetChatContextSnapshotParams{
			ID: chat.ID, AggregateHash: []byte("pre-pack snapshot"),
		}))
		pinnedHash := func() []byte {
			got, err := db.GetChatByID(ctx, chat.ID)
			require.NoError(t, err)
			return got.ContextAggregateHash
		}
		files := &fakeWorkspaceFiles{files: map[string]string{}}
		dial := func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			return files.conn(t), func() {}, nil
		}

		agentID := uuid.New()
		server.materializePluginPacks(ctx, server.logger, chat, agentID, dial)
		server.materializePluginPacks(ctx, server.logger, chat, agentID, dial)
		require.Equal(t, []byte("pre-pack snapshot"), pinnedHash(), "a chat the agent has not marked dirty keeps its pin")

		// The agent's push of the written files marks the chat dirty, and
		// the next turn re-pins it. The chat has no agent, so the re-pin
		// clears the pinned hash.
		chat.ContextDirtySince = sql.NullTime{Time: dbtime.Now(), Valid: true}
		server.materializePluginPacks(ctx, server.logger, chat, agentID, dial)
		require.Nil(t, pinnedHash(), "the first dirty turn after a write re-pins the chat")

		require.NoError(t, db.SetChatContextSnapshot(ctx, database.SetChatContextSnapshotParams{
			ID: chat.ID, AggregateHash: []byte("later snapshot"),
		}))
		server.materializePluginPacks(ctx, server.logger, chat, agentID, dial)
		require.Equal(t, []byte("later snapshot"), pinnedHash(), "the chat is re-pinned once per write")
	})

	t.Run("UnsafePathWritesNoMarker", func(t *testing.T) {
		t.Parallel()
		pack := insertPack("unsafe", "plugin.json", "skills/../../escape")
		chat := newChat(pack)
		files := &fakeWorkspaceFiles{files: map[string]string{}}
		dial := func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			return files.conn(t), func() {}, nil
		}

		server.materializePluginPacks(ctx, server.logger, chat, uuid.New(), dial)
		require.Equal(t, []string{"/home/coder/.coder/plugins/unsafe/plugin.json"}, files.writes,
			"the unsafe path and the marker are not written")
	})

	t.Run("NothingEnabled", func(t *testing.T) {
		t.Parallel()
		chat := newChat()
		dial := func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			t.Fatal("a chat with no enabled packs must not connect")
			return nil, nil, nil
		}
		server.materializePluginPacks(ctx, server.logger, chat, uuid.New(), dial)
	})
}
