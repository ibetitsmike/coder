package dbauthz

// This file is fork code: it authorizes the fork queries of package forkdb
// like the methods of querier authorize upstream's queries. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"context"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
)

var _ forkdb.Wrapper = (*querier)(nil)

func (q *querier) ForkUnwrap() database.Store {
	return q.db
}

func (q *querier) ForkWrap(s forkdb.Store) forkdb.Store {
	return &forkQuerier{q: q, db: s}
}

// forkQuerier authorizes each fork query. It does not embed forkdb.Store, so
// a new fork query does not compile until it is authorized here.
type forkQuerier struct {
	q  *querier
	db forkdb.Store
}

var _ forkdb.Store = (*forkQuerier)(nil)

func (f *forkQuerier) AcquirePluginLeases(ctx context.Context, arg forkdb.AcquirePluginLeasesParams) ([]forkdb.Plugin, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return nil, err
	}
	return f.db.AcquirePluginLeases(ctx, arg)
}

func (f *forkQuerier) ConsumePluginLinkCode(ctx context.Context, arg forkdb.ConsumePluginLinkCodeParams) (forkdb.PluginLinkCode, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionDelete, rbac.ResourceSystem); err != nil {
		return forkdb.PluginLinkCode{}, err
	}
	return f.db.ConsumePluginLinkCode(ctx, arg)
}

func (f *forkQuerier) CountActiveSubagentsForCapacity(ctx context.Context, arg forkdb.CountActiveSubagentsForCapacityParams) (forkdb.CountActiveSubagentsForCapacityRow, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceChat); err != nil {
		return forkdb.CountActiveSubagentsForCapacityRow{}, err
	}
	return f.db.CountActiveSubagentsForCapacity(ctx, arg)
}

// CountLiveSubagentsInFamily authorizes through the family's root chat.
func (f *forkQuerier) CountLiveSubagentsInFamily(ctx context.Context, rootChatID uuid.UUID) (int64, error) {
	if _, err := f.q.GetChatByID(ctx, rootChatID); err != nil {
		return 0, err
	}
	return f.db.CountLiveSubagentsInFamily(ctx, rootChatID)
}

func (f *forkQuerier) DeleteExpiredPluginLinkCodes(ctx context.Context) error {
	if err := f.q.authorizeContext(ctx, policy.ActionDelete, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.DeleteExpiredPluginLinkCodes(ctx)
}

func (f *forkQuerier) DeleteOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.DeleteOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	fetch := func(ctx context.Context, arg forkdb.DeleteOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
		return f.db.GetOrganizationAgentDefinitionByName(ctx, forkdb.GetOrganizationAgentDefinitionByNameParams(arg))
	}
	return fetchAndQuery(f.q.log, f.q.auth, policy.ActionDelete, fetch, f.db.DeleteOrganizationAgentDefinitionByName)(ctx, arg)
}

func (f *forkQuerier) DeletePluginByID(ctx context.Context, id uuid.UUID) error {
	return deleteQ(f.q.log, f.q.auth, f.db.GetPluginByID, f.db.DeletePluginByID)(ctx, id)
}

func (f *forkQuerier) DeletePluginLinkCodesByUser(ctx context.Context, arg forkdb.DeletePluginLinkCodesByUserParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionDelete, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.DeletePluginLinkCodesByUser(ctx, arg)
}

func (f *forkQuerier) DeletePluginUserLink(ctx context.Context, arg forkdb.DeletePluginUserLinkParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionDelete, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.DeletePluginUserLink(ctx, arg)
}

func (f *forkQuerier) DeleteUserAgentDefinitionByName(ctx context.Context, arg forkdb.DeleteUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionDelete, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.DeleteUserAgentDefinitionByName(ctx, arg)
}

