package duel

import "fmt"

func notGroupMessage() string {
	return "这个功能还是在群里玩比较合适，我们去群里开一局吧~"
}

func unidentifiedActorMessage() string {
	return "抱歉呀，我一时认不出这位玩家是谁。"
}

func botCannotJoinMessage() string {
	return "机器人就别加入对局啦~"
}

func noPendingDuelMessage() string {
	return "当前还没有等待中的对局呢~"
}

func duelBusyMessage() string {
	return "当前本群已经有一场对局在进行了，先等这局打完吧~"
}

func directChallengeNotFoundMessage() string {
	return "抱歉，您回复的那条消息不在我的记忆里啦~"
}

func unidentifiedTargetMessage() string {
	return "抱歉呀，我暂时认不出被挑战的是哪位玩家。"
}

func selfChallengeMessage() string {
	return "您不能和自己决斗。"
}

func invitedOnlyMessage(name string) string {
	return fmt.Sprintf("这场 1v1 对局还在等 %s 确认，其他玩家先等等哦~", name)
}

func maxPlayersMessage(limit int) string {
	return fmt.Sprintf("这场对局最多支持 %d 名玩家。", limit)
}

func minimumPlayersMessage() string {
	return "至少需要 2 名玩家才能开始对局。"
}

func lobbyControlOnlyHostMessage() string {
	return "这场对局得由房主来操作，其他玩家先等等哦~"
}

func duelAlreadyStartedMessage() string {
	return "对局已经开始了，不能再调整报名啦~"
}

func conflictingControlMessage() string {
	return "开始和取消不能一起发呀。"
}

func fixedDirectChallengeMessage() string {
	return "回复发起的 1v1 对局人数固定为 2，不能再加人啦~"
}

func maxTooSmallForCurrentPlayersMessage() string {
	return "这个人数上限太小了，当前玩家都放不下。"
}

func duelUsageMessage() string {
	return "用法：/duel [--max N] [--start|--cancel]"
}

func maxRequiresValueMessage() string {
	return "请给 --max 带上人数呀。"
}

func maxMustBeIntegerMessage() string {
	return "人数上限得是个整数。"
}

func maxCannotMixWithControlsMessage() string {
	return "调整人数上限和开始/取消要分开说哦。"
}

func lobbyCreatedNote(name string) string {
	return fmt.Sprintf("%s 发起了一场对局，等待其他玩家加入中~", name)
}

func directChallengeCreatedNote(name string) string {
	return fmt.Sprintf("%s 发起了 1v1 对局，等待对方确认加入~", name)
}

func joinedLobbyNote(name string) string {
	return fmt.Sprintf("%s 已加入对局。", name)
}

func autoStartNote() string {
	return "人数已满，对局开始！"
}

func hostStartNote() string {
	return "房主选择提前开始，对局开始！"
}

func lobbyTimeoutCancelNote() string {
	return "等待加入超时了，这场对局先取消吧~"
}

func lobbyTimeoutStartNote() string {
	return "等待时间到了，对局开始！"
}

func cancelLobbyNote(name string) string {
	return fmt.Sprintf("%s 取消了这场对局。", name)
}

func maxUpdatedNote(maxPlayers int) string {
	return fmt.Sprintf("房主把人数上限调整为 %d。", maxPlayers)
}

func hitNote(name string, card card) string {
	return fmt.Sprintf("%s 摸到了一张 %s。", name, card.label)
}

func standNote(name string) string {
	return fmt.Sprintf("%s 选择停牌。", name)
}

func deckEmptyNote() string {
	return "牌堆空了，就按当前点数结算吧~"
}

func loneSurvivorNote() string {
	return "场上只剩 1 名未爆牌玩家，胜负已经分出来啦~"
}

func allBustNote() string {
	return "好家伙，全员爆牌，一个都没跑掉。"
}

func timeoutNote() string {
	return "对局超时了，未完成操作的玩家判负哦~"
}

func tieSubtitle() string {
	return "大家点数一样，这局算平手吧~"
}

func loserFooter(names string) string {
	return fmt.Sprintf("%s，跟我去小黑屋走一趟吧！😈", names)
}

func loserFooterNoMute(names string) string {
	return fmt.Sprintf("%s，这局惜败咯。", names)
}
