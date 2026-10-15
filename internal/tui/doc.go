// Package tui 是 mulix 的全屏接管 TUI 前端——转写视口与流式渲染（markdown、
// 思考、工具卡）、输入与授权交互、状态呈现；经 Sink/SessionSwapper 接缝与
// control 解耦，治理三层语义零改动，组合根 internal/cli 只留装配。
//
// 本包部分内容改编自 DeepSeek-Reasonix (MIT License, © 2026 Reasonix Contributors, https://github.com/esengine/DeepSeek-Reasonix)。
package tui
