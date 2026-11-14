package coderd_test

// This file is fork code: it covers the HTML attachment route of the
// fork-html-attachments experiment.

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func newForkHTMLAttachmentsAPI(t *testing.T, experiments ...codersdk.Experiment) (*codersdk.Client, database.Store) {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	values := coderdtest.DeploymentValues(t)
	for _, experiment := range experiments {
		values.Experiments = append(values.Experiments, string(experiment))
	}
	client, _, _ := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues: values,
		Database:         db,
		Pubsub:           ps,
	})
	return client, db
}

func insertForkHTMLChatFile(t *testing.T, db database.Store, user codersdk.CreateFirstUserResponse, name, mimetype, data string) uuid.UUID {
	t.Helper()

	row, err := db.InsertChatFile(testutil.Context(t, testutil.WaitShort), database.InsertChatFileParams{
		OwnerID:        user.UserID,
		OrganizationID: user.OrganizationID,
		Name:           name,
		Mimetype:       mimetype,
		Data:           []byte(data),
	})
	require.NoError(t, err)
	return row.ID
}

type forkHTMLAttachmentResponse struct {
	status int
	header http.Header
	body   string
}

func getForkHTMLAttachment(t *testing.T, client *codersdk.Client, fileID string) forkHTMLAttachmentResponse {
	t.Helper()

	ctx := testutil.Context(t, testutil.WaitLong)
	res, err := client.Request(ctx, http.MethodGet, fmt.Sprintf("/api/v2/chats/fork/files/%s/html", fileID), nil)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return forkHTMLAttachmentResponse{status: res.StatusCode, header: res.Header, body: string(body)}
}

// requireForkHTMLAttachmentSandbox checks the headers that keep a response of
// the HTML attachment route from running a script on the Coder origin.
func requireForkHTMLAttachmentSandbox(t *testing.T, res forkHTMLAttachmentResponse) {
	t.Helper()

	csp := res.header.Get("Content-Security-Policy")
	require.Contains(t, csp, "sandbox allow-scripts;", "status %d", res.status)
	require.Contains(t, csp, "default-src 'none';", "status %d", res.status)
	require.NotContains(t, csp, "allow-same-origin", "status %d", res.status)
	require.Equal(t, "nosniff", res.header.Get("X-Content-Type-Options"), "status %d", res.status)
	require.Equal(t, "no-referrer", res.header.Get("Referrer-Policy"), "status %d", res.status)
}

