package coderd

// This file is fork code: it maps the /loop command errors of the
// fork-chat-wakeups experiment to HTTP responses.

import (
	"errors"
	"net/http"

	"github.com/coder/coder/v2/coderd/httpapi/httperror"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
)

// forkChatLoopError returns a 400 response for an invalid /loop command, or
// nil for any other error.
func forkChatLoopError(err error) error {
	invalid, ok := errors.AsType[*chatd.InvalidLoopCommandError](err)
	if !ok {
		return nil
	}
	return httperror.WrapResponseError(http.StatusBadRequest, codersdk.Response{
		Message: "Invalid /loop command.",
		Detail:  invalid.Reason,
	}, err)
}
