package coderd

// This file is fork code: it serves HTML chat attachments to the sandboxed
// viewer of the fork-html-attachments experiment.

import (
	"bytes"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/x/chatfiles"
	"github.com/coder/coder/v2/codersdk"
)

// forkHTMLAttachmentCSP confines an HTML attachment. The sandbox directive
// gives the document an opaque origin even when it is opened outside the
// viewer's sandboxed iframe, so its scripts cannot read the Coder session.
// default-src 'none' blocks every fetch and load, so they cannot call the
// Coder API. CSP does not cover WebRTC: webrtc 'block' stops it only in
// browsers that implement that directive, and Chromium does not, so a page
// can still send what the user does in it out over WebRTC. Inline scripts
// and styles run, because a page the agent writes keeps them in one file.
const forkHTMLAttachmentCSP = "sandbox allow-scripts; " +
	"default-src 'none'; " +
	"webrtc 'block'; " +
	"script-src 'unsafe-inline'; " +
	"style-src 'unsafe-inline'; " +
	"img-src data: blob:; " +
	"font-src data:; " +
	"media-src data: blob:; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'self'"

// forkHTMLAttachmentHeaders sets the sandbox policy on every response of the
// HTML attachment route, so an error page cannot run a script on the Coder
// origin either.
func forkHTMLAttachmentHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		header := rw.Header()
		header.Set("Content-Security-Policy", forkHTMLAttachmentCSP)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(rw, r)
	})
}

// isForkHTMLAttachment reports whether a stored chat file is an HTML page.
// The classifier stores HTML as text/plain, which the upstream file route
// serves as a download, so the name decides.
func isForkHTMLAttachment(chatFile database.ChatFile) bool {
	switch strings.ToLower(filepath.Ext(chatFile.Name)) {
	case ".html", ".htm":
	default:
		return false
	}
	switch chatfiles.BaseMediaType(chatFile.Mimetype) {
	case "text/plain", "text/html":
		return true
	}
	return false
}

// @Summary Get a chat HTML attachment for the sandboxed viewer
// @ID get-a-chat-html-attachment-for-the-sandboxed-viewer
// @Security CoderSessionToken
// @Tags Chats
// @Param file path string true "File ID" format(uuid)
// @Success 200
// @Router /api/v2/chats/fork/files/{file}/html [get]
// @x-apidocgen {"skip": true}
// @Description Serves a stored .html or .htm chat file as text/html with a
// @Description Content-Security-Policy that sandboxes it and blocks network
// @Description access.
//
//nolint:revive // HTTP handler writes to ResponseWriter.
func (api *API) getForkChatHTMLAttachment(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	fileID, err := uuid.Parse(chi.URLParam(r, "file"))
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Invalid file ID.",
		})
		return
	}

	chatFile, err := api.Database.GetChatFileByID(ctx, fileID)
	if err != nil {
		if httpapi.Is404Error(err) {
			httpapi.ResourceNotFound(rw)
			return
		}
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to get chat file.",
			Detail:  err.Error(),
		})
		return
	}
	if !isForkHTMLAttachment(chatFile) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "The chat file is not an HTML attachment.",
		})
		return
	}

	header := rw.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Revalidate on every load, so revoked access and the experiment switch
	// take effect at once. Last-Modified keeps a revalidation cheap.
	header.Set("Cache-Control", "private, no-cache")
	header.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": chatFile.Name}))
	http.ServeContent(rw, r, "", chatFile.CreatedAt, bytes.NewReader(chatFile.Data))
}
