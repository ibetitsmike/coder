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

func (f *forkQuerier) CancelChatWakeupsByChatID(ctx context.Context, arg forkdb.CancelChatWakeupsByChatIDParams) ([]forkdb.ChatWakeup, error) {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return nil, err
	}
	return f.db.CancelChatWakeupsByChatID(ctx, arg)
}

// ClaimDueChatWakeups spans chats, so it needs site-wide chat access, like chatd's.
func (f *forkQuerier) ClaimDueChatWakeups(ctx context.Context, arg forkdb.ClaimDueChatWakeupsParams) ([]forkdb.ChatWakeup, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceChat); err != nil {
		return nil, err
	}
	return f.db.ClaimDueChatWakeups(ctx, arg)
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

// CountChatUserNotificationsSince reads notification messages, like
// GetNotificationMessagesByStatus.
func (f *forkQuerier) CountChatUserNotificationsSince(ctx context.Context, arg forkdb.CountChatUserNotificationsSinceParams) (int64, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceNotificationMessage); err != nil {
		return 0, err
	}
	return f.db.CountChatUserNotificationsSince(ctx, arg)
}

func (f *forkQuerier) CountChatWakeupRunsSince(ctx context.Context, arg forkdb.CountChatWakeupRunsSinceParams) (int64, error) {
	if err := f.authorizeChat(ctx, policy.ActionRead, arg.ChatID); err != nil {
		return 0, err
	}
	return f.db.CountChatWakeupRunsSince(ctx, arg)
}

// CountLiveSubagentsInFamily authorizes through the family's root chat.
func (f *forkQuerier) CountLiveSubagentsInFamily(ctx context.Context, rootChatID uuid.UUID) (int64, error) {
	if _, err := f.q.GetChatByID(ctx, rootChatID); err != nil {
		return 0, err
	}
	return f.db.CountLiveSubagentsInFamily(ctx, rootChatID)
}

// CountOrganizationChatWakeupRunsSince spans chats, so it needs site-wide chat access, like chatd's.
func (f *forkQuerier) CountOrganizationChatWakeupRunsSince(ctx context.Context, arg forkdb.CountOrganizationChatWakeupRunsSinceParams) (int64, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceChat); err != nil {
		return 0, err
	}
	return f.db.CountOrganizationChatWakeupRunsSince(ctx, arg)
}

// DeleteChatSecretAnswer authorizes as an update of the owning chat.
func (f *forkQuerier) DeleteChatPluginPack(ctx context.Context, arg forkdb.DeleteChatPluginPackParams) error {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return err
	}
	return f.db.DeleteChatPluginPack(ctx, arg)
}

func (f *forkQuerier) DeleteChatSecretAnswer(ctx context.Context, arg forkdb.DeleteChatSecretAnswerParams) error {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return err
	}
	return f.db.DeleteChatSecretAnswer(ctx, arg)
}

func (f *forkQuerier) DeleteOldChatSecretAnswers(ctx context.Context, limitCount int32) (int64, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionDelete, rbac.ResourceSystem); err != nil {
		return 0, err
	}
	return f.db.DeleteOldChatSecretAnswers(ctx, limitCount)
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

func (f *forkQuerier) DeletePluginPackByID(ctx context.Context, id uuid.UUID) error {
	return deleteQ(f.q.log, f.q.auth, f.db.GetPluginPackByID, f.db.DeletePluginPackByID)(ctx, id)
}

func (f *forkQuerier) DeletePluginPackResources(ctx context.Context, packID uuid.UUID) error {
	if err := f.authorizePluginPackUpdate(ctx, packID); err != nil {
		return err
	}
	return f.db.DeletePluginPackResources(ctx, packID)
}

func (f *forkQuerier) DeletePluginPackProject(ctx context.Context, arg forkdb.DeletePluginPackProjectParams) error {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionUpdate, arg.ProjectID); err != nil {
		return err
	}
	return f.db.DeletePluginPackProject(ctx, arg)
}

