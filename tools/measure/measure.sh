#!/bin/zsh
# 量内存（0.18.4）：起一次真的 Grove（0.18.5 以前叫 Workbench，老的构建照样能量），每隔几秒记它的 phys_footprint 和子 shell，跑够了 TERM 掉、打印汇总。
#
#   measure.sh <标签> <Grove.app> [-w 工作区文件] [-s 种子 state.json] [-a 搜索词] [-t all] [-m 分钟] [-i 采样秒]
#
# -w 默认 $WORKBENCH_TEST_WS（没设就必须给 -w）；-s 默认 seed/idle.json；-a 设 WORKBENCH_ALLOPEN（全开，跑够的分钟数从日志里"全开："那行算起）；
# -t all 设 WORKBENCH_TERMS=all；-m 默认 5；-i 默认 5。结果在 $MEASURE_OUT/<标签>/（默认 $TMPDIR/workbench-measure）：
#   samples.csv（sec,footprint_bytes,shells,shell_rss_bytes；头 15 s 每 0.5 s 一行）、top.txt（起来 15 s 后 top -l 7 -s 10）、front.txt（前台应用换了就记一行）、
#   meta.txt、state/ logs/（WORKBENCH_STATE / WORKBENCH_LOGS，从种子造的，不碰用户真的状态目录）、git.before / git.after、summary.txt。
#
# 规矩（AGENTS.md "量内存"）：用户的输入空闲 ≥ $SMOKE_IDLE 秒（默认 60）才起，不够每 20 s 再看，最多等 $MEASURE_WAIT 秒
# （默认 2700 = 45 min），到了写 SKIPPED、退出码 3；起了以后不往窗口送任何事件；对仓库只跑 git status / log / diff-tree。
# 种子里的 @WS@ 换成工作区文件、@VERSION@ 换成应用版本、@REPOn@ 换成工作区第 n 个仓库的根、["@ALL@"] 换成全部仓库根、
# "@LAST:n@" 换成第 n 个仓库最近一次非合并提交里的一个文件（Diff 标签的 change；仓库没有未提交的改动时全开也有 Diff）。
# 窗口位置用 seed/window-state.json（固定 1440×880，和以前量的时候一样大），日志里每帧一行（MYGO_FRAME_STATS=all），汇总里数帧。
# 用 open -g -n --env 起（和 M 系列"无焦点"一样）：窗口开在后面、不拿键盘；环境变量用 --env 传，输出进 run.log。
# MEASURE_DRY=1：只造好 state.json 就退出（不等空闲、不起应用；测试用）。

set -u
zmodload zsh/datetime

here=${0:A:h}
usage() { print -u2 "用法：measure.sh <标签> <Grove.app> [-w 工作区文件] [-s 种子] [-a 搜索词] [-t all] [-m 分钟] [-i 采样秒]"; exit 2; }
(( $# >= 2 )) || usage
label=$1 appdir=${2:a}
shift 2
ws=${WORKBENCH_TEST_WS:-} seed=$here/seed/idle.json query= terms= minutes=5 interval=5
while getopts w:s:a:t:m:i: opt; do
	case $opt in
	w) ws=$OPTARG ;;
	s) seed=$OPTARG ;;
	a) query=$OPTARG ;;
	t) terms=$OPTARG ;;
	m) minutes=$OPTARG ;;
	i) interval=$OPTARG ;;
	*) usage ;;
	esac
done
ws=${ws:a} seed=${seed:a}
exe=$(defaults read "$appdir/Contents/Info" CFBundleExecutable 2>/dev/null) # 0.18.5 起是 Grove，以前 Workbench
bin=$appdir/Contents/MacOS/${exe:-${${appdir:t}%.app}}
[[ -x $bin ]] || { print -u2 "没有 $bin"; exit 2; }
[[ -f $ws ]] || { print -u2 "没有工作区文件 $ws"; exit 2; }
[[ -f $seed ]] || { print -u2 "没有种子 $seed"; exit 2; }
version=$(defaults read "$appdir/Contents/Info" CFBundleShortVersionString 2>/dev/null)
out=${MEASURE_OUT:-${TMPDIR:-/tmp}/workbench-measure}
dir=$out/$label
[[ -e $dir ]] && { print -u2 "已经有 $dir：换个标签，或者先挪走"; exit 2; }

