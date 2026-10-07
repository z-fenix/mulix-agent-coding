#!/usr/bin/env bash
# check-cache-impact.sh — 缓存影响门禁（design §8.3；本地与 CI 同源）。
#
# 用途：diff 触及缓存敏感路径（internal/cache、engine、control、registry、
# docs 的 prompt 模板）时，要求 PR/commit 描述携带两行元数据：
#   Cache-impact: <说明本次改动对缓存构造的影响>
#   Cache-guard: <说明以哪条护栏测试/基线证明不变量（或 "none" 说明为何不需要）>
# 缺任一行 → 拒绝（退出 1）。
#
# 用法：
#   scripts/check-cache-impact.sh [BASE_SHA] [HEAD_SHA]
#   无参 = 相对 origin/master 或 HEAD~1 的默认 diff（见 DEFAULT_BASE）。
#
# 环境变量（CI 注入；本地可不设）：
#   PR_BODY_FILE  — 含 PR 描述的文件路径；缺省用 git log 的合并消息。
set -euo pipefail

# 缓存敏感路径（design §8.3：system prompt 生产者/工具面冻结/记账口径的实现面）。
CACHE_SENSITIVE=(
  "internal/cache/"
  "internal/engine/"
  "internal/control/"
  "internal/registry/"
  "internal/tools/"
  "docs/prompt/"
)

required_keys=("Cache-impact:" "Cache-guard:")

# ---- 取 diff 文件列表（fail-closed：git 出错即拒绝，终评 M-7）----
if [[ $# -ge 2 ]]; then
  BASE="$1"; HEAD="$2"
elif [[ $# -eq 1 ]]; then
  BASE="$1"; HEAD="HEAD"
else
  # 默认基线：CI 常态是 origin/master；本地单提交场景回退 HEAD~1。
  if git rev-parse --verify --quiet origin/master >/dev/null 2>&1; then
    BASE="origin/master"; HEAD="HEAD"
  else
    BASE="HEAD~1"; HEAD="HEAD"
  fi
fi

files=$(git diff --name-only "$BASE" "$HEAD" -- "${CACHE_SENSITIVE[@]}")
if [[ -z "$files" ]]; then
  echo "check-cache-impact: diff 未触及缓存敏感路径 — 通过"
  exit 0
fi

echo "check-cache-impact: 以下缓存敏感路径被触及："
echo "$files" | sed 's/^/  - /'

# ---- 描述文本：PR body 文件优先，否则合并/HEAD 提交消息 ----
body=""
if [[ -n "${PR_BODY_FILE:-}" && -f "$PR_BODY_FILE" ]]; then
  body=$(cat "$PR_BODY_FILE")
else
  body=$(git log --format=%B "$BASE".."$HEAD" 2>/dev/null || git log --format=%B -1)
fi

missing=0
for key in "${required_keys[@]}"; do
  if ! grep -q "^${key}" <<<"$body"; then
    echo "check-cache-impact: 缺少元数据行「${key}」" >&2
    missing=1
  fi
done

if [[ "$missing" -ne 0 ]]; then
  cat >&2 <<'EOF'
check-cache-impact: 拒绝 — 缓存敏感改动必须说明影响与护栏（design §8.3）。
请在 PR/提交描述中加入两行：
  Cache-impact: <本次改动对缓存构造（C1-C5）的影响说明>
  Cache-guard: <护栏测试名或基线证明；确无则写 none + 理由>
EOF
  exit 1
fi

echo "check-cache-impact: 元数据齐备 — 通过"