func (f *forkQuerier) DeletePluginUserLink(ctx context.Context, arg forkdb.DeletePluginUserLinkParams) error {
	if err := f.q.authorizeContext(ctx, policy.ActionDelete, rbac.ResourceSystem); err != nil {
		return err
	}
	return f.db.DeletePluginUserLink(ctx, arg)
}

func (f *forkQuerier) DeleteProjectAgentDefinitionByName(ctx context.Context, arg forkdb.DeleteProjectAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionUpdate, arg.ProjectID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.DeleteProjectAgentDefinitionByName(ctx, arg)
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

func (f *forkQuerier) FinishChatWakeupClaim(ctx context.Context, arg forkdb.FinishChatWakeupClaimParams) (forkdb.ChatWakeup, error) {
	if _, err := f.authorizeChatWakeup(ctx, policy.ActionUpdate, arg.ID); err != nil {
		return forkdb.ChatWakeup{}, err
	}
	return f.db.FinishChatWakeupClaim(ctx, arg)
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

func (f *forkQuerier) GetChatOrganizationWakeupCaps(ctx context.Context, organizationID uuid.UUID) (forkdb.ChatOrganizationWakeupCap, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceChatModelConfig.InOrg(organizationID)); err != nil {
		return forkdb.ChatOrganizationWakeupCap{}, err
	}
	return f.db.GetChatOrganizationWakeupCaps(ctx, organizationID)
}

// GetChatSubagentByChatID authorizes through the owning chat.
func (f *forkQuerier) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	if _, err := f.q.GetChatByID(ctx, chatID); err != nil {
		return forkdb.ChatSubagent{}, err
	}
	return f.db.GetChatSubagentByChatID(ctx, chatID)
}

// GetChatSubagentTypesByChatIDs is system-only: it reads many chats at once,
// so callers must already have read the chats they pass.
func (f *forkQuerier) GetChatSubagentTypesByChatIDs(ctx context.Context, chatIDs []uuid.UUID) ([]forkdb.GetChatSubagentTypesByChatIDsRow, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceSystem); err != nil {
		return nil, err
	}
	return f.db.GetChatSubagentTypesByChatIDs(ctx, chatIDs)
}

// GetChatSubagentsByParentChatID authorizes through the parent chat.
func (f *forkQuerier) GetChatSubagentsByParentChatID(ctx context.Context, parentChatID uuid.UUID) ([]forkdb.ChatSubagent, error) {
	if _, err := f.q.GetChatByID(ctx, parentChatID); err != nil {
		return nil, err
	}
	return f.db.GetChatSubagentsByParentChatID(ctx, parentChatID)
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

// GetPendingChatDescendantQuestion authorizes through the owning chat.
func (f *forkQuerier) GetPendingChatDescendantQuestion(ctx context.Context, chatID uuid.UUID) (forkdb.ChatDescendantQuestion, error) {
	if _, err := f.q.GetChatByID(ctx, chatID); err != nil {
		return forkdb.ChatDescendantQuestion{}, err
	}
	return f.db.GetPendingChatDescendantQuestion(ctx, chatID)
}

// GetChatSecretAnswersByChatID returns secret values, so it requires update
// access to the owning chat, not read access.
func (f *forkQuerier) GetChatSecretAnswersByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatSecretAnswer, error) {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, chatID); err != nil {
		return nil, err
	}
	return f.db.GetChatSecretAnswersByChatID(ctx, chatID)
}

func (f *forkQuerier) GetChatWakeupByID(ctx context.Context, id uuid.UUID) (forkdb.ChatWakeup, error) {
	return f.authorizeChatWakeup(ctx, policy.ActionRead, id)
}

func (f *forkQuerier) GetChatWakeupByIDForUpdate(ctx context.Context, id uuid.UUID) (forkdb.ChatWakeup, error) {
	if _, err := f.authorizeChatWakeup(ctx, policy.ActionUpdate, id); err != nil {
		return forkdb.ChatWakeup{}, err
	}
	return f.db.GetChatWakeupByIDForUpdate(ctx, id)
}