func (f *forkQuerier) EnsurePluginChatBindings(ctx context.Context, pluginID uuid.UUID) error {
	if err := f.q.authorizeContext(ctx, policy.ActionCreate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.EnsurePluginChatBindings(ctx, pluginID)
}

func (f *forkQuerier) GetChatAwaitingWorker(ctx context.Context, arg forkdb.GetChatAwaitingWorkerParams) (bool, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceChat); err != nil {
		return false, err
	}
	return f.db.GetChatAwaitingWorker(ctx, arg)
}

func (f *forkQuerier) GetChatOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID) (forkdb.ChatOrganizationSubagentLimit, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceChatModelConfig.InOrg(organizationID)); err != nil {
		return forkdb.ChatOrganizationSubagentLimit{}, err
	}
	return f.db.GetChatOrganizationSubagentLimits(ctx, organizationID)
}

// GetChatSubagentByChatID authorizes through the owning chat.
func (f *forkQuerier) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	if _, err := f.q.GetChatByID(ctx, chatID); err != nil {
		return forkdb.ChatSubagent{}, err
	}
	return f.db.GetChatSubagentByChatID(ctx, chatID)
}

// GetChatSubtreeIDs authorizes through the subtree's root chat.
func (f *forkQuerier) GetChatSubtreeIDs(ctx context.Context, rootID uuid.UUID) ([]forkdb.GetChatSubtreeIDsRow, error) {
	if _, err := f.q.GetChatByID(ctx, rootID); err != nil {
		return nil, err
	}
	return f.db.GetChatSubtreeIDs(ctx, rootID)
}

// GetChatTodosByChatID authorizes through the owning chat.
func (f *forkQuerier) GetChatTodosByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatTodo, error) {
	if _, err := f.q.GetChatByID(ctx, chatID); err != nil {
		return forkdb.ChatTodo{}, err
	}
	return f.db.GetChatTodosByChatID(ctx, chatID)
}

func (f *forkQuerier) GetOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.GetOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	return fetch(f.q.log, f.q.auth, f.db.GetOrganizationAgentDefinitionByName)(ctx, arg)
}

func (f *forkQuerier) GetPluginByID(ctx context.Context, id uuid.UUID) (forkdb.Plugin, error) {
	return fetch(f.q.log, f.q.auth, f.db.GetPluginByID)(ctx, id)
}

func (f *forkQuerier) GetPluginChatBindingsDue(ctx context.Context, arg forkdb.GetPluginChatBindingsDueParams) ([]forkdb.GetPluginChatBindingsDueRow, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceSystem); err != nil {
		return nil, err
	}
	return f.db.GetPluginChatBindingsDue(ctx, arg)
}

func (f *forkQuerier) GetPluginUserLink(ctx context.Context, arg forkdb.GetPluginUserLinkParams) (forkdb.PluginUserLink, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceSystem); err != nil {
		return forkdb.PluginUserLink{}, err
	}
	return f.db.GetPluginUserLink(ctx, arg)
}

func (f *forkQuerier) GetPluginUserLinksByPluginAndUser(ctx context.Context, arg forkdb.GetPluginUserLinksByPluginAndUserParams) ([]forkdb.PluginUserLink, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceSystem); err != nil {
		return nil, err
	}
	return f.db.GetPluginUserLinksByPluginAndUser(ctx, arg)
}

func (f *forkQuerier) GetPluginUserLinksByPluginID(ctx context.Context, pluginID uuid.UUID) ([]forkdb.PluginUserLink, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceSystem); err != nil {
		return nil, err
	}
	return f.db.GetPluginUserLinksByPluginID(ctx, pluginID)
}

func (f *forkQuerier) GetPluginUserLinksByUserID(ctx context.Context, userID uuid.UUID) ([]forkdb.PluginUserLink, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceSystem); err != nil {
		return nil, err
	}
	return f.db.GetPluginUserLinksByUserID(ctx, userID)
}