# 工作区的仓库根；种子换成这一次的 state.json（对仓库只读：git log / diff-tree）
roots=("${(@f)$(python3 -I - $ws <<'PY'
import json, os, re, sys
ws = sys.argv[1]
text = open(ws, encoding="utf-8").read()
try:
    d = json.loads(text)
except ValueError:  # .code-workspace 可以有注释和多余的逗号
    text = re.sub(r"^\s*//.*$", "", text, flags=re.M)
    d = json.loads(re.sub(r",(\s*[}\]])", r"\1", text))
for f in d.get("folders", []):
    print(os.path.normpath(os.path.join(os.path.dirname(ws), f["path"])))
PY
)}")
[[ -n ${roots[1]:-} ]] || { print -u2 "工作区 $ws 里没有仓库"; exit 2; }
mkdir -p $dir/state $dir/logs
python3 -I - $seed $dir/state/state.json $ws "$version" $roots <<'PY' || { print -u2 "种子 $seed 换不了"; exit 2; }
import json, subprocess, sys
seed, dst, ws, version, roots = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5:]

def git(root, *args):
    return subprocess.run(["git", "-C", root, "--no-optional-locks", *args], capture_output=True, text=True, check=True).stdout

def last(n):
    root = roots[n]
    sha, *par = git(root, "log", "-1", "--no-merges", "--format=%H %P").split()
    rows = [l.split("\t") for l in git(root, "diff-tree", "--root", "--no-commit-id", "-r", "--name-status", sha).splitlines() if l]
    code, path = next((r[:2] for r in rows if r[0] == "M"), rows[0][:2])
    return {"kind": "commit", "path": path, "code": code[0], "commit": sha, "parent": par[0] if par else ""}

def sub(v):
    if isinstance(v, dict):
        return {sub(k): sub(x) for k, x in v.items()}
    if isinstance(v, list):
        return list(roots) if v == ["@ALL@"] else [sub(x) for x in v]
    if not isinstance(v, str):
        return v
    if v.startswith("@LAST:"):
        return last(int(v[6:-1]))
    v = v.replace("@WS@", ws).replace("@VERSION@", version)
    for i in reversed(range(len(roots))):
        v = v.replace(f"@REPO{i}@", roots[i])
    return v