// GetFamilySubagentsAwaitingWorker authorizes through the family's root chat.
func (f *forkQuerier) GetFamilySubagentsAwaitingWorker(ctx context.Context, arg forkdb.GetFamilySubagentsAwaitingWorkerParams) ([]forkdb.GetFamilySubagentsAwaitingWorkerRow, error) {
	if _, err := f.q.GetChatByID(ctx, arg.RootChatID); err != nil {
		return nil, err
	}
	return f.db.GetFamilySubagentsAwaitingWorker(ctx, arg)
}

func (f *forkQuerier) GetOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.GetOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	return fetch(f.q.log, f.q.auth, f.db.GetOrganizationAgentDefinitionByName)(ctx, arg)
}

func (f *forkQuerier) GetOrganizationAgentDefinitionByNameForUpdate(ctx context.Context, arg forkdb.GetOrganizationAgentDefinitionByNameForUpdateParams) (forkdb.AgentDefinition, error) {
	return fetch(f.q.log, f.q.auth, f.db.GetOrganizationAgentDefinitionByNameForUpdate)(ctx, arg)
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

func (f *forkQuerier) GetPluginPackByID(ctx context.Context, id uuid.UUID) (forkdb.PluginPack, error) {
	return fetch(f.q.log, f.q.auth, f.db.GetPluginPackByID)(ctx, id)
}

func (f *forkQuerier) GetPluginPackResources(ctx context.Context, packID uuid.UUID) ([]forkdb.PluginPackResource, error) {
	if _, err := f.GetPluginPackByID(ctx, packID); err != nil {
		return nil, err
	}
	return f.db.GetPluginPackResources(ctx, packID)
}

func (f *forkQuerier) GetPluginPackSources(ctx context.Context, organizationID uuid.UUID) (forkdb.PluginPackSource, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceOrganizationSkill.InOrg(organizationID)); err != nil {
		return forkdb.PluginPackSource{}, err
	}
	return f.db.GetPluginPackSources(ctx, organizationID)
}

// GetPluginPacksByChatID authorizes through the owning chat. It returns only
// the names and commits of packs, which members of the chat may see even
// though they cannot read the packs.
func (f *forkQuerier) GetPluginPacksByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.GetPluginPacksByChatIDRow, error) {
	if _, err := f.q.GetChatByID(ctx, chatID); err != nil {
		return nil, err
	}
	return f.db.GetPluginPacksByChatID(ctx, chatID)
}

func (f *forkQuerier) GetPluginPacksByOrganizationID(ctx context.Context, organizationID uuid.UUID) ([]forkdb.PluginPack, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceOrganizationSkill.InOrg(organizationID)); err != nil {
		return nil, err
	}
	return f.db.GetPluginPacksByOrganizationID(ctx, organizationID)
}

// GetPluginPacksByProjectID authorizes through the project, like
// GetPluginPacksByChatID.
func (f *forkQuerier) GetPluginPacksByProjectID(ctx context.Context, projectID uuid.UUID) ([]forkdb.GetPluginPacksByProjectIDRow, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionRead, projectID); err != nil {
		return nil, err
	}
	return f.db.GetPluginPacksByProjectID(ctx, projectID)
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

func (f *forkQuerier) GetProjectAgentDefinitionByName(ctx context.Context, arg forkdb.GetProjectAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionRead, arg.ProjectID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.GetProjectAgentDefinitionByName(ctx, arg)
}

func (f *forkQuerier) GetUserAgentDefinitionByName(ctx context.Context, arg forkdb.GetUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionRead, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.GetUserAgentDefinitionByName(ctx, arg)
}

// InsertChatSecretAnswer authorizes as an update of the owning chat.
// InsertChatPluginPack authorizes as an update of the chat and not as a read
// of the pack: enabling a pack mirrors enabling a skill, and the query only
// accepts a pack of the chat's organization.
func (f *forkQuerier) InsertChatPluginPack(ctx context.Context, arg forkdb.InsertChatPluginPackParams) (forkdb.ChatPluginPack, error) {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return forkdb.ChatPluginPack{}, err
	}
	return f.db.InsertChatPluginPack(ctx, arg)
}

