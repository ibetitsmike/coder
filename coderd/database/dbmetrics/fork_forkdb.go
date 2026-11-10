package dbmetrics

// This file is fork code: it measures the fork queries of package forkdb like
// queryMetricsStore measures upstream's queries. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpmw"
)

var (
	_ forkdb.Wrapper = metricsStore{}
	_ forkdb.Wrapper = queryMetricsStore{}
)

func (m metricsStore) ForkUnwrap() database.Store {
	return m.Store
}

// ForkWrap returns s as it is: metricsStore measures only transactions, and
// forkdb.InTx runs them through InTx of the store.
func (metricsStore) ForkWrap(s forkdb.Store) forkdb.Store {
	return s
}

func (m queryMetricsStore) ForkUnwrap() database.Store {
	return m.s
}

func (m queryMetricsStore) ForkWrap(s forkdb.Store) forkdb.Store {
	return forkQueryMetricsStore{m: m, s: s}
}

// forkQueryMetricsStore does not embed forkdb.Store, so a new fork query does
// not compile until it is measured here.
type forkQueryMetricsStore struct {
	m queryMetricsStore
	s forkdb.Store
}

var _ forkdb.Store = forkQueryMetricsStore{}

func (f forkQueryMetricsStore) observe(ctx context.Context, query string, start time.Time) {
	f.m.queryLatencies.WithLabelValues(query).Observe(time.Since(start).Seconds())
	f.m.queryCounts.WithLabelValues(httpmw.ExtractHTTPRoute(ctx), httpmw.ExtractHTTPMethod(ctx), query).Inc()
}

func (f forkQueryMetricsStore) AcquirePluginLeases(ctx context.Context, arg forkdb.AcquirePluginLeasesParams) ([]forkdb.Plugin, error) {
	defer f.observe(ctx, "AcquirePluginLeases", time.Now())
	return f.s.AcquirePluginLeases(ctx, arg)
}

func (f forkQueryMetricsStore) CancelChatWakeupsByChatID(ctx context.Context, arg forkdb.CancelChatWakeupsByChatIDParams) ([]forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "CancelChatWakeupsByChatID", time.Now())
	return f.s.CancelChatWakeupsByChatID(ctx, arg)
}

func (f forkQueryMetricsStore) ClaimDueChatWakeups(ctx context.Context, arg forkdb.ClaimDueChatWakeupsParams) ([]forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "ClaimDueChatWakeups", time.Now())
	return f.s.ClaimDueChatWakeups(ctx, arg)
}

func (f forkQueryMetricsStore) ConsumePluginLinkCode(ctx context.Context, arg forkdb.ConsumePluginLinkCodeParams) (forkdb.PluginLinkCode, error) {
	defer f.observe(ctx, "ConsumePluginLinkCode", time.Now())
	return f.s.ConsumePluginLinkCode(ctx, arg)
}

func (f forkQueryMetricsStore) CountActiveSubagentsForCapacity(ctx context.Context, arg forkdb.CountActiveSubagentsForCapacityParams) (forkdb.CountActiveSubagentsForCapacityRow, error) {
	defer f.observe(ctx, "CountActiveSubagentsForCapacity", time.Now())
	return f.s.CountActiveSubagentsForCapacity(ctx, arg)
}

func (f forkQueryMetricsStore) CountChatWakeupRunsSince(ctx context.Context, arg forkdb.CountChatWakeupRunsSinceParams) (int64, error) {
	defer f.observe(ctx, "CountChatWakeupRunsSince", time.Now())
	return f.s.CountChatWakeupRunsSince(ctx, arg)
}

func (f forkQueryMetricsStore) CountLiveSubagentsInFamily(ctx context.Context, rootChatID uuid.UUID) (int64, error) {
	defer f.observe(ctx, "CountLiveSubagentsInFamily", time.Now())
	return f.s.CountLiveSubagentsInFamily(ctx, rootChatID)
}

func (f forkQueryMetricsStore) CountOrganizationChatWakeupRunsSince(ctx context.Context, arg forkdb.CountOrganizationChatWakeupRunsSinceParams) (int64, error) {
	defer f.observe(ctx, "CountOrganizationChatWakeupRunsSince", time.Now())
	return f.s.CountOrganizationChatWakeupRunsSince(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteChatWakeupByID(ctx context.Context, id uuid.UUID) error {
	defer f.observe(ctx, "DeleteChatWakeupByID", time.Now())
	return f.s.DeleteChatWakeupByID(ctx, id)
}

func (f forkQueryMetricsStore) DeleteExpiredPluginLinkCodes(ctx context.Context) error {
	defer f.observe(ctx, "DeleteExpiredPluginLinkCodes", time.Now())
	return f.s.DeleteExpiredPluginLinkCodes(ctx)
}

func (f forkQueryMetricsStore) DeleteOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.DeleteOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "DeleteOrganizationAgentDefinitionByName", time.Now())
	return f.s.DeleteOrganizationAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) DeletePluginByID(ctx context.Context, id uuid.UUID) error {
	defer f.observe(ctx, "DeletePluginByID", time.Now())
	return f.s.DeletePluginByID(ctx, id)
}

