package chatd

// This file is fork code: it checks that packProjectID, which pack
// enablement uses, resolves the project that the upstream
// resolveUsableChatProjectID resolves, so upstream drift fails a test.

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
)

func TestPackProjectIDMatchesResolveUsableChatProjectID(t *testing.T) {
	t.Parallel()

	ownerID := uuid.New()
	projectID := uuid.New()
	rootID := uuid.New()
	project := uuid.NullUUID{UUID: projectID, Valid: true}
	ownChat := database.Chat{ID: uuid.New(), OwnerID: ownerID, ProjectID: project}
	subagent := database.Chat{
		ID:           uuid.New(),
		OwnerID:      ownerID,
		ParentChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
		RootChatID:   uuid.NullUUID{UUID: rootID, Valid: true},
	}
	failure := xerrors.New("connection reset")

	for _, tc := range []struct {
		name  string
		chat  database.Chat
		setup func(db *dbmock.MockStore)
		want  uuid.NullUUID
		// failed is true when the upstream resolver logs a failed lookup,
		// which packProjectID returns.
		failed bool
	}{
		{
			name: "ProjectOwner",
			chat: ownChat,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{ID: projectID, OwnerID: ownerID}, nil).Times(2)
			},
			want: project,
		},
		{
			name: "SharedWithChatOwner",
			chat: ownChat,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{ID: projectID, OwnerID: uuid.New()}, nil).Times(2)
				db.EXPECT().IsChatProjectUsableByUserID(gomock.Any(), database.IsChatProjectUsableByUserIDParams{ProjectID: projectID, UserID: ownerID}).Return(true, nil).Times(2)
			},
			want: project,
		},
		{
			name: "RevokedFromChatOwner",
			chat: ownChat,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{ID: projectID, OwnerID: uuid.New()}, nil).Times(2)
				db.EXPECT().IsChatProjectUsableByUserID(gomock.Any(), database.IsChatProjectUsableByUserIDParams{ProjectID: projectID, UserID: ownerID}).Return(false, nil).Times(2)
			},
		},
		{
			name:  "OutsideProject",
			chat:  database.Chat{ID: uuid.New(), OwnerID: ownerID},
			setup: func(*dbmock.MockStore) {},
		},
		{
			name: "SubagentInheritsRootProject",
			chat: subagent,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatByID(gomock.Any(), rootID).Return(database.Chat{ID: rootID, ProjectID: project}, nil).Times(2)
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{ID: projectID, OwnerID: ownerID}, nil).Times(2)
			},
			want: project,
		},
		{
			name: "SubagentRootWithoutProject",
			chat: subagent,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatByID(gomock.Any(), rootID).Return(database.Chat{ID: rootID}, nil).Times(2)
			},
		},
		{
			name: "MissingRoot",
			chat: subagent,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatByID(gomock.Any(), rootID).Return(database.Chat{}, sql.ErrNoRows).Times(2)
			},
		},
		{
			name: "MissingProject",
			chat: ownChat,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{}, sql.ErrNoRows).Times(2)
			},
		},
		{
			name: "RootLookupFailure",
			chat: subagent,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatByID(gomock.Any(), rootID).Return(database.Chat{}, failure).Times(2)
			},
			failed: true,
		},
		{
			name: "ProjectLookupFailure",
			chat: ownChat,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{}, failure).Times(2)
			},
			failed: true,
		},
		{
			name: "AccessCheckFailure",
			chat: ownChat,
			setup: func(db *dbmock.MockStore) {
				db.EXPECT().GetChatProjectByID(gomock.Any(), projectID).Return(database.ChatProject{ID: projectID, OwnerID: uuid.New()}, nil).Times(2)
				db.EXPECT().IsChatProjectUsableByUserID(gomock.Any(), gomock.Any()).Return(false, failure).Times(2)
			},
			failed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The strict mock requires both resolvers to make the same
			// lookups, each once.
			db := dbmock.NewMockStore(gomock.NewController(t))
			tc.setup(db)
			server := newProjectTestServer(t, db)

			upstream := server.resolveUsableChatProjectID(t.Context(), server.logger, tc.chat)
			fork, err := server.packProjectID(t.Context(), tc.chat)
			require.Equal(t, tc.want, upstream, "upstream result")
			require.Equal(t, upstream, fork, "packProjectID matches resolveUsableChatProjectID")
			if tc.failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
