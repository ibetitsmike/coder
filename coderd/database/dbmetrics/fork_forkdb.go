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

func (f forkQueryMetricsStore) ConsumePluginLinkCode(ctx context.Context, arg forkdb.ConsumePluginLinkCodeParams) (forkdb.PluginLinkCode, error) {
	defer f.observe(ctx, "ConsumePluginLinkCode", time.Now())
	return f.s.ConsumePluginLinkCode(ctx, arg)
}

func (f forkQueryMetricsStore) CountActiveSubagentsForCapacity(ctx context.Context, arg forkdb.CountActiveSubagentsForCapacityParams) (forkdb.CountActiveSubagentsForCapacityRow, error) {
	defer f.observe(ctx, "CountActiveSubagentsForCapacity", time.Now())
	return f.s.CountActiveSubagentsForCapacity(ctx, arg)
}

func (f forkQueryMetricsStore) CountLiveSubagentsInFamily(ctx context.Context, rootChatID uuid.UUID) (int64, error) {
	defer f.observe(ctx, "CountLiveSubagentsInFamily", time.Now())
	return f.s.CountLiveSubagentsInFamily(ctx, rootChatID)
}

func (f forkQueryMetricsStore) DeleteExpiredPluginLinkCodes(ctx context.Context) error {
	defer f.observe(ctx, "DeleteExpiredPluginLinkCodes", time.Now())
	return f.s.DeleteExpiredPluginLinkCodes(ctx)
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

func (f forkQueryMetricsStore) EnsurePluginChatBindings(ctx context.Context, pluginID uuid.UUID) error {
	defer f.observe(ctx, "EnsurePluginChatBindings", time.Now())
	return f.s.EnsurePluginChatBindings(ctx, pluginID)
}

func (f forkQueryMetricsStore) GetChatOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID) (forkdb.ChatOrganizationSubagentLimit, error) {
	defer f.observe(ctx, "GetChatOrganizationSubagentLimits", time.Now())
	return f.s.GetChatOrganizationSubagentLimits(ctx, organizationID)
}

func (f forkQueryMetricsStore) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "GetChatSubagentByChatID", time.Now())
	return f.s.GetChatSubagentByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) GetChatSubtreeIDs(ctx context.Context, rootID uuid.UUID) ([]forkdb.GetChatSubtreeIDsRow, error) {
	defer f.observe(ctx, "GetChatSubtreeIDs", time.Now())
	return f.s.GetChatSubtreeIDs(ctx, rootID)
}

func (f forkQueryMetricsStore) GetChatTodosByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatTodo, error) {
	defer f.observe(ctx, "GetChatTodosByChatID", time.Now())
	return f.s.GetChatTodosByChatID(ctx, chatID)
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

func (f forkQueryMetricsStore) InsertChatSubagent(ctx context.Context, arg forkdb.InsertChatSubagentParams) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "InsertChatSubagent", time.Now())
	return f.s.InsertChatSubagent(ctx, arg)
}

func (f forkQueryMetricsStore) InsertPlugin(ctx context.Context, arg forkdb.InsertPluginParams) (forkdb.Plugin, error) {
	defer f.observe(ctx, "InsertPlugin", time.Now())
	return f.s.InsertPlugin(ctx, arg)
}

func (f forkQueryMetricsStore) InsertPluginLinkCode(ctx context.Context, arg forkdb.InsertPluginLinkCodeParams) error {
	defer f.observe(ctx, "InsertPluginLinkCode", time.Now())
	return f.s.InsertPluginLinkCode(ctx, arg)
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
