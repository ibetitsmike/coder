package forkdb

import (
	"slices"

	"github.com/coder/coder/v2/coderd/database"
)

// API key scopes that fork nodes add to the api_key_scope enum. Upstream's
// generated database.APIKeyScope.Valid does not know them, so validate scopes
// with ValidAPIKeyScope.
const (
	APIKeyScopePlugin             database.APIKeyScope = "plugin:*"
	APIKeyScopePluginCreate       database.APIKeyScope = "plugin:create"
	APIKeyScopePluginRead         database.APIKeyScope = "plugin:read"
	APIKeyScopePluginUpdate       database.APIKeyScope = "plugin:update"
	APIKeyScopePluginDelete       database.APIKeyScope = "plugin:delete"
	APIKeyScopeCoderChatsConverse database.APIKeyScope = "coder:chats.converse" //nolint:gosec // A scope name, not a credential.
)

// APIKeyScopeValues returns the api_key_scope values that fork nodes add.
func APIKeyScopeValues() []database.APIKeyScope {
	return []database.APIKeyScope{
		APIKeyScopePlugin,
		APIKeyScopePluginCreate,
		APIKeyScopePluginRead,
		APIKeyScopePluginUpdate,
		APIKeyScopePluginDelete,
		APIKeyScopeCoderChatsConverse,
	}
}

// ValidAPIKeyScope reports whether s is a value of the api_key_scope enum,
// counting the values that fork nodes add.
func ValidAPIKeyScope(s database.APIKeyScope) bool {
	return s.Valid() || slices.Contains(APIKeyScopeValues(), s)
}