func (f forkQueryMetricsStore) DeletePluginLinkCodesByUser(ctx context.Context, arg forkdb.DeletePluginLinkCodesByUserParams) error {
	defer f.observe(ctx, "DeletePluginLinkCodesByUser", time.Now())
	return f.s.DeletePluginLinkCodesByUser(ctx, arg)
}

func (f forkQueryMetricsStore) DeletePluginUserLink(ctx context.Context, arg forkdb.DeletePluginUserLinkParams) error {
	defer f.observe(ctx, "DeletePluginUserLink", time.Now())
	return f.s.DeletePluginUserLink(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteProjectAgentDefinitionByName(ctx context.Context, arg forkdb.DeleteProjectAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "DeleteProjectAgentDefinitionByName", time.Now())
	return f.s.DeleteProjectAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteUserAgentDefinitionByName(ctx context.Context, arg forkdb.DeleteUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "DeleteUserAgentDefinitionByName", time.Now())
	return f.s.DeleteUserAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) EnsurePluginChatBindings(ctx context.Context, pluginID uuid.UUID) error {
	defer f.observe(ctx, "EnsurePluginChatBindings", time.Now())
	return f.s.EnsurePluginChatBindings(ctx, pluginID)
}

func (f forkQueryMetricsStore) FinishChatWakeupClaim(ctx context.Context, arg forkdb.FinishChatWakeupClaimParams) (forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "FinishChatWakeupClaim", time.Now())
	return f.s.FinishChatWakeupClaim(ctx, arg)
}

func (f forkQueryMetricsStore) GetChatAwaitingWorker(ctx context.Context, arg forkdb.GetChatAwaitingWorkerParams) (bool, error) {
	defer f.observe(ctx, "GetChatAwaitingWorker", time.Now())
	return f.s.GetChatAwaitingWorker(ctx, arg)
}

func (f forkQueryMetricsStore) GetChatOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID) (forkdb.ChatOrganizationSubagentLimit, error) {
	defer f.observe(ctx, "GetChatOrganizationSubagentLimits", time.Now())
	return f.s.GetChatOrganizationSubagentLimits(ctx, organizationID)
}

func (f forkQueryMetricsStore) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "GetChatSubagentByChatID", time.Now())
	return f.s.GetChatSubagentByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetChatSubagentTypesByChatIDs(ctx context.Context, chatIDs []uuid.UUID) ([]forkdb.GetChatSubagentTypesByChatIDsRow, error) {
	defer f.observe(ctx, "GetChatSubagentTypesByChatIDs", time.Now())
	return f.s.GetChatSubagentTypesByChatIDs(ctx, chatIDs)
}

func (f forkQueryMetricsStore) GetChatSubagentsByParentChatID(ctx context.Context, parentChatID uuid.UUID) ([]forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "GetChatSubagentsByParentChatID", time.Now())
	return f.s.GetChatSubagentsByParentChatID(ctx, parentChatID)
}

func (f forkQueryMetricsStore) GetChatSubtreeIDs(ctx context.Context, rootID uuid.UUID) ([]forkdb.GetChatSubtreeIDsRow, error) {
	defer f.observe(ctx, "GetChatSubtreeIDs", time.Now())
	return f.s.GetChatSubtreeIDs(ctx, rootID)
}

func (f forkQueryMetricsStore) GetChatTodosByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatTodo, error) {
	defer f.observe(ctx, "GetChatTodosByChatID", time.Now())
	return f.s.GetChatTodosByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetPendingChatDescendantQuestion(ctx context.Context, chatID uuid.UUID) (forkdb.ChatDescendantQuestion, error) {
	defer f.observe(ctx, "GetPendingChatDescendantQuestion", time.Now())
	return f.s.GetPendingChatDescendantQuestion(ctx, chatID)
}

func (f forkQueryMetricsStore) GetChatWakeupByID(ctx context.Context, id uuid.UUID) (forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "GetChatWakeupByID", time.Now())
	return f.s.GetChatWakeupByID(ctx, id)
}

