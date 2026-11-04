#!/bin/bash
# 收尾一次 @claude：有提交就推分支（跑成了的新分支再开 PR）；最后把"在处理"那条评论改成 Claude 的回复。
# 没跑完（到了轮数或花费上限、出错）也把做了一半的改动推到分支上留着，但不开 PR、不推到 PR 的分支。
# 要的环境变量：GH_TOKEN、REPO、RUN_URL、AGENT（agent 那一步的 outcome），prepare.sh 的输出 NUM、BRANCH、NEW、BASE、START、CID，
# 以及 RUNNER_TEMP（agent.mjs 写的 claude-reply.md / claude-meta.json 在那里）。
set -euo pipefail

reply=$(cat "$RUNNER_TEMP/claude-reply.md" 2>/dev/null || true)
meta=$(cat "$RUNNER_TEMP/claude-meta.json" 2>/dev/null || echo '{}')
turns=$(jq -r '.turns // 0' <<<"$meta")
cost=$(jq -r '.cost // 0 | . * 100 | round / 100' <<<"$meta")
links="[运行日志]($RUN_URL)"

ok=""
[ "$AGENT" = success ] && ok=1
branch=$BRANCH
# 没跑完时不往 PR 的分支上推半成品，另开一个分支
if [ -z "$ok" ] && [ -z "$NEW" ]; then
  branch="claude/wip-pr-${NUM}-$(date -u +%Y%m%d-%H%M%S)"
fi

# Claude 忘了提交（或没来得及提交）的改动也带上
if [ -n "$(git status --porcelain)" ]; then
  git add -A
  git commit -qm "Claude：#${NUM} 里没提交的改动" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
fi
pushed=1
if [ "$(git rev-list --count "$START..HEAD")" -gt 0 ]; then
  # 先打成 bundle（工作流把它传成运行的附件 claude-work）：推送失败时改动还在，`git fetch claude-work.bundle HEAD` 就能取回
  git bundle create "$RUNNER_TEMP/claude-work.bundle" "$START..HEAD" 2>/dev/null || true
  # GitHub 偶尔回 500（remote rejected / Internal Server Error）：退避重试，别让一小时的活儿白干
  pushed=""
  for wait in 0 15 30 60 120; do
    sleep "$wait"
    if git push -q "https://x-access-token:$GH_TOKEN@github.com/$REPO.git" "HEAD:refs/heads/$branch"; then
      pushed=1
      break
    fi
    echo "推送 $branch 失败（这次之前等了 ${wait}s），再试" >&2
  done
fi
if [ -z "$pushed" ]; then
  links="推送分支 \`$branch\` 失败（重试了 5 次），改动在运行附件 \`claude-work\` 的 bundle 里 · $links"
elif [ "$(git rev-list --count "$START..HEAD")" -gt 0 ]; then
  links="分支 \`$branch\` · $links"
  compare="https://github.com/$REPO/compare/$BASE...$branch?expand=1"
  if [ -n "$ok" ] && [ -n "$NEW" ]; then
    title=$(git log -1 --format=%s)
    body=$(printf '关联 #%s\n\n%s\n\n🤖 由 Claude（Agent SDK）在 GitHub Actions 里生成' "$NUM" "$reply")
    # 仓库没开"允许 GitHub Actions 创建 PR"时 gh pr create 会失败，就给一个手动开 PR 的链接
    if pr=$(gh pr create -R "$REPO" --base "$BASE" --head "$branch" --title "$title" --body "$body" 2>/dev/null); then
      links="[PR]($pr) · $links"
    else
      links="[开 PR]($compare) · $links"
    fi
  elif [ -z "$ok" ]; then
    links="[看改动]($compare) · $links"
  fi
fi

if [ -n "$ok" ]; then
  body=$(printf '%s\n\n---\n%s · %s 轮，约 $%s' "${reply:-（没有回复）}" "$links" "$turns" "$cost")
else
  body=$(printf 'Claude 没跑完：%s\n\n---\n%s · %s 轮，约 $%s' "${reply:-见运行日志}" "$links" "$turns" "$cost")
fi

gh api -X PATCH "repos/$REPO/issues/comments/$CID" -f body="$body" >/dev/null
[ -n "$pushed" ] # 推送失败：评论已经写上了，这一步仍算失败