state = sub(json.load(open(seed, encoding="utf-8")))
json.dump(state, open(dst, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
PY
cp $here/seed/window-state.json $dir/state/window-state.json
[[ -n ${MEASURE_DRY:-} ]] && { print "MEASURE_DRY：$dir/state/state.json"; exit 0; }

idle() { ioreg -c IOHIDSystem | awk '/HIDIdleTime/ {print int($NF/1000000000); exit}'; }
need=${SMOKE_IDLE:-60} cap=${MEASURE_WAIT:-2700} waited=0
while (( $(idle) < need )); do
	if (( waited >= cap )); then
		print "SKIPPED $label：用户一直在用（等了 $waited s，$(date '+%F %T')）" | tee $dir/SKIPPED
		exit 3
	fi
	sleep 20
	(( waited += 20 ))
done
print "用户空闲 $(idle) s（等了 $waited s），$(date '+%T') 起 $label"

snap() { for r in $roots; do print "== $r"; git -C $r --no-optional-locks status --porcelain=v2 --branch 2>&1; done; }
snap > $dir/git.before
{
	print "label=$label"
	print "app=$appdir"
	print "exe=${bin:t}"
	print "version=$version"
	print "workspace=$ws"
	print "seed=$seed"
	print "allopen=$query"
	print "terms=$terms"
	print "minutes=$minutes"
	print "interval=$interval"
	print "start=$(date '+%F %T')"
	print "uptime_before=$(uptime)"
} > $dir/meta.txt

# open -g：不到前台（直接跑二进制的话应用自己会到前台，抢走用户的键盘）；-n：另起一个，不碰用户开着的那个。
# 起的不是子进程：pid 是起之前没有、起之后有的那个同一路径的进程。
mine() { pgrep -f "^$bin( |\$)" | sort -n; }
had=(${(f)"$(mine)"})
envs=(--env WORKBENCH_WORKSPACE=$ws --env WORKBENCH_STATE=$dir/state --env WORKBENCH_LOGS=$dir/logs --env MYGO_FRAME_STATS=all)
[[ -n $query ]] && envs+=(--env WORKBENCH_ALLOPEN=$query)
[[ -n $terms ]] && envs+=(--env WORKBENCH_TERMS=$terms)
open -g -n -a $appdir --stdout $dir/run.log --stderr $dir/run.log $envs || { print -u2 "open 起不来 $appdir"; exit 2; }
t0=$EPOCHREALTIME pid= toppid=
for _ in {1..100}; do
	pid=${${(f)"$(mine)"}:|had}
	pid=${pid%% *}
	[[ -n $pid ]] && break
	sleep 0.1
done
[[ -n $pid ]] || { print -u2 "10 s 了还没看到 $bin 的新进程"; exit 2; }
print "pid=$pid" >> $dir/meta.txt
cleanup() { [[ -n $toppid ]] && kill $toppid 2>/dev/null; kill -TERM $pid 2>/dev/null; }
trap 'cleanup; exit 130' INT TERM HUP

print "sec,footprint_bytes,shells,shell_rss_bytes" > $dir/samples.csv
shells='^-?(zsh|bash|sh|fish|dash|ksh|tcsh|nu)$'
log=$dir/logs/workbench.log
total=$(( minutes * 60 )) base=0 waiting=0 next=0 alive=1 front=
[[ -n $query ]] && waiting=1
while true; do
	now=$(( EPOCHREALTIME - t0 ))
	kill -0 $pid 2>/dev/null || { alive=0; break; }
	if (( now >= next )); then
		fp=$(footprint -p $pid --noCategories -f bytes 2>/dev/null | awk '/phys_footprint:/ {print $2; exit}')
		sh=$(ps -A -o ppid=,rss=,comm= | awk -v p=$pid -v re=$shells '$1 == p { n = split($3, a, "/"); if (a[n] ~ re) { c++; r += $2 } } END { printf "%d,%d", c, r * 1024 }')
		[[ -n $fp ]] && printf '%.1f,%s,%s\n' $now $fp $sh >> $dir/samples.csv
		f=$(lsappinfo info -only pid "$(lsappinfo front)" 2>/dev/null | awk '/"pid"=/ { sub(/.*"pid"=/, ""); print $1; exit }
			{ for (i = 1; i < NF - 1; i++) if ($i == "pid" && $(i + 1) == "=") { print $(i + 2); exit } }')
		if [[ $f != $front ]]; then # 前台换了：量的那个到了前台的那段光标会闪、帧数涨，不算
			front=$f
			print "${now%.*} $f $([[ $f == $pid ]] && print ${bin:t} || ps -o comm= -p ${f:-0} 2>/dev/null)" >> $dir/front.txt
		fi
		# 头 15 s 每 0.5 s 一个点：启动峰值（Metal 的 drawable）不到 2.5 s 就放掉，5 s 一个点抓不到；之后对齐到采样间隔的整数倍
		if (( now < 15 )); then (( next = now + 0.5 )); else (( next = (${now%.*} / interval + 1) * interval )); fi
	fi
	if [[ -z $toppid ]] && (( now >= 15 )); then
		top -l 7 -s 10 -pid $pid -stats pid,cpu,mem > $dir/top.txt 2>&1 &
		toppid=$!
	fi
	if (( waiting )); then # 全开：跑够的分钟数从"全开："那行算起，3 min 还没有就从启动算
		if grep -q '全开：' $log 2>/dev/null; then
			waiting=0 base=${now%.*}
			print "allopen_at=$base" >> $dir/meta.txt
		elif (( now >= 180 )); then
			waiting=0
			print "allopen_at=none" >> $dir/meta.txt
		fi
	fi
	if (( ! waiting && now >= base + total )); then
		[[ -n $toppid ]] && kill -0 $toppid 2>/dev/null || break # top 还在采就等它采完
	fi
	sleep $(( now < 15 ? 0.1 : 0.5 ))
done
[[ -n $toppid ]] && wait $toppid 2>/dev/null
if (( alive )); then
	kill -TERM $pid 2>/dev/null
	for _ in {1..40}; do kill -0 $pid 2>/dev/null || break; sleep 0.5; done
	kill -0 $pid 2>/dev/null && { print "TERM 后 20 s 没退，KILL" | tee -a $dir/meta.txt; kill -KILL $pid; }
fi
(( alive )) || print "EXITED EARLY：应用在量完之前自己退了（看 $dir/run.log）" | tee -a $dir/meta.txt
trap - INT TERM HUP
{
	print "end=$(date '+%F %T')"
	print "uptime_after=$(uptime)"
} >> $dir/meta.txt

snap > $dir/git.after
if ! diff -u $dir/git.before $dir/git.after > $dir/git.diff; then
	print "REPO CHANGED：量前量后 git status 不一样（$dir/git.diff）"
fi
grep -E '开回窗口|打不开工作区' $log 2>/dev/null && print "RESTORE FAILED"
python3 -I $here/report.py summary $dir | tee $dir/summary.txt
