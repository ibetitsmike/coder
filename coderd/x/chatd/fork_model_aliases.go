package chatd

// This file is fork code: custom agent definitions of the
// fork-agent-definitions experiment can set their model to an alias that an
// admin listed in a model config's aliases. resolveCustomAgentModel falls back
// to resolveCustomAgentModelAlias when no model config ID, model or display
// name matches.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// resolveCustomAgentModelAlias returns the spawnable model that has the
// definition's model as an alias. An alias is unique per AI provider, so when
// several providers use it, the model on the parent chat's provider wins.
func (p *Server) resolveCustomAgentModelAlias(ctx context.Context, parent database.Chat, def workspacesdk.AgentDefinition, models []map[string]any) (uuid.UUID, error) {
	//nolint:gocritic // Chatd reads model configuration for the chat owner.
	chatdCtx := dbauthz.AsChatd(ctx)
	alias := strings.ToLower(def.Model)
	var matches []database.ChatModelConfig
	for _, model := range models {
		aliases, _ := model["aliases"].([]string)
		if !slices.Contains(aliases, alias) {
			continue
		}
		id, _ := model["model_config_id"].(string)
		modelConfigID, err := uuid.Parse(id)
		if err != nil {
			return uuid.Nil, xerrors.Errorf("parse model config id %q: %w", id, err)
		}
		config, err := p.db.GetChatModelConfigByID(chatdCtx, modelConfigID)
		if err != nil {
			return uuid.Nil, xerrors.Errorf("get model config %s: %w", modelConfigID, err)
		}
		matches = append(matches, config)
	}
	switch len(matches) {
	case 0:
		return uuid.Nil, xerrors.Errorf(
			"agent %q sets model %q, which is not an available model; pass model_config_id from %s instead",
			def.Name, def.Model, listSubagentModelsToolName,
		)
	case 1:
		return matches[0].ID, nil
	}
	parentModel, err := p.db.GetChatModelConfigByID(chatdCtx, parent.LastModelConfigID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, xerrors.Errorf("get parent chat model config: %w", err)
	}
	labels := make([]string, 0, len(matches))
	for _, match := range matches {
		if err == nil && match.AIProviderID == parentModel.AIProviderID {
			return match.ID, nil
		}
		labels = append(labels, fmt.Sprintf("%s (%s)", match.Model, match.ID))
	}
	return uuid.Nil, xerrors.Errorf(
		"agent %q sets model %q, an alias of several models, none on the parent chat's AI provider: %s; pass model_config_id from %s instead",
		def.Name, def.Model, strings.Join(labels, ", "), listSubagentModelsToolName,
	)
}
