// 在 GitHub Actions 里跑一次 Claude（Claude Agent SDK）。用法：node agent.mjs <提示词文件> <回复文件> <摘要文件>
// 提示词从文件读；Claude 最后一条消息写进回复文件（贴到 issue / PR 评论里），轮数、花费写进摘要文件。
// 仓库是公开的，Actions 日志谁都能看，所以日志里只打工具名和一行参数，不打 Claude 的原文。
import { query } from "@anthropic-ai/claude-agent-sdk";
import { readFileSync, writeFileSync } from "node:fs";

const [promptFile, replyFile, metaFile] = process.argv.slice(2);
if (!metaFile) {
  console.error("用法：node agent.mjs <提示词文件> <回复文件> <摘要文件>");
  process.exit(2);
}

// 能读能改能跑命令。Bash 不细分：Claude 本来就能 go run 任意代码，按命令名拦挡不住什么，只会白耗轮数；
// 这一步拿不到 GitHub token（checkout 不留凭据），推送和开 PR 由 finish.sh 做。没列的工具一律拒绝（dontAsk）。
const allowedTools = ["Read", "Glob", "Grep", "Edit", "Write", "Bash"];

// brief 是工具参数的一行摘要（日志用）。
function brief(input) {
  const s = input?.command ?? input?.file_path ?? input?.pattern ?? input?.path ?? "";
  return String(s).replace(/\s+/g, " ").slice(0, 120);
}

let result, err;
try {
  for await (const m of query({
    prompt: readFileSync(promptFile, "utf8"),
    options: {
      model: "claude-opus-5-5",
      systemPrompt: { type: "preset", preset: "claude_code" },
      settingSources: ["project"],
      allowedTools,
      permissionMode: "dontAsk",
      maxTurns: 100, // 一次最多 100 轮（一次工具调用算一轮），防止跑飞
      maxBudgetUsd: 5, // 一次最多花约 5 美元
    },
  })) {
    if (m.type === "assistant") {
      for (const b of m.message.content) {
        if (b.type === "tool_use") console.log(`· ${b.name} ${brief(b.input)}`);
      }
    } else if (m.type === "result") {
      result = m;
    }
  }
} catch (e) {
  err = String(e?.message ?? e); // 到轮数 / 花费上限时 SDK 可能先给了 result 再抛错：有就留着，里面有轮数和花费
}
result ??= { subtype: "exception", is_error: true, errors: [err] };

const ok = !!result && result.subtype === "success" && !result.is_error;
const reply = result.result || result.errors?.join("\n") || err || "（没有结果）";
const meta = {
  ok,
  subtype: result?.subtype ?? "none",
  turns: result?.num_turns ?? 0,
  cost: result?.total_cost_usd ?? 0,
};
writeFileSync(replyFile, reply);
writeFileSync(metaFile, JSON.stringify(meta));
console.log(JSON.stringify(meta));
if (!ok) console.log(`出错：${reply.slice(0, 300)}`);
process.exit(ok ? 0 : 1);
