#!/bin/bash
# 收尾一次 @claude：Claude 跑成了且有提交，就推分支（新分支再开 PR）；最后把"在处理"那条评论改成 Claude 的回复。
# 要的环境变量：GH_TOKEN、REPO、RUN_URL、AGENT（agent 那一步的 outcome），prepare.sh 的输出 NUM、BRANCH、NEW、BASE、START、CID，
# 以及 RUNNER_TEMP（agent.mjs 写的 claude-reply.md / claude-meta.json 在那里）。
set -euo pipefail

reply=$(cat "$RUNNER_TEMP/claude-reply.md" 2>/dev/null || true)
meta=$(cat "$RUNNER_TEMP/claude-meta.json" 2>/dev/null || echo '{}')
turns=$(jq -r '.turns // 0' <<<"$meta")
cost=$(jq -r '.cost // 0 | . * 100 | round / 100' <<<"$meta")
links="[运行日志]($RUN_URL)"

if [ "$AGENT" = success ]; then
  # Claude 忘了提交的改动也带上
  if [ -n "$(git status --porcelain)" ]; then
    git add -A
    git commit -qm "Claude：#$NUM 里没提交的改动" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
  fi
  if [ "$(git rev-list --count "$START..HEAD")" -gt 0 ]; then
    git push -q "https://x-access-token:$GH_TOKEN@github.com/$REPO.git" "HEAD:refs/heads/$BRANCH"
    links="分支 \`$BRANCH\` · $links"
    if [ -n "$NEW" ]; then
      title=$(git log -1 --format=%s)
      body=$(printf '关联 #%s\n\n%s\n\n🤖 由 Claude（Agent SDK）在 GitHub Actions 里生成' "$NUM" "$reply")
      # 仓库没开"允许 GitHub Actions 创建 PR"时 gh pr create 会失败，就给一个手动开 PR 的链接
      if pr=$(gh pr create -R "$REPO" --base "$BASE" --head "$BRANCH" --title "$title" --body "$body" 2>/dev/null); then
        links="[PR]($pr) · $links"
      else
        links="[开 PR](https://github.com/$REPO/compare/$BASE...$BRANCH?expand=1) · $links"
      fi
    fi
  fi
  body=$(printf '%s\n\n---\n%s · %s 轮，约 $%s' "${reply:-（没有回复）}" "$links" "$turns" "$cost")
else
  body=$(printf 'Claude 没跑完：%s\n\n%s' "${reply:-见运行日志}" "$links")
fi

gh api -X PATCH "repos/$REPO/issues/comments/$CID" -f body="$body" >/dev/null
