package chatd

// This file is fork code: it puts the skills pinned to a chat in the prompt
// (fork-pinned-skills experiment). syncForkContext calls pinnedSkillsMessage.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// MaxPinnedSkillsPerChat bounds the skills one chat can pin, like the skills
// one message can invoke.
const MaxPinnedSkillsPerChat = MaxSkillInvocationsPerMessage

// pinnedSkillsBudgetBytes bounds the skill bodies one pinned skills message
// holds, so the pins of a chat take no more prompt than one skill of the
// largest size.
const pinnedSkillsBudgetBytes = workspacesdk.MaxSkillMetaBytes

// pinnedSkillsHeader starts the model-only message that holds the pinned
// skills, and with pinnedSkillsFingerprintLine identifies one in the prompt.
const pinnedSkillsHeader = "The user pinned these skills to this chat. Follow their instructions on every turn until they are unpinned. This list replaces any earlier list of pinned skills."

const pinnedSkillsFingerprintLine = "Pins: "

// pinnedSkillsNone is the list of a chat whose pins are all gone, so a model
// that still sees an earlier list in the prompt stops following it.
const pinnedSkillsNone = "(none)"

// pinnedSkillTagPattern matches opening and closing <pinned-skill> tags, so a
// body cannot end its block early.
var pinnedSkillTagPattern = regexp.MustCompile(`(?i)<\s*/?\s*pinned-skill\b[^>]*>`)

// pinnedSkillsMessage returns the model-only message that puts the chat's
// pinned skills in the prompt, or ok=false when none is due. Like the todo
// snapshot, it is appended only when the prompt does not show the current
// list, so the cached prompt prefix stays intact, and a compaction that drops
// it brings it back on the next step. The fingerprint covers the loaded
// bodies, so an edited, deleted or disabled skill changes it too.
func (p *Server) pinnedSkillsMessage(ctx context.Context, chat database.Chat, history []database.ChatMessage) (chatstate.Message, bool, error) {
	if !p.experiments.Enabled(codersdk.ExperimentForkPinnedSkills) {
		return chatstate.Message{}, false, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("fork queries: %w", err)
	}
	pins, err := fdb.GetChatPinnedSkillsByChatID(ctx, chat.ID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("read pinned skills: %w", err)
	}
	// Without pins, only a list the model may still see needs replacing, and
	// most chats never had one, so skip the prompt read for them.
	if len(pins) == 0 {
		if _, found := latestPinnedSkillsFingerprint(history); !found {
			return chatstate.Message{}, false, nil
		}
	}
	promptRows, err := p.db.GetChatMessagesForPromptByChatID(ctx, chat.ID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("load prompt messages: %w", err)
	}
	if deferred, err := forkContextDeferred(chat, promptRows); err != nil || deferred {
		return chatstate.Message{}, false, err
	}
	list, err := p.renderPinnedSkills(ctx, chat, pins)
	if err != nil {
		return chatstate.Message{}, false, err
	}
	sum := sha256.Sum256([]byte(list))
	fingerprint := hex.EncodeToString(sum[:8])
	shown, found := latestPinnedSkillsFingerprint(promptRows)
	if found && shown == fingerprint {
		return chatstate.Message{}, false, nil
	}
	if !found && list == pinnedSkillsNone {
		return chatstate.Message{}, false, nil
	}
	message, err := forkContextTextMessage(chat, pinnedSkillsHeader+"\n"+pinnedSkillsFingerprintLine+fingerprint+"\n"+list)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("marshal pinned skills: %w", err)
	}
	return message, true, nil
}

// renderPinnedSkills loads every pin with the chat owner's permissions, the
// way a skill invocation loads it, and renders the bodies that fit the
// budget. A pin the owner cannot use is skipped, since a skill can be deleted,
// disabled or unshared after it was pinned.
func (p *Server) renderPinnedSkills(ctx context.Context, chat database.Chat, pins []forkdb.ChatPinnedSkill) (string, error) {
	var sb strings.Builder
	var omitted []string
	for _, pin := range pins {
		resolved, err := p.resolveSkillInvocations(ctx, chat, []codersdk.ChatMessagePart{
			codersdk.ChatMessageSkillInvocation(codersdk.ChatSkillSource(pin.SkillSource), pin.SkillName),
		})
		var unavailable *SkillInvocationUnavailableError
		if errors.As(err, &unavailable) {
			continue
		}
		if err != nil {
			return "", xerrors.Errorf("load pinned skill %s/%s: %w", pin.SkillSource, pin.SkillName, err)
		}
		block := pinnedSkillBlock(resolved[0], chat.WorkspaceID)
		if sb.Len()+len(block) > pinnedSkillsBudgetBytes {
			omitted = append(omitted, chatprompt.SkillInvocationAlias(resolved[0]))
			continue
		}
		_, _ = sb.WriteString(block)
	}
	if len(omitted) > 0 {
		_, _ = sb.WriteString("Not shown because the pinned skills exceed the size limit: " + strings.Join(omitted, ", ") + "\n")
	}
	if sb.Len() == 0 {
		return pinnedSkillsNone, nil
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

// pinnedSkillBlock renders one loaded skill like an invoked skill, with the
// directory of a workspace skill only while the chat stays bound to the
// workspace it was loaded from.
func pinnedSkillBlock(part codersdk.ChatMessagePart, chatWorkspaceID uuid.NullUUID) string {
	body := strings.TrimSpace(codersdk.SanitizePromptText(part.SkillBody))
	body = pinnedSkillTagPattern.ReplaceAllStringFunc(body, func(tag string) string {
		return "&lt;" + tag[1:len(tag)-1] + "&gt;"
	})
	openTag := `<pinned-skill name="` + html.EscapeString(chatprompt.SkillInvocationAlias(part)) + `"`
	if part.SkillDir != "" && chatWorkspaceID.Valid && part.SkillWorkspaceID == chatWorkspaceID.UUID {
		openTag += ` dir="` + html.EscapeString(part.SkillDir) + `"`
	}
	return openTag + ">\n" + body + "\n</pinned-skill>\n"
}

// latestPinnedSkillsFingerprint returns the fingerprint of the last pinned
// skills message in messages. Only model-only user rows count, and users
// cannot author those, so a chat message cannot forge one.
func latestPinnedSkillsFingerprint(messages []database.ChatMessage) (string, bool) {
	texts := memoryIndexTexts(messages)
	for i := len(texts) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(texts[i], pinnedSkillsHeader+"\n"+pinnedSkillsFingerprintLine)
		if !ok {
			continue
		}
		fingerprint, _, _ := strings.Cut(rest, "\n")
		return fingerprint, true
	}
	return "", false
}