func (f *forkQuerier) InsertChatSecretAnswer(ctx context.Context, arg forkdb.InsertChatSecretAnswerParams) error {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return err
	}
	return f.db.InsertChatSecretAnswer(ctx, arg)
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

func (f *forkQuerier) InsertChatWakeup(ctx context.Context, arg forkdb.InsertChatWakeupParams) (forkdb.ChatWakeup, error) {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return forkdb.ChatWakeup{}, err
	}
	return f.db.InsertChatWakeup(ctx, arg)
}

func (f *forkQuerier) InsertChatWakeupRun(ctx context.Context, arg forkdb.InsertChatWakeupRunParams) (forkdb.ChatWakeupRun, error) {
	if _, err := f.authorizeChatWakeup(ctx, policy.ActionUpdate, arg.WakeupID); err != nil {
		return forkdb.ChatWakeupRun{}, err
	}
	return f.db.InsertChatWakeupRun(ctx, arg)
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

func (f *forkQuerier) InsertPluginPack(ctx context.Context, arg forkdb.InsertPluginPackParams) (forkdb.PluginPack, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionCreate, rbac.ResourceOrganizationSkill.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.PluginPack{}, err
	}
	return f.db.InsertPluginPack(ctx, arg)
}

func (f *forkQuerier) InsertPluginPackResources(ctx context.Context, arg forkdb.InsertPluginPackResourcesParams) error {
	if err := f.authorizePluginPackUpdate(ctx, arg.PackID); err != nil {
		return err
	}
	return f.db.InsertPluginPackResources(ctx, arg)
}

// InsertPluginPackProject authorizes as an update of the project, like
// InsertChatPluginPack.
func (f *forkQuerier) InsertPluginPackProject(ctx context.Context, arg forkdb.InsertPluginPackProjectParams) (forkdb.PluginPackProject, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionUpdate, arg.ProjectID); err != nil {
		return forkdb.PluginPackProject{}, err
	}
	return f.db.InsertPluginPackProject(ctx, arg)
}

func (f *forkQuerier) InsertProjectAgentDefinition(ctx context.Context, arg forkdb.InsertProjectAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionUpdate, arg.ProjectID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.InsertProjectAgentDefinition(ctx, arg)
}

func (f *forkQuerier) InsertUserAgentDefinition(ctx context.Context, arg forkdb.InsertUserAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionCreate, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.InsertUserAgentDefinition(ctx, arg)
}

func (f *forkQuerier) ListChatWakeupRunKeys(ctx context.Context, wakeupID uuid.UUID) ([]string, error) {
	if _, err := f.authorizeChatWakeup(ctx, policy.ActionRead, wakeupID); err != nil {
		return nil, err
	}
	return f.db.ListChatWakeupRunKeys(ctx, wakeupID)
}

func (f *forkQuerier) ListChatWakeupsByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatWakeup, error) {
	if err := f.authorizeChat(ctx, policy.ActionRead, chatID); err != nil {
		return nil, err
	}
	return f.db.ListChatWakeupsByChatID(ctx, chatID)
}

func (f *forkQuerier) ListChildChatIDsWithActiveEventsWakeups(ctx context.Context, parentChatID uuid.UUID) ([]uuid.UUID, error) {
	if err := f.authorizeChat(ctx, policy.ActionRead, parentChatID); err != nil {
		return nil, err
	}
	return f.db.ListChildChatIDsWithActiveEventsWakeups(ctx, parentChatID)
}

func (f *forkQuerier) ListOrganizationAgentDefinitionMetadata(ctx context.Context, organizationID uuid.UUID) ([]forkdb.ListOrganizationAgentDefinitionMetadataRow, error) {
	return fetchWithPostFilter(f.q.auth, policy.ActionRead, f.db.ListOrganizationAgentDefinitionMetadata)(ctx, organizationID)
}

