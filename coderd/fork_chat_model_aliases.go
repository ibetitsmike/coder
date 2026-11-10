package coderd

// This file is fork code: it validates the model aliases that custom agent
// definitions of the fork-agent-definitions experiment can name as their
// model.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/codersdk"
)

var chatModelAliasPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// validateChatModelAliases checks the aliases in options, the options model
// config configID is saved with. Each alias must be a lowercase slug other
// than "inherit", listed once, and unused by the organization's other model
// configs of the same AI provider. The caller holds the organization's model
// config write lock, so the uniqueness check cannot race another write.
func validateChatModelAliases(
	ctx context.Context,
	tx database.Store,
	organizationID uuid.UUID,
	configID uuid.UUID,
	aiProviderID uuid.NullUUID,
	options json.RawMessage,
) error {
	config := unmarshalChatModelCallConfig(options)
	if config == nil || len(config.Aliases) == 0 {
		return nil
	}
	invalid := func(format string, args ...any) error {
		return &chatModelConfigProviderModelError{Response: codersdk.Response{
			Message: "Invalid model aliases.",
			Detail:  fmt.Sprintf(format, args...),
		}}
	}
	aliases := make(map[string]struct{}, len(config.Aliases))
	for _, alias := range config.Aliases {
		if !chatModelAliasPattern.MatchString(alias) {
			return invalid("alias %q must be 1 to 32 lowercase letters, digits or hyphens", alias)
		}
		if alias == "inherit" {
			return invalid(`alias "inherit" is reserved: a custom agent definition with model "inherit" runs on the parent chat's model`)
		}
		if _, ok := aliases[alias]; ok {
			return invalid("alias %q is listed twice", alias)
		}
		aliases[alias] = struct{}{}
	}

	//nolint:gocritic // Aliases are unique across every config of the provider, including configs the caller cannot read.
	configs, err := tx.GetChatModelConfigs(dbauthz.AsSystemRestricted(ctx), organizationID)
	if err != nil {
		return xerrors.Errorf("get chat model configs: %w", err)
	}
	for _, other := range configs {
		if other.ID == configID || other.AIProviderID != aiProviderID {
			continue
		}
		otherConfig := unmarshalChatModelCallConfig(other.Options)
		if otherConfig == nil {
			continue
		}
		for _, alias := range otherConfig.Aliases {
			if _, ok := aliases[alias]; ok {
				return invalid("alias %q already belongs to model %q of the same AI provider", alias, other.Model)
			}
		}
	}
	return nil
}