func TestForkChatHTMLAttachment(t *testing.T) {
	t.Parallel()

	const page = `<!doctype html><button onclick="parent.postMessage({type:'coder:chat-message',text:'ok'},'*')">OK</button>`

	t.Run("ServesSandboxedHTML", func(t *testing.T) {
		t.Parallel()

		client, db := newForkHTMLAttachmentsAPI(t, codersdk.ExperimentForkHTMLAttachments)
		user := coderdtest.CreateFirstUser(t, client)
		// attach_file stores an HTML page as text/plain.
		fileID := insertForkHTMLChatFile(t, db, user, "status.html", "text/plain", page)

		res := getForkHTMLAttachment(t, client, fileID.String())
		require.Equal(t, http.StatusOK, res.status)
		require.Equal(t, page, res.body)
		require.Equal(t, "text/html; charset=utf-8", res.header.Get("Content-Type"))
		require.Equal(t, "nosniff", res.header.Get("X-Content-Type-Options"))
		require.Equal(t, "no-referrer", res.header.Get("Referrer-Policy"))
		require.Equal(t, `inline; filename=status.html`, res.header.Get("Content-Disposition"))

		// The policy is what keeps the page away from the session and the
		// API, so pin it exactly.
		require.Equal(t,
			"sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; "+
				"style-src 'unsafe-inline'; img-src data: blob:; font-src data:; "+
				"media-src data: blob:; form-action 'none'; base-uri 'none'; frame-ancestors 'self'",
			res.header.Get("Content-Security-Policy"),
		)
		require.NotContains(t, res.header.Get("Content-Security-Policy"), "allow-same-origin")
	})

	t.Run("AcceptsHTMLMediaTypeAndHtmExtension", func(t *testing.T) {
		t.Parallel()

		client, db := newForkHTMLAttachmentsAPI(t, codersdk.ExperimentForkHTMLAttachments)
		user := coderdtest.CreateFirstUser(t, client)
		fileID := insertForkHTMLChatFile(t, db, user, "PLAN.HTM", "text/html", page)

		res := getForkHTMLAttachment(t, client, fileID.String())
		require.Equal(t, http.StatusOK, res.status)
		require.Equal(t, page, res.body)
	})

	t.Run("RejectsOtherFiles", func(t *testing.T) {
		t.Parallel()

		client, db := newForkHTMLAttachmentsAPI(t, codersdk.ExperimentForkHTMLAttachments)
		user := coderdtest.CreateFirstUser(t, client)
		for _, file := range []struct{ name, mimetype string }{
			{"notes.txt", "text/plain"},
			{"image.html", "image/png"},
			{"drawing.svg", "image/svg+xml"},
		} {
			fileID := insertForkHTMLChatFile(t, db, user, file.name, file.mimetype, page)
			res := getForkHTMLAttachment(t, client, fileID.String())
			require.Equal(t, http.StatusBadRequest, res.status, file.name)
		}

		res := getForkHTMLAttachment(t, client, "not-a-uuid")
		require.Equal(t, http.StatusBadRequest, res.status)
	})

	t.Run("HidesOtherUsersFiles", func(t *testing.T) {
		t.Parallel()

		client, db := newForkHTMLAttachmentsAPI(t, codersdk.ExperimentForkHTMLAttachments)
		user := coderdtest.CreateFirstUser(t, client)
		fileID := insertForkHTMLChatFile(t, db, user, "status.html", "text/plain", page)
		member, _ := coderdtest.CreateAnotherUser(t, client, user.OrganizationID)

		res := getForkHTMLAttachment(t, member, fileID.String())
		require.Equal(t, http.StatusNotFound, res.status)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		client, db := newForkHTMLAttachmentsAPI(t)
		user := coderdtest.CreateFirstUser(t, client)
		fileID := insertForkHTMLChatFile(t, db, user, "status.html", "text/plain", page)

		res := getForkHTMLAttachment(t, client, fileID.String())
		require.Equal(t, http.StatusForbidden, res.status)
		require.Contains(t, res.body, "fork-html-attachments")
		requireForkHTMLAttachmentSandbox(t, res)
	})

	// Every response of the route carries the sandbox policy, so a direct
	// open of an error never runs a script on the Coder origin either.
	t.Run("SandboxesEveryResponse", func(t *testing.T) {
		t.Parallel()

		client, db := newForkHTMLAttachmentsAPI(t, codersdk.ExperimentForkHTMLAttachments)
		user := coderdtest.CreateFirstUser(t, client)
		htmlID := insertForkHTMLChatFile(t, db, user, "status.html", "text/plain", page)
		textID := insertForkHTMLChatFile(t, db, user, "notes.txt", "text/plain", page)
		member, _ := coderdtest.CreateAnotherUser(t, client, user.OrganizationID)
		anonymous := codersdk.New(client.URL)

		offClient, offDB := newForkHTMLAttachmentsAPI(t)
		offUser := coderdtest.CreateFirstUser(t, offClient)
		offID := insertForkHTMLChatFile(t, offDB, offUser, "status.html", "text/plain", page)

		for _, tc := range []struct {
			name   string
			client *codersdk.Client
			fileID string
			status int
		}{
			{"OK", client, htmlID.String(), http.StatusOK},
			{"InvalidID", client, "not-a-uuid", http.StatusBadRequest},
			{"NotHTML", client, textID.String(), http.StatusBadRequest},
			{"Missing", client, uuid.NewString(), http.StatusNotFound},
			{"OtherUser", member, htmlID.String(), http.StatusNotFound},
			{"Unauthenticated", anonymous, htmlID.String(), http.StatusUnauthorized},
			{"ExperimentOff", offClient, offID.String(), http.StatusForbidden},
		} {
			res := getForkHTMLAttachment(t, tc.client, tc.fileID)
			require.Equal(t, tc.status, res.status, tc.name)
			requireForkHTMLAttachmentSandbox(t, res)
			if tc.status != http.StatusOK {
				require.NotContains(t, res.header.Get("Content-Type"), "text/html", tc.name)
			}
		}
	})
}
