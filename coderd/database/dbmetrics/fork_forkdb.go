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

func (f forkQueryMetricsStore) DeleteChatPinnedSkills(ctx context.Context, chatID uuid.UUID) error {
	defer f.observe(ctx, "DeleteChatPinnedSkills", time.Now())
	return f.s.DeleteChatPinnedSkills(ctx, chatID)
}

func (f forkQueryMetricsStore) GetChatPinnedSkillsByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatPinnedSkill, error) {
	defer f.observe(ctx, "GetChatPinnedSkillsByChatID", time.Now())
	return f.s.GetChatPinnedSkillsByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetWorkflowRun(ctx context.Context, id uuid.UUID) (forkdb.WorkflowRun, error) {
	defer f.observe(ctx, "GetWorkflowRun", time.Now())
	return f.s.GetWorkflowRun(ctx, id)
}

func (f forkQueryMetricsStore) GetWorkflowStep(ctx context.Context, id uuid.UUID) (forkdb.WorkflowStep, error) {
	defer f.observe(ctx, "GetWorkflowStep", time.Now())
	return f.s.GetWorkflowStep(ctx, id)
}

func (f forkQueryMetricsStore) GetWorkflowStepsByRun(ctx context.Context, runID uuid.UUID) ([]forkdb.WorkflowStep, error) {
	defer f.observe(ctx, "GetWorkflowStepsByRun", time.Now())
	return f.s.GetWorkflowStepsByRun(ctx, runID)
}

func (f forkQueryMetricsStore) InsertWorkflowRun(ctx context.Context, arg forkdb.InsertWorkflowRunParams) error {
	defer f.observe(ctx, "InsertWorkflowRun", time.Now())
	return f.s.InsertWorkflowRun(ctx, arg)
}

func (f forkQueryMetricsStore) InsertWorkflowStep(ctx context.Context, arg forkdb.InsertWorkflowStepParams) error {
	defer f.observe(ctx, "InsertWorkflowStep", time.Now())
	return f.s.InsertWorkflowStep(ctx, arg)
}

func (f forkQueryMetricsStore) MarkOpenWorkflowRunsInterrupted(ctx context.Context, errText string) (int64, error) {
	defer f.observe(ctx, "MarkOpenWorkflowRunsInterrupted", time.Now())
	return f.s.MarkOpenWorkflowRunsInterrupted(ctx, errText)
}

func (f forkQueryMetricsStore) UpdateWorkflowRunStatus(ctx context.Context, arg forkdb.UpdateWorkflowRunStatusParams) error {
	defer f.observe(ctx, "UpdateWorkflowRunStatus", time.Now())
	return f.s.UpdateWorkflowRunStatus(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateWorkflowStep(ctx context.Context, arg forkdb.UpdateWorkflowStepParams) error {
	defer f.observe(ctx, "UpdateWorkflowStep", time.Now())
	return f.s.UpdateWorkflowStep(ctx, arg)
}

func (f forkQueryMetricsStore) GetWorkflowRunStepCountsByChat(ctx context.Context, chatID uuid.UUID) ([]forkdb.GetWorkflowRunStepCountsByChatRow, error) {
	defer f.observe(ctx, "GetWorkflowRunStepCountsByChat", time.Now())
	return f.s.GetWorkflowRunStepCountsByChat(ctx, chatID)
}

func (f forkQueryMetricsStore) InsertChatFamilyMessage(ctx context.Context, arg forkdb.InsertChatFamilyMessageParams) error {
	defer f.observe(ctx, "InsertChatFamilyMessage", time.Now())
	return f.s.InsertChatFamilyMessage(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatPinnedSkills(ctx context.Context, arg forkdb.InsertChatPinnedSkillsParams) error {
	defer f.observe(ctx, "InsertChatPinnedSkills", time.Now())
	return f.s.InsertChatPinnedSkills(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteChatExcludedPluginPack(ctx context.Context, arg forkdb.DeleteChatExcludedPluginPackParams) error {
	defer f.observe(ctx, "DeleteChatExcludedPluginPack", time.Now())
	return f.s.DeleteChatExcludedPluginPack(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteChatPluginPack(ctx context.Context, arg forkdb.DeleteChatPluginPackParams) error {
	defer f.observe(ctx, "DeleteChatPluginPack", time.Now())
	return f.s.DeleteChatPluginPack(ctx, arg)
}

func (f forkQueryMetricsStore) DeletePluginPackProject(ctx context.Context, arg forkdb.DeletePluginPackProjectParams) error {
	defer f.observe(ctx, "DeletePluginPackProject", time.Now())
	return f.s.DeletePluginPackProject(ctx, arg)
}

func (f forkQueryMetricsStore) GetExcludedPluginPacksByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.GetExcludedPluginPacksByChatIDRow, error) {
	defer f.observe(ctx, "GetExcludedPluginPacksByChatID", time.Now())
	return f.s.GetExcludedPluginPacksByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetPluginPacksByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.GetPluginPacksByChatIDRow, error) {
	defer f.observe(ctx, "GetPluginPacksByChatID", time.Now())
	return f.s.GetPluginPacksByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetPluginPacksByProjectID(ctx context.Context, projectID uuid.UUID) ([]forkdb.GetPluginPacksByProjectIDRow, error) {
	defer f.observe(ctx, "GetPluginPacksByProjectID", time.Now())
	return f.s.GetPluginPacksByProjectID(ctx, projectID)
}

func (f forkQueryMetricsStore) InsertChatExcludedPluginPack(ctx context.Context, arg forkdb.InsertChatExcludedPluginPackParams) (forkdb.ChatExcludedPluginPack, error) {
	defer f.observe(ctx, "InsertChatExcludedPluginPack", time.Now())
	return f.s.InsertChatExcludedPluginPack(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatPluginPack(ctx context.Context, arg forkdb.InsertChatPluginPackParams) (forkdb.ChatPluginPack, error) {
	defer f.observe(ctx, "InsertChatPluginPack", time.Now())
	return f.s.InsertChatPluginPack(ctx, arg)
}

func (f forkQueryMetricsStore) InsertPluginPackProject(ctx context.Context, arg forkdb.InsertPluginPackProjectParams) (forkdb.PluginPackProject, error) {
	defer f.observe(ctx, "InsertPluginPackProject", time.Now())
	return f.s.InsertPluginPackProject(ctx, arg)
}

func (f forkQueryMetricsStore) DeletePluginPackByID(ctx context.Context, id uuid.UUID) error {
	defer f.observe(ctx, "DeletePluginPackByID", time.Now())
	return f.s.DeletePluginPackByID(ctx, id)
}

func (f forkQueryMetricsStore) DeletePluginPackResources(ctx context.Context, packID uuid.UUID) error {
	defer f.observe(ctx, "DeletePluginPackResources", time.Now())
	return f.s.DeletePluginPackResources(ctx, packID)
}

func (f forkQueryMetricsStore) GetPluginPackByID(ctx context.Context, id uuid.UUID) (forkdb.PluginPack, error) {
	defer f.observe(ctx, "GetPluginPackByID", time.Now())
	return f.s.GetPluginPackByID(ctx, id)
}

func (f forkQueryMetricsStore) GetPluginPackResources(ctx context.Context, packID uuid.UUID) ([]forkdb.PluginPackResource, error) {
	defer f.observe(ctx, "GetPluginPackResources", time.Now())
	return f.s.GetPluginPackResources(ctx, packID)
}

func (f forkQueryMetricsStore) GetPluginPackSources(ctx context.Context, organizationID uuid.UUID) (forkdb.PluginPackSource, error) {
	defer f.observe(ctx, "GetPluginPackSources", time.Now())
	return f.s.GetPluginPackSources(ctx, organizationID)
}

func (f forkQueryMetricsStore) GetPluginPacksByOrganizationID(ctx context.Context, organizationID uuid.UUID) ([]forkdb.PluginPack, error) {
	defer f.observe(ctx, "GetPluginPacksByOrganizationID", time.Now())
	return f.s.GetPluginPacksByOrganizationID(ctx, organizationID)
}

func (f forkQueryMetricsStore) InsertPluginPack(ctx context.Context, arg forkdb.InsertPluginPackParams) (forkdb.PluginPack, error) {
	defer f.observe(ctx, "InsertPluginPack", time.Now())
	return f.s.InsertPluginPack(ctx, arg)
}

func (f forkQueryMetricsStore) InsertPluginPackResources(ctx context.Context, arg forkdb.InsertPluginPackResourcesParams) error {
	defer f.observe(ctx, "InsertPluginPackResources", time.Now())
	return f.s.InsertPluginPackResources(ctx, arg)
}

func (f forkQueryMetricsStore) UpdatePluginPackCommit(ctx context.Context, arg forkdb.UpdatePluginPackCommitParams) (forkdb.PluginPack, error) {
	defer f.observe(ctx, "UpdatePluginPackCommit", time.Now())
	return f.s.UpdatePluginPackCommit(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertPluginPackSources(ctx context.Context, arg forkdb.UpsertPluginPackSourcesParams) (forkdb.PluginPackSource, error) {
	defer f.observe(ctx, "UpsertPluginPackSources", time.Now())
	return f.s.UpsertPluginPackSources(ctx, arg)
}

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

func (f forkQueryMetricsStore) CountChatFamilyMessagesSince(ctx context.Context, arg forkdb.CountChatFamilyMessagesSinceParams) (int64, error) {
	defer f.observe(ctx, "CountChatFamilyMessagesSince", time.Now())
	return f.s.CountChatFamilyMessagesSince(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteChatFamilyMessagesBefore(ctx context.Context, arg forkdb.DeleteChatFamilyMessagesBeforeParams) error {
	defer f.observe(ctx, "DeleteChatFamilyMessagesBefore", time.Now())
	return f.s.DeleteChatFamilyMessagesBefore(ctx, arg)
}

func (f forkQueryMetricsStore) GetChatFamilyMessageRecipient(ctx context.Context, arg forkdb.GetChatFamilyMessageRecipientParams) (uuid.UUID, error) {
	defer f.observe(ctx, "GetChatFamilyMessageRecipient", time.Now())
	return f.s.GetChatFamilyMessageRecipient(ctx, arg)
}

func (f forkQueryMetricsStore) CountChatUserNotificationsSince(ctx context.Context, arg forkdb.CountChatUserNotificationsSinceParams) (int64, error) {
	defer f.observe(ctx, "CountChatUserNotificationsSince", time.Now())
	return f.s.CountChatUserNotificationsSince(ctx, arg)
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

func (f forkQueryMetricsStore) DeleteChatSecretAnswer(ctx context.Context, arg forkdb.DeleteChatSecretAnswerParams) error {
	defer f.observe(ctx, "DeleteChatSecretAnswer", time.Now())
	return f.s.DeleteChatSecretAnswer(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteOldChatSecretAnswers(ctx context.Context, limitCount int32) (int64, error) {
	defer f.observe(ctx, "DeleteOldChatSecretAnswers", time.Now())
	return f.s.DeleteOldChatSecretAnswers(ctx, limitCount)
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

func (f forkQueryMetricsStore) GetChatOrganizationWakeupCaps(ctx context.Context, organizationID uuid.UUID) (forkdb.ChatOrganizationWakeupCap, error) {
	defer f.observe(ctx, "GetChatOrganizationWakeupCaps", time.Now())
	return f.s.GetChatOrganizationWakeupCaps(ctx, organizationID)
}

func (f forkQueryMetricsStore) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "GetChatSubagentByChatID", time.Now())
	return f.s.GetChatSubagentByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetChatSubagentMessageRecipient(ctx context.Context, arg forkdb.GetChatSubagentMessageRecipientParams) (uuid.UUID, error) {
	defer f.observe(ctx, "GetChatSubagentMessageRecipient", time.Now())
	return f.s.GetChatSubagentMessageRecipient(ctx, arg)
}

func (f forkQueryMetricsStore) DeleteChatSubagentMessagesBefore(ctx context.Context, arg forkdb.DeleteChatSubagentMessagesBeforeParams) error {
	defer f.observe(ctx, "DeleteChatSubagentMessagesBefore", time.Now())
	return f.s.DeleteChatSubagentMessagesBefore(ctx, arg)
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

func (f forkQueryMetricsStore) GetChatSecretAnswersByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatSecretAnswer, error) {
	defer f.observe(ctx, "GetChatSecretAnswersByChatID", time.Now())
	return f.s.GetChatSecretAnswersByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetChatWakeupByID(ctx context.Context, id uuid.UUID) (forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "GetChatWakeupByID", time.Now())
	return f.s.GetChatWakeupByID(ctx, id)
}

func (f forkQueryMetricsStore) GetChatWakeupByIDForUpdate(ctx context.Context, id uuid.UUID) (forkdb.ChatWakeup, error) {
	defer f.observe(ctx, "GetChatWakeupByIDForUpdate", time.Now())
	return f.s.GetChatWakeupByIDForUpdate(ctx, id)
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

func (f forkQueryMetricsStore) InsertChatSecretAnswer(ctx context.Context, arg forkdb.InsertChatSecretAnswerParams) error {
	defer f.observe(ctx, "InsertChatSecretAnswer", time.Now())
	return f.s.InsertChatSecretAnswer(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatSubagent(ctx context.Context, arg forkdb.InsertChatSubagentParams) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "InsertChatSubagent", time.Now())
	return f.s.InsertChatSubagent(ctx, arg)
}

func (f forkQueryMetricsStore) InsertChatSubagentMessage(ctx context.Context, arg forkdb.InsertChatSubagentMessageParams) error {
	defer f.observe(ctx, "InsertChatSubagentMessage", time.Now())
	return f.s.InsertChatSubagentMessage(ctx, arg)
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

func (f forkQueryMetricsStore) ListChatFamilyMembers(ctx context.Context, arg forkdb.ListChatFamilyMembersParams) ([]forkdb.ListChatFamilyMembersRow, error) {
	defer f.observe(ctx, "ListChatFamilyMembers", time.Now())
	return f.s.ListChatFamilyMembers(ctx, arg)
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

func (f forkQueryMetricsStore) SumChatCostMicrosSince(ctx context.Context, arg forkdb.SumChatCostMicrosSinceParams) (int64, error) {
	defer f.observe(ctx, "SumChatCostMicrosSince", time.Now())
	return f.s.SumChatCostMicrosSince(ctx, arg)
}

func (f forkQueryMetricsStore) SumOrganizationWakeupChatCostMicrosSince(ctx context.Context, arg forkdb.SumOrganizationWakeupChatCostMicrosSinceParams) (int64, error) {
	defer f.observe(ctx, "SumOrganizationWakeupChatCostMicrosSince", time.Now())
	return f.s.SumOrganizationWakeupChatCostMicrosSince(ctx, arg)
}

func (f forkQueryMetricsStore) UpdateChatSubagentWorkspaceID(ctx context.Context, arg forkdb.UpdateChatSubagentWorkspaceIDParams) error {
	defer f.observe(ctx, "UpdateChatSubagentWorkspaceID", time.Now())
	return f.s.UpdateChatSubagentWorkspaceID(ctx, arg)
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

func (f forkQueryMetricsStore) GetWorkflowRunsByChat(ctx context.Context, arg forkdb.GetWorkflowRunsByChatParams) ([]forkdb.WorkflowRun, error) {
	defer f.observe(ctx, "GetWorkflowRunsByChat", time.Now())
	return f.s.GetWorkflowRunsByChat(ctx, arg)
}

func (f forkQueryMetricsStore) FailOpenWorkflowStepsOfRun(ctx context.Context, arg forkdb.FailOpenWorkflowStepsOfRunParams) (int64, error) {
	defer f.observe(ctx, "FailOpenWorkflowStepsOfRun", time.Now())
	return f.s.FailOpenWorkflowStepsOfRun(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatDescendantQuestion(ctx context.Context, arg forkdb.UpsertChatDescendantQuestionParams) (forkdb.ChatDescendantQuestion, error) {
	defer f.observe(ctx, "UpsertChatDescendantQuestion", time.Now())
	return f.s.UpsertChatDescendantQuestion(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatOrganizationSubagentLimits(ctx context.Context, arg forkdb.UpsertChatOrganizationSubagentLimitsParams) (forkdb.ChatOrganizationSubagentLimit, error) {
	defer f.observe(ctx, "UpsertChatOrganizationSubagentLimits", time.Now())
	return f.s.UpsertChatOrganizationSubagentLimits(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatOrganizationWakeupCaps(ctx context.Context, arg forkdb.UpsertChatOrganizationWakeupCapsParams) (forkdb.ChatOrganizationWakeupCap, error) {
	defer f.observe(ctx, "UpsertChatOrganizationWakeupCaps", time.Now())
	return f.s.UpsertChatOrganizationWakeupCaps(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertChatTodos(ctx context.Context, arg forkdb.UpsertChatTodosParams) (forkdb.ChatTodo, error) {
	defer f.observe(ctx, "UpsertChatTodos", time.Now())
	return f.s.UpsertChatTodos(ctx, arg)
}

func (f forkQueryMetricsStore) UpsertPluginUserLink(ctx context.Context, arg forkdb.UpsertPluginUserLinkParams) (forkdb.PluginUserLink, error) {
	defer f.observe(ctx, "UpsertPluginUserLink", time.Now())
	return f.s.UpsertPluginUserLink(ctx, arg)
}