func (f *forkQuerier) GetPluginsByOrganizationID(ctx context.Context, organizationID uuid.UUID) ([]forkdb.Plugin, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourcePlugin.InOrg(organizationID)); err != nil {
		return nil, err
	}
	return f.db.GetPluginsByOrganizationID(ctx, organizationID)
}

func (f *forkQuerier) GetUserAgentDefinitionByName(ctx context.Context, arg forkdb.GetUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionRead, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.GetUserAgentDefinitionByName(ctx, arg)
}

// InsertChatSubagent authorizes as an update of the owning chat, like
// InsertChatDebugRun.
func (f *forkQuerier) InsertChatSubagent(ctx context.Context, arg forkdb.InsertChatSubagentParams) (forkdb.ChatSubagent, error) {
	chat, err := f.q.db.GetChatByID(ctx, arg.ChatID)
	if err != nil {
		return forkdb.ChatSubagent{}, err
	}
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, chat); err != nil {
		return forkdb.ChatSubagent{}, err
	}
	return f.db.InsertChatSubagent(ctx, arg)
}

func (f *forkQuerier) InsertOrganizationAgentDefinition(ctx context.Context, arg forkdb.InsertOrganizationAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionCreate, rbac.ResourceOrganizationSkill.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.InsertOrganizationAgentDefinition(ctx, arg)
}

func (f *forkQuerier) InsertPlugin(ctx context.Context, arg forkdb.InsertPluginParams) (forkdb.Plugin, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionCreate, rbac.ResourcePlugin.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.Plugin{}, err
	}
	return f.db.InsertPlugin(ctx, arg)
}

func (f *forkQuerier) InsertPluginLinkCode(ctx context.Context, arg forkdb.InsertPluginLinkCodeParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionCreate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.InsertPluginLinkCode(ctx, arg)
}

func (f *forkQuerier) InsertUserAgentDefinition(ctx context.Context, arg forkdb.InsertUserAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionCreate, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.InsertUserAgentDefinition(ctx, arg)
}

func (f *forkQuerier) ListOrganizationAgentDefinitionMetadata(ctx context.Context, organizationID uuid.UUID) ([]forkdb.ListOrganizationAgentDefinitionMetadataRow, error) {
	return fetchWithPostFilter(f.q.auth, policy.ActionRead, f.db.ListOrganizationAgentDefinitionMetadata)(ctx, organizationID)
}

func (f *forkQuerier) ListUserAgentDefinitionMetadata(ctx context.Context, arg forkdb.ListUserAgentDefinitionMetadataParams) ([]forkdb.ListUserAgentDefinitionMetadataRow, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionRead, arg.OrganizationID, arg.UserID); err != nil {
		return nil, err
	}
	return f.db.ListUserAgentDefinitionMetadata(ctx, arg)
}

// LockChatForNoKeyUpdate authorizes as an update of the chat, like
// InsertChatSubagent.
func (f *forkQuerier) LockChatForNoKeyUpdate(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	chat, err := f.q.db.GetChatByID(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, chat); err != nil {
		return uuid.Nil, err
	}
	return f.db.LockChatForNoKeyUpdate(ctx, id)
}

func (f *forkQuerier) ReleasePluginLeases(ctx context.Context, runnerID uuid.UUID) error {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.ReleasePluginLeases(ctx, runnerID)
}

func (f *forkQuerier) SetPluginUserLinkActiveChat(ctx context.Context, arg forkdb.SetPluginUserLinkActiveChatParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.SetPluginUserLinkActiveChat(ctx, arg)
}

func (f *forkQuerier) SetPluginUserLinksActiveChatByUser(ctx context.Context, arg forkdb.SetPluginUserLinksActiveChatByUserParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.SetPluginUserLinksActiveChatByUser(ctx, arg)
}

