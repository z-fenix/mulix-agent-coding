#!/usr/bin/env bash
# check-engine-lock.sh — engine.lock 格式校验（本地与 CI 同源；T10.2）。
#
# 校验契约（对齐 internal/engine LoadEngineLock 的最小约定 + 元数据完整性）：
#   1. 文件存在
#   2. JSON 语法合法（有 jq 时严格校验；无 jq 降级为键检查）
#   3. cli_version 非空
#   4. pinned_at 非空
#   5. protocol_probe.init_version_field 非空
#
# 用法：scripts/check-engine-lock.sh [engine.lock 路径，默认 ./engine.lock]
set -euo pipefail

LOCK="${1:-engine.lock}"

fail() { echo "check-engine-lock: $1" >&2; exit 1; }

[[ -f "$LOCK" ]] || fail "$LOCK 不存在（T5：engine.lock pin 引擎版本）"

if command -v jq >/dev/null 2>&1; then
  jq -e . "$LOCK" >/dev/null 2>&1 || fail "$LOCK 不是合法 JSON"
  echo "check-engine-lock: JSON 语法合法（jq）"
else
  echo "check-engine-lock: jq 不可用，降级为键检查（JSON 语法由 CI 端校验）"
fi

require_key() {
  local key="$1"
  # 提取键值：匹配 "key": "value"（值非空、非占位）
  local val
  val=$(grep -E "\"${key}\"[[:space:]]*:" "$LOCK" | head -n1 | sed -E "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"?([^\"]*)\"?.*/\1/")
  [[ -n "$val" ]] || fail "\"${key}\" 缺失或为空"
  echo "check-engine-lock: ${key} = ${val}"
}

require_key "cli_version"
require_key "pinned_at"
require_key "init_version_field"

echo "check-engine-lock: 通过"