func (f forkQueryMetricsStore) GetFamilySubagentsAwaitingWorker(ctx context.Context, arg forkdb.GetFamilySubagentsAwaitingWorkerParams) ([]forkdb.GetFamilySubagentsAwaitingWorkerRow, error) {
	defer f.observe(ctx, "GetFamilySubagentsAwaitingWorker", time.Now())
	return f.s.GetFamilySubagentsAwaitingWorker(ctx, arg)
}

func (f forkQueryMetricsStore) GetOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.GetOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "GetOrganizationAgentDefinitionByName", time.Now())
	return f.s.GetOrganizationAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) GetPluginByID(ctx context.Context, id uuid.UUID) (forkdb.Plugin, error) {
	defer f.observe(ctx, "GetPluginByID", time.Now())
	return f.s.GetPluginByID(ctx, id)
}

func (f forkQueryMetricsStore) GetPluginChatBindingsDue(ctx context.Context, arg forkdb.GetPluginChatBindingsDueParams) ([]forkdb.GetPluginChatBindingsDueRow, error) {
	defer f.observe(ctx, "GetPluginChatBindingsDue", time.Now())
	return f.s.GetPluginChatBindingsDue(ctx, arg)
}

func (f forkQueryMetricsStore) GetPluginUserLink(ctx context.Context, arg forkdb.GetPluginUserLinkParams) (forkdb.PluginUserLink, error) {
	defer f.observe(ctx, "GetPluginUserLink", time.Now())
	return f.s.GetPluginUserLink(ctx, arg)
}

func (f forkQueryMetricsStore) GetPluginUserLinksByPluginAndUser(ctx context.Context, arg forkdb.GetPluginUserLinksByPluginAndUserParams) ([]forkdb.PluginUserLink, error) {
	defer f.observe(ctx, "GetPluginUserLinksByPluginAndUser", time.Now())
	return f.s.GetPluginUserLinksByPluginAndUser(ctx, arg)
}

func (f forkQueryMetricsStore) GetPluginUserLinksByPluginID(ctx context.Context, pluginID uuid.UUID) ([]forkdb.PluginUserLink, error) {
	defer f.observe(ctx, "GetPluginUserLinksByPluginID", time.Now())
	return f.s.GetPluginUserLinksByPluginID(ctx, pluginID)
}

func (f forkQueryMetricsStore) GetPluginUserLinksByUserID(ctx context.Context, userID uuid.UUID) ([]forkdb.PluginUserLink, error) {
	defer f.observe(ctx, "GetPluginUserLinksByUserID", time.Now())
	return f.s.GetPluginUserLinksByUserID(ctx, userID)
}

func (f forkQueryMetricsStore) GetPluginsByOrganizationID(ctx context.Context, organizationID uuid.UUID) ([]forkdb.Plugin, error) {
	defer f.observe(ctx, "GetPluginsByOrganizationID", time.Now())
	return f.s.GetPluginsByOrganizationID(ctx, organizationID)
}

func (f forkQueryMetricsStore) GetProjectAgentDefinitionByName(ctx context.Context, arg forkdb.GetProjectAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "GetProjectAgentDefinitionByName", time.Now())
	return f.s.GetProjectAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) GetUserAgentDefinitionByName(ctx context.Context, arg forkdb.GetUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "GetUserAgentDefinitionByName", time.Now())
	return f.s.GetUserAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatSubagent(ctx context.Context, arg forkdb.InsertChatSubagentParams) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "InsertChatSubagent", time.Now())
	return f.s.InsertChatSubagent(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatWakeup(ctx context.Context, arg forkdb.InsertChatWakeupParams) (forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "InsertChatWakeup", time.Now())
	return f.s.InsertChatWakeup(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatWakeupRun(ctx context.Context, arg forkdb.InsertChatWakeupRunParams) (forkdb.ChatWakeupRun, error) {
	defer f.observe(ctx, "InsertChatWakeupRun", time.Now())
	return f.s.InsertChatWakeupRun(ctx, arg)
}

func (f forkQueryMetricsStore) InsertOrganizationAgentDefinition(ctx context.Context, arg forkdb.InsertOrganizationAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "InsertOrganizationAgentDefinition", time.Now())
	return f.s.InsertOrganizationAgentDefinition(ctx, arg)
}

func (f forkQueryMetricsStore) InsertPlugin(ctx context.Context, arg forkdb.InsertPluginParams) (forkdb.Plugin, error) {
	defer f.observe(ctx, "InsertPlugin", time.Now())
	return f.s.InsertPlugin(ctx, arg)
}

func (f forkQueryMetricsStore) InsertPluginLinkCode(ctx context.Context, arg forkdb.InsertPluginLinkCodeParams) error {
	defer f.observe(ctx, "InsertPluginLinkCode", time.Now())
	return f.s.InsertPluginLinkCode(ctx, arg)
}

func (f forkQueryMetricsStore) InsertProjectAgentDefinition(ctx context.Context, arg forkdb.InsertProjectAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "InsertProjectAgentDefinition", time.Now())
	return f.s.InsertProjectAgentDefinition(ctx, arg)
}

