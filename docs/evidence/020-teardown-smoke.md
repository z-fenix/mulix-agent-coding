# 020-bus-endpoint-session-teardown 真机冒烟证据(FR-108 惯例)

**日期:** 2026-10-14 | **平台:** Windows 11 / Git Bash | **引擎:** engine.lock 钉定
**对照场景:** docs/evidence/018-events-smoke.md(同形 enable=true run——018 时代 endpoint 残留)

## 基线(接线前,mulix020a.exe @ cc0a684 后)

- 工作区:`[eventbus] enable="true"` + engine.lock;`mulix run "回复:好"` 正常完成(exit 0)。
- `.mulix/bus/`:`endpoint` + `jetstream/` ——**endpoint 残留实证**(018 已知行为复现)。

## 接线后(mulix020b.exe,main 出口接 CloseSharedBus)

- 同场景重跑(手工删除残留后纯净复测):run 正常完成(exit 0,`[引擎已退出] graceful`)。
- `.mulix/bus/`:仅 `jetstream/` ——**endpoint 已清**(CloseSharedBus→Bus.Close→019 删除能力生产可达)。

## 门禁

`go test ./... -count=1` exit 0(26 包);vet/gofmt 干净。

## 结论

FR-101 达成:正常退出后 endpoint 清痕,观察面不再读到死端点。退出码语义不变(exit 0 路径实证;execErr 路径未触碰)。
