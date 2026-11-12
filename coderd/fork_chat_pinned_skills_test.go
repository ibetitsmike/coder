package coderd_test

// This file is fork code: it covers the pinned skill routes of the
// fork-pinned-skills experiment.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatPinnedSkills(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	client := coderdtest.New(t, &coderdtest.Options{
		Database: db,
		Pubsub:   ps,
		DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
			dv.Experiments = []string{string(codersdk.ExperimentForkPinnedSkills)}
		}),
	})
	owner := coderdtest.CreateFirstUser(t, client)
	member, memberUser := coderdtest.CreateAnotherUser(t, client, owner.OrganizationID)
	other, _ := coderdtest.CreateAnotherUser(t, client, owner.OrganizationID)
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: owner.OrganizationID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: owner.OrganizationID, OwnerID: memberUser.ID, LastModelConfigID: model.ID})

	pins, err := member.ChatPinnedSkills(ctx, chat.ID)
	require.NoError(t, err)
	require.NotNil(t, pins)
	require.Empty(t, pins)

	deploy := codersdk.ChatPinnedSkill{Source: codersdk.ChatSkillSourcePersonal, Name: "deploy"}
	review := codersdk.ChatPinnedSkill{Source: codersdk.ChatSkillSourceOrganization, Name: "review"}
	// A repeated pin is stored once, and the list comes back by source and name.
	pins, err = member.UpdateChatPinnedSkills(ctx, chat.ID, codersdk.UpdateChatPinnedSkillsRequest{
		Skills: []codersdk.ChatPinnedSkill{deploy, review, deploy},
	})
	require.NoError(t, err)
	require.Equal(t, []codersdk.ChatPinnedSkill{review, deploy}, pins)
	pins, err = member.ChatPinnedSkills(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, []codersdk.ChatPinnedSkill{review, deploy}, pins)

	// Unpinning sends the list without the skill.
	pins, err = member.UpdateChatPinnedSkills(ctx, chat.ID, codersdk.UpdateChatPinnedSkillsRequest{
		Skills: []codersdk.ChatPinnedSkill{deploy},
	})
	require.NoError(t, err)
	require.Equal(t, []codersdk.ChatPinnedSkill{deploy}, pins)

	tooMany := make([]codersdk.ChatPinnedSkill, chatd.MaxPinnedSkillsPerChat+1)
	for i := range tooMany {
		tooMany[i] = deploy
	}
	for _, skills := range [][]codersdk.ChatPinnedSkill{
		{{Source: "team", Name: "deploy"}},
		{{Source: codersdk.ChatSkillSourcePersonal, Name: "Not A Name"}},
		tooMany,
	} {
		_, err = member.UpdateChatPinnedSkills(ctx, chat.ID, codersdk.UpdateChatPinnedSkillsRequest{Skills: skills})
		_ = requireSDKError(t, err, http.StatusBadRequest)
	}

	// Another member cannot see the chat, so it can neither list nor change
	// its pins.
	_, err = other.ChatPinnedSkills(ctx, chat.ID)
	_ = requireSDKError(t, err, http.StatusNotFound)
	_, err = other.UpdateChatPinnedSkills(ctx, chat.ID, codersdk.UpdateChatPinnedSkillsRequest{})
	_ = requireSDKError(t, err, http.StatusNotFound)

	pins, err = member.UpdateChatPinnedSkills(ctx, chat.ID, codersdk.UpdateChatPinnedSkillsRequest{})
	require.NoError(t, err)
	require.Empty(t, pins)
}

// TestForkChatPinnedSkillsExperimentOff checks that the routes stay closed
// while the experiment is disabled.
func TestForkChatPinnedSkillsExperimentOff(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	client := coderdtest.New(t, &coderdtest.Options{Database: db, Pubsub: ps})
	owner := coderdtest.CreateFirstUser(t, client)
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: owner.OrganizationID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: owner.OrganizationID, OwnerID: owner.UserID, LastModelConfigID: model.ID})

	_, err := client.ChatPinnedSkills(ctx, chat.ID)
	_ = requireSDKError(t, err, http.StatusForbidden)
}