func (f *forkQuerier) UpdateOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	fetch := func(ctx context.Context, arg forkdb.UpdateOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
		return f.db.GetOrganizationAgentDefinitionByName(ctx, forkdb.GetOrganizationAgentDefinitionByNameParams{
			OrganizationID: arg.OrganizationID,
			Name:           arg.Name,
		})
	}
	return updateWithReturn(f.q.log, f.q.auth, fetch, f.db.UpdateOrganizationAgentDefinitionByName)(ctx, arg)
}

func (f *forkQuerier) UpdatePluginChatBindingDelivered(ctx context.Context, arg forkdb.UpdatePluginChatBindingDeliveredParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.UpdatePluginChatBindingDelivered(ctx, arg)
}

func (f *forkQuerier) UpdatePluginChatBindingFailed(ctx context.Context, arg forkdb.UpdatePluginChatBindingFailedParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.UpdatePluginChatBindingFailed(ctx, arg)
}

func (f *forkQuerier) UpdatePluginConfig(ctx context.Context, arg forkdb.UpdatePluginConfigParams) (forkdb.Plugin, error) {
	fetch := func(ctx context.Context, arg forkdb.UpdatePluginConfigParams) (forkdb.Plugin, error) {
		return f.db.GetPluginByID(ctx, arg.ID)
	}
	return updateWithReturn(f.q.log, f.q.auth, fetch, f.db.UpdatePluginConfig)(ctx, arg)
}

func (f *forkQuerier) UpdatePluginEnabled(ctx context.Context, arg forkdb.UpdatePluginEnabledParams) (forkdb.Plugin, error) {
	fetch := func(ctx context.Context, arg forkdb.UpdatePluginEnabledParams) (forkdb.Plugin, error) {
		return f.db.GetPluginByID(ctx, arg.ID)
	}
	return updateWithReturn(f.q.log, f.q.auth, fetch, f.db.UpdatePluginEnabled)(ctx, arg)
}

func (f *forkQuerier) UpdatePluginLastError(ctx context.Context, arg forkdb.UpdatePluginLastErrorParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.UpdatePluginLastError(ctx, arg)
}

func (f *forkQuerier) UpdateUserAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionUpdate, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.UpdateUserAgentDefinitionByName(ctx, arg)
}

func (f *forkQuerier) UpsertChatOrganizationSubagentLimits(ctx context.Context, arg forkdb.UpsertChatOrganizationSubagentLimitsParams) (forkdb.ChatOrganizationSubagentLimit, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceChatModelConfig.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.ChatOrganizationSubagentLimit{}, err
	}
	return f.db.UpsertChatOrganizationSubagentLimits(ctx, arg)
}

// UpsertChatTodos authorizes as an update of the owning chat, like
// InsertChatSubagent.
func (f *forkQuerier) UpsertChatTodos(ctx context.Context, arg forkdb.UpsertChatTodosParams) (forkdb.ChatTodo, error) {
	chat, err := f.q.db.GetChatByID(ctx, arg.ChatID)
	if err != nil {
		return forkdb.ChatTodo{}, err
	}
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, chat); err != nil {
		return forkdb.ChatTodo{}, err
	}
	return f.db.UpsertChatTodos(ctx, arg)
}

func (f *forkQuerier) UpsertPluginUserLink(ctx context.Context, arg forkdb.UpsertPluginUserLinkParams) (forkdb.PluginUserLink, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return forkdb.PluginUserLink{}, err
	}
	return f.db.UpsertPluginUserLink(ctx, arg)
}

// authorizeUserAgentDefinition authorizes action on the user-tier agent
// definitions of userID in organizationID. Their RBAC object is a personal
// skill without an organization, so the organization check denies users who
// are not members of organizationID.
func (f *forkQuerier) authorizeUserAgentDefinition(ctx context.Context, action policy.Action, organizationID, userID uuid.UUID) error {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceOrganization.WithID(organizationID).InOrg(organizationID)); err != nil {
		return err
	}
	return f.q.authorizeContext(ctx, action, rbac.ResourceUserSkill.WithOwner(userID.String()))
}
