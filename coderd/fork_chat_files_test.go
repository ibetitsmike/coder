package coderd_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/testutil"
)

func TestGetChatFileByteRange(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, store := newChatClientWithDatabase(t)
	firstUser := coderdtest.CreateFirstUser(t, client.Client)

	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	//nolint:gocritic // Using AsChatd to mimic an agent-attached video.
	row, err := store.InsertChatFile(dbauthz.AsChatd(ctx), database.InsertChatFileParams{
		OwnerID:        firstUser.UserID,
		OrganizationID: firstUser.OrganizationID,
		Name:           "review.mp4",
		Mimetype:       "video/mp4",
		Data:           data,
	})
	require.NoError(t, err)
	path := fmt.Sprintf("/api/v2/chats/files/%s", row.ID)

	t.Run("RangeRequest", func(t *testing.T) {
		t.Parallel()

		res, err := client.Request(ctx, http.MethodGet, path, nil, func(r *http.Request) {
			r.Header.Set("Range", "bytes=10-19")
		})
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusPartialContent, res.StatusCode)
		require.Equal(t, "bytes 10-19/100", res.Header.Get("Content-Range"))
		require.Equal(t, "video/mp4", res.Header.Get("Content-Type"))
		got, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, data[10:20], got)
	})

	t.Run("FullRequest", func(t *testing.T) {
		t.Parallel()

		res, err := client.Request(ctx, http.MethodGet, path, nil)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "bytes", res.Header.Get("Accept-Ranges"))
		require.Equal(t, "video/mp4", res.Header.Get("Content-Type"))
		got, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, data, got)
	})
}