func (f forkQueryMetricsStore) InsertUserAgentDefinition(ctx context.Context, arg forkdb.InsertUserAgentDefinitionParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "InsertUserAgentDefinition", time.Now())
	return f.s.InsertUserAgentDefinition(ctx, arg)
}

func (f forkQueryMetricsStore) GetOrganizationAgentDefinitionByNameForUpdate(ctx context.Context, arg forkdb.GetOrganizationAgentDefinitionByNameForUpdateParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "GetOrganizationAgentDefinitionByNameForUpdate", time.Now())
	return f.s.GetOrganizationAgentDefinitionByNameForUpdate(ctx, arg)
}

func (f forkQueryMetricsStore) ListChatWakeupRunKeys(ctx context.Context, wakeupID uuid.UUID) ([]string, error) {
	defer f.observe(ctx, "ListChatWakeupRunKeys", time.Now())
	return f.s.ListChatWakeupRunKeys(ctx, wakeupID)
}

func (f forkQueryMetricsStore) ListChatWakeupsByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "ListChatWakeupsByChatID", time.Now())
	return f.s.ListChatWakeupsByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) ListChildChatIDsWithActiveEventsWakeups(ctx context.Context, parentChatID uuid.UUID) ([]uuid.UUID, error) {
	defer f.observe(ctx, "ListChildChatIDsWithActiveEventsWakeups", time.Now())
	return f.s.ListChildChatIDsWithActiveEventsWakeups(ctx, parentChatID)
}

func (f forkQueryMetricsStore) ListOrganizationAgentDefinitionMetadata(ctx context.Context, organizationID uuid.UUID) ([]forkdb.ListOrganizationAgentDefinitionMetadataRow, error) {
	defer f.observe(ctx, "ListOrganizationAgentDefinitionMetadata", time.Now())
	return f.s.ListOrganizationAgentDefinitionMetadata(ctx, organizationID)
}

func (f forkQueryMetricsStore) ListPendingChatDescendantQuestionsByRoot(ctx context.Context, rootChatID uuid.UUID) ([]forkdb.ListPendingChatDescendantQuestionsByRootRow, error) {
	defer f.observe(ctx, "ListPendingChatDescendantQuestionsByRoot", time.Now())
	return f.s.ListPendingChatDescendantQuestionsByRoot(ctx, rootChatID)
}

func (f forkQueryMetricsStore) ListProjectAgentDefinitionMetadata(ctx context.Context, projectID uuid.UUID) ([]forkdb.ListProjectAgentDefinitionMetadataRow, error) {
	defer f.observe(ctx, "ListProjectAgentDefinitionMetadata", time.Now())
	return f.s.ListProjectAgentDefinitionMetadata(ctx, projectID)
}

func (f forkQueryMetricsStore) ListUserAgentDefinitionMetadata(ctx context.Context, arg forkdb.ListUserAgentDefinitionMetadataParams) ([]forkdb.ListUserAgentDefinitionMetadataRow, error) {
	defer f.observe(ctx, "ListUserAgentDefinitionMetadata", time.Now())
	return f.s.ListUserAgentDefinitionMetadata(ctx, arg)
}

func (f forkQueryMetricsStore) LockChatForNoKeyUpdate(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	defer f.observe(ctx, "LockChatForNoKeyUpdate", time.Now())
	return f.s.LockChatForNoKeyUpdate(ctx, id)
}

func (f forkQueryMetricsStore) ReleasePluginLeases(ctx context.Context, runnerID uuid.UUID) error {
	defer f.observe(ctx, "ReleasePluginLeases", time.Now())
	return f.s.ReleasePluginLeases(ctx, runnerID)
}

func (f forkQueryMetricsStore) SetPluginUserLinkActiveChat(ctx context.Context, arg forkdb.SetPluginUserLinkActiveChatParams) error {
	defer f.observe(ctx, "SetPluginUserLinkActiveChat", time.Now())
	return f.s.SetPluginUserLinkActiveChat(ctx, arg)
}