// ListPendingChatDescendantQuestionsByRoot authorizes through the family's
// root chat.
func (f *forkQuerier) ListPendingChatDescendantQuestionsByRoot(ctx context.Context, rootChatID uuid.UUID) ([]forkdb.ListPendingChatDescendantQuestionsByRootRow, error) {
	if _, err := f.q.GetChatByID(ctx, rootChatID); err != nil {
		return nil, err
	}
	return f.db.ListPendingChatDescendantQuestionsByRoot(ctx, rootChatID)
}

func (f *forkQuerier) ListProjectAgentDefinitionMetadata(ctx context.Context, projectID uuid.UUID) ([]forkdb.ListProjectAgentDefinitionMetadataRow, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionRead, projectID); err != nil {
		return nil, err
	}
	return f.db.ListProjectAgentDefinitionMetadata(ctx, projectID)
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

func (f *forkQuerier) SumChatCostMicrosSince(ctx context.Context, arg forkdb.SumChatCostMicrosSinceParams) (int64, error) {
	if err := f.authorizeChat(ctx, policy.ActionRead, arg.ChatID); err != nil {
		return 0, err
	}
	return f.db.SumChatCostMicrosSince(ctx, arg)
}

// SumOrganizationWakeupChatCostMicrosSince spans chats, so it needs site-wide chat access, like chatd's.
func (f *forkQuerier) SumOrganizationWakeupChatCostMicrosSince(ctx context.Context, arg forkdb.SumOrganizationWakeupChatCostMicrosSinceParams) (int64, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionRead, rbac.ResourceChat); err != nil {
		return 0, err
	}
	return f.db.SumOrganizationWakeupChatCostMicrosSince(ctx, arg)
}

// UpdateChatSubagentWorkspaceID authorizes as an update of the owning chat,
// like InsertChatSubagent.
func (f *forkQuerier) UpdateChatSubagentWorkspaceID(ctx context.Context, arg forkdb.UpdateChatSubagentWorkspaceIDParams) error {
	if err := f.authorizeChat(ctx, policy.ActionUpdate, arg.ChatID); err != nil {
		return err
	}
	return f.db.UpdateChatSubagentWorkspaceID(ctx, arg)
}

func (f *forkQuerier) UpdateChatWakeupStatus(ctx context.Context, arg forkdb.UpdateChatWakeupStatusParams) (forkdb.ChatWakeup, error) {
	if _, err := f.authorizeChatWakeup(ctx, policy.ActionUpdate, arg.ID); err != nil {
		return forkdb.ChatWakeup{}, err
	}
	return f.db.UpdateChatWakeupStatus(ctx, arg)
}

func (f *forkQuerier) UpdateOrganizationAgentDefinitionACLByName(ctx context.Context, arg forkdb.UpdateOrganizationAgentDefinitionACLByNameParams) (forkdb.AgentDefinition, error) {
	fetch := func(ctx context.Context, arg forkdb.UpdateOrganizationAgentDefinitionACLByNameParams) (forkdb.AgentDefinition, error) {
		return f.db.GetOrganizationAgentDefinitionByName(ctx, forkdb.GetOrganizationAgentDefinitionByNameParams{
			OrganizationID: arg.OrganizationID,
			Name:           arg.Name,
		})
	}
	return fetchAndQuery(f.q.log, f.q.auth, policy.ActionShare, fetch, f.db.UpdateOrganizationAgentDefinitionACLByName)(ctx, arg)
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

func (f *forkQuerier) UpdatePluginPackCommit(ctx context.Context, arg forkdb.UpdatePluginPackCommitParams) (forkdb.PluginPack, error) {
	fetch := func(ctx context.Context, arg forkdb.UpdatePluginPackCommitParams) (forkdb.PluginPack, error) {
		return f.db.GetPluginPackByID(ctx, arg.ID)
	}
	return updateWithReturn(f.q.log, f.q.auth, fetch, f.db.UpdatePluginPackCommit)(ctx, arg)
}

func (f *forkQuerier) UpdateProjectAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateProjectAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.q.authorizeChatProjectConfig(ctx, policy.ActionUpdate, arg.ProjectID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.UpdateProjectAgentDefinitionByName(ctx, arg)
}

func (f *forkQuerier) UpdateUserAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	if err := f.authorizeUserAgentDefinition(ctx, policy.ActionUpdate, arg.OrganizationID, arg.UserID); err != nil {
		return forkdb.AgentDefinition{}, err
	}
	return f.db.UpdateUserAgentDefinitionByName(ctx, arg)
}

