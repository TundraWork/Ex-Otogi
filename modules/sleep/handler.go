package sleep

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ex-otogi/pkg/otogi"
)

const (
	minSleepDuration = 1 * time.Second
	maxSleepDuration = 12 * time.Hour
)

func (m *Module) handleSleep(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != otogi.EventKindCommandReceived {
		return nil
	}
	if event.Command.Name != sleepCommandName {
		return nil
	}

	duration, err := parseSleepDuration(strings.TrimSpace(event.Command.Value))
	if err != nil {
		return m.replyError(ctx, event, err.Error())
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("sleep derive outbound target: %w", err)
	}

	userID := event.Actor.ID
	if userID == "" {
		return m.replyError(ctx, event, "unable to identify user")
	}

	untilDate := time.Now().Add(duration)
	code, err := m.codeManager.Generate(codeScopeFromEvent(event), untilDate)
	if err != nil {
		return fmt.Errorf("sleep generate wake code: %w", err)
	}

	if err := m.moderation.RestrictMember(ctx, otogi.RestrictMemberRequest{
		Target:      target,
		MemberID:    userID,
		Permissions: mutedPermissions(),
		UntilDate:   untilDate,
	}); err != nil {
		return m.replyPermissionChangeFailure(
			ctx,
			event,
			"没能开始休息，暂时无法修改你的权限，请稍后再试。",
			fmt.Errorf("sleep restrict member %s: %w", userID, err),
		)
	}

	humanDuration := formatDuration(duration)

	sleepText := fmt.Sprintf("好好休息%s吧～祝好梦哦✨\n要提前回来的话，可以在当前会话发送以下命令：", humanDuration)
	if _, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             sleepText,
		ReplyToMessageID: event.Article.ID,
	}); err != nil {
		return fmt.Errorf("sleep send sleep message: %w", err)
	}

	wakeCommand := "/wake " + code
	wakeLen := utf8.RuneCountInString(wakeCommand)
	if _, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target: target,
		Text:   wakeCommand,
		Entities: []otogi.TextEntity{
			{
				Type:   otogi.TextEntityTypePre,
				Offset: 0,
				Length: wakeLen,
			},
		},
		ReplyToMessageID: event.Article.ID,
	}); err != nil {
		return fmt.Errorf("sleep send wake code message: %w", err)
	}

	return nil
}

func (m *Module) handleWake(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != otogi.EventKindCommandReceived {
		return nil
	}
	if event.Command.Name != wakeCommandName {
		return nil
	}

	code := strings.TrimSpace(event.Command.Value)
	if code == "" {
		return m.replyError(ctx, event, "请提供唤醒码。")
	}

	userID := event.Actor.ID
	if userID == "" {
		return m.replyError(ctx, event, "unable to identify user")
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("sleep derive wake target: %w", err)
	}

	if err := m.codeManager.Validate(code, codeScopeFromEvent(event), time.Now()); err != nil {
		return m.replyError(ctx, event, "唤醒码无效或已过期，请检查后重新发送。")
	}
	if target.Conversation.Type == "" {
		return fmt.Errorf("sleep derive wake target: missing conversation type")
	}

	if err := m.moderation.RestrictMember(ctx, otogi.RestrictMemberRequest{
		Target:      target,
		MemberID:    userID,
		Permissions: otogi.AllPermissionsGranted(),
		UntilDate:   time.Now(),
	}); err != nil {
		return m.replyPermissionChangeFailure(
			ctx,
			event,
			"没能恢复你的权限，请稍后再试。",
			fmt.Errorf("sleep unrestrict member %s: %w", userID, err),
		)
	}

	if _, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target: target,
		Text:   "咦，有坏孩子不好好睡觉跑回来了，是谁呢～😉",
	}); err != nil {
		return fmt.Errorf("sleep send wake announcement: %w", err)
	}

	return nil
}

func codeScopeFromEvent(event *otogi.Event) codeScope {
	if event == nil {
		return codeScope{}
	}

	return codeScope{
		UserID:           event.Actor.ID,
		ConversationID:   event.Conversation.ID,
		ConversationType: event.Conversation.Type,
		SourcePlatform:   event.Source.Platform,
		SourceID:         event.Source.ID,
	}
}

func (m *Module) replyError(ctx context.Context, event *otogi.Event, message string) error {
	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("sleep reply error derive target: %w", err)
	}

	_, sendErr := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             message,
		ReplyToMessageID: event.Article.ID,
	})
	if sendErr != nil {
		return fmt.Errorf("sleep send error reply: %w", sendErr)
	}

	return nil
}

func (m *Module) replyPermissionChangeFailure(
	ctx context.Context,
	event *otogi.Event,
	message string,
	cause error,
) error {
	replyErr := m.replyError(ctx, event, message)
	if replyErr != nil {
		return errors.Join(cause, fmt.Errorf("sleep reply permission failure: %w", replyErr))
	}

	return cause
}

// mutedPermissions returns permissions that block all message-sending capabilities
// but still allow inviting users and pinning messages.
func mutedPermissions() otogi.MemberPermissions {
	return otogi.MemberPermissions{
		SendMessages: false,
		SendMedia:    false,
		SendPolls:    false,
		SendOther:    false,
		EmbedLinks:   false,
		ChangeInfo:   true,
		InviteUsers:  true,
		PinMessages:  true,
		ManageTopics: true,
	}
}

// parseSleepDuration parses the user-provided duration value.
//
// It accepts Go duration strings (e.g. "1h30m", "30m", "3600s") and
// plain integers interpreted as seconds.
func parseSleepDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("请提供休息时长，范围为 1 秒到 12 小时。例如：/sleep 30m")
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		seconds, numErr := strconv.Atoi(value)
		if numErr != nil {
			return 0, fmt.Errorf("无法解析时长 %q。请使用格式如 30m、1h30m 或秒数。", value)
		}
		duration = time.Duration(seconds) * time.Second
	}

	if duration < minSleepDuration || duration > maxSleepDuration {
		return 0, fmt.Errorf("休息时长必须在 1 秒到 12 小时之间。")
	}

	return duration, nil
}

// formatDuration converts a duration to a human-readable Chinese string.
func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d小时", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d分钟", minutes))
	}
	if seconds > 0 && hours == 0 {
		parts = append(parts, fmt.Sprintf("%d秒", seconds))
	}

	if len(parts) == 0 {
		return "0秒"
	}

	return strings.Join(parts, "")
}