func (f forkQueryMetricsStore) SetPluginUserLinksActiveChatByUser(ctx context.Context, arg forkdb.SetPluginUserLinksActiveChatByUserParams) error {
	defer f.observe(ctx, "SetPluginUserLinksActiveChatByUser", time.Now())
	return f.s.SetPluginUserLinksActiveChatByUser(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateChatWakeupRunStatus(ctx context.Context, arg forkdb.UpdateChatWakeupRunStatusParams) (forkdb.ChatWakeupRun, error) {
	defer f.observe(ctx, "UpdateChatWakeupRunStatus", time.Now())
	return f.s.UpdateChatWakeupRunStatus(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateChatWakeupStatus(ctx context.Context, arg forkdb.UpdateChatWakeupStatusParams) (forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "UpdateChatWakeupStatus", time.Now())
	return f.s.UpdateChatWakeupStatus(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateOrganizationAgentDefinitionACLByName(ctx context.Context, arg forkdb.UpdateOrganizationAgentDefinitionACLByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "UpdateOrganizationAgentDefinitionACLByName", time.Now())
	return f.s.UpdateOrganizationAgentDefinitionACLByName(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateOrganizationAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateOrganizationAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "UpdateOrganizationAgentDefinitionByName", time.Now())
	return f.s.UpdateOrganizationAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) UpdatePluginChatBindingDelivered(ctx context.Context, arg forkdb.UpdatePluginChatBindingDeliveredParams) error {
	defer f.observe(ctx, "UpdatePluginChatBindingDelivered", time.Now())
	return f.s.UpdatePluginChatBindingDelivered(ctx, arg)
}

func (f forkQueryMetricsStore) UpdatePluginChatBindingFailed(ctx context.Context, arg forkdb.UpdatePluginChatBindingFailedParams) error {
	defer f.observe(ctx, "UpdatePluginChatBindingFailed", time.Now())
	return f.s.UpdatePluginChatBindingFailed(ctx, arg)
}

func (f forkQueryMetricsStore) UpdatePluginConfig(ctx context.Context, arg forkdb.UpdatePluginConfigParams) (forkdb.Plugin, error) {
	defer f.observe(ctx, "UpdatePluginConfig", time.Now())
	return f.s.UpdatePluginConfig(ctx, arg)
}

func (f forkQueryMetricsStore) UpdatePluginEnabled(ctx context.Context, arg forkdb.UpdatePluginEnabledParams) (forkdb.Plugin, error) {
	defer f.observe(ctx, "UpdatePluginEnabled", time.Now())
	return f.s.UpdatePluginEnabled(ctx, arg)
}

func (f forkQueryMetricsStore) UpdatePluginLastError(ctx context.Context, arg forkdb.UpdatePluginLastErrorParams) error {
	defer f.observe(ctx, "UpdatePluginLastError", time.Now())
	return f.s.UpdatePluginLastError(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateProjectAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateProjectAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "UpdateProjectAgentDefinitionByName", time.Now())
	return f.s.UpdateProjectAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateUserAgentDefinitionByName(ctx context.Context, arg forkdb.UpdateUserAgentDefinitionByNameParams) (forkdb.AgentDefinition, error) {
	defer f.observe(ctx, "UpdateUserAgentDefinitionByName", time.Now())
	return f.s.UpdateUserAgentDefinitionByName(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatDescendantQuestion(ctx context.Context, arg forkdb.UpsertChatDescendantQuestionParams) (forkdb.ChatDescendantQuestion, error) {
	defer f.observe(ctx, "UpsertChatDescendantQuestion", time.Now())
	return f.s.UpsertChatDescendantQuestion(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatOrganizationSubagentLimits(ctx context.Context, arg forkdb.UpsertChatOrganizationSubagentLimitsParams) (forkdb.ChatOrganizationSubagentLimit, error) {
	defer f.observe(ctx, "UpsertChatOrganizationSubagentLimits", time.Now())
	return f.s.UpsertChatOrganizationSubagentLimits(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatTodos(ctx context.Context, arg forkdb.UpsertChatTodosParams) (forkdb.ChatTodo, error) {
	defer f.observe(ctx, "UpsertChatTodos", time.Now())
	return f.s.UpsertChatTodos(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertPluginUserLink(ctx context.Context, arg forkdb.UpsertPluginUserLinkParams) (forkdb.PluginUserLink, error) {
	defer f.observe(ctx, "UpsertPluginUserLink", time.Now())
	return f.s.UpsertPluginUserLink(ctx, arg)
}