// UpsertChatDescendantQuestion authorizes as an update of the owning chat.
func (f *forkQuerier) UpsertChatDescendantQuestion(ctx context.Context, arg forkdb.UpsertChatDescendantQuestionParams) (forkdb.ChatDescendantQuestion, error) {
	chat, err := f.q.db.GetChatByID(ctx, arg.ChatID)
	if err != nil {
		return forkdb.ChatDescendantQuestion{}, err
	}
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, chat); err != nil {
		return forkdb.ChatDescendantQuestion{}, err
	}
	return f.db.UpsertChatDescendantQuestion(ctx, arg)
}

func (f *forkQuerier) UpsertChatOrganizationSubagentLimits(ctx context.Context, arg forkdb.UpsertChatOrganizationSubagentLimitsParams) (forkdb.ChatOrganizationSubagentLimit, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceChatModelConfig.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.ChatOrganizationSubagentLimit{}, err
	}
	return f.db.UpsertChatOrganizationSubagentLimits(ctx, arg)
}

func (f *forkQuerier) UpsertChatOrganizationWakeupCaps(ctx context.Context, arg forkdb.UpsertChatOrganizationWakeupCapsParams) (forkdb.ChatOrganizationWakeupCap, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceChatModelConfig.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.ChatOrganizationWakeupCap{}, err
	}
	return f.db.UpsertChatOrganizationWakeupCaps(ctx, arg)
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

func (f *forkQuerier) UpsertPluginPackSources(ctx context.Context, arg forkdb.UpsertPluginPackSourcesParams) (forkdb.PluginPackSource, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceOrganizationSkill.InOrg(arg.OrganizationID)); err != nil {
		return forkdb.PluginPackSource{}, err
	}
	return f.db.UpsertPluginPackSources(ctx, arg)
}

func (f *forkQuerier) UpsertPluginUserLink(ctx context.Context, arg forkdb.UpsertPluginUserLinkParams) (forkdb.PluginUserLink, error) {
	if err := f.q.authorizeContext(ctx, policy.ActionUpdate, rbac.ResourceSystem); err != nil {
		return forkdb.PluginUserLink{}, err
	}
	return f.db.UpsertPluginUserLink(ctx, arg)
}

// authorizeChat authorizes action on the chat that owns a fork row, like
// UpsertChatTodos.
func (f *forkQuerier) authorizeChat(ctx context.Context, action policy.Action, chatID uuid.UUID) error {
	chat, err := f.q.db.GetChatByID(ctx, chatID)
	if err != nil {
		return err
	}
	return f.q.authorizeContext(ctx, action, chat)
}

// authorizeChatWakeup authorizes action on the chat that owns wake-up id and
// returns the wake-up.
func (f *forkQuerier) authorizeChatWakeup(ctx context.Context, action policy.Action, id uuid.UUID) (forkdb.ChatWakeup, error) {
	wakeup, err := f.db.GetChatWakeupByID(ctx, id)
	if err != nil {
		return forkdb.ChatWakeup{}, err
	}
	if err := f.authorizeChat(ctx, action, wakeup.ChatID); err != nil {
		return forkdb.ChatWakeup{}, err
	}
	return wakeup, nil
}

// authorizePluginPackUpdate authorizes changing the resources of pack id.
func (f *forkQuerier) authorizePluginPackUpdate(ctx context.Context, id uuid.UUID) error {
	pack, err := f.db.GetPluginPackByID(ctx, id)
	if err != nil {
		return err
	}
	return f.q.authorizeContext(ctx, policy.ActionUpdate, pack)
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
