package control

import (
	"mulix/internal/event"
	"mulix/internal/sessionx"
)

// RecoveryRequired 扫描镜像判定崩溃恢复（design §7.4/§10）：存在未闭合回合
// → required，引导 `mulix --resume` 续链。判据 = TurnStarted 与 TurnDone 的
// 计数差（两者恰 1:1 是 Controller 钉死的不变量：result/send_failed/
// engine_exited/engine_closed 各收口路径恰发布一个 TurnDone）；openTurn 为
// 最近未闭合回合号（started 计数，回合号自 1 顺序递增）。返回 err 仅在镜像
// 不可读时（fail-closed：读不了镜像就无从判定恢复）。
func RecoveryRequired(store *sessionx.Store) (required bool, openTurn int64, err error) {
	events, err := store.ReadAllEvents()
	if err != nil {
		return false, 0, err
	}
	var started, done int64
	for _, ev := range events {
		switch ev.(type) {
		case event.TurnStartedEvent:
			started++
		case event.TurnDoneEvent:
			done++
		}
	}
	if started > done {
		return true, started, nil
	}
	return false, 0, nil
}
