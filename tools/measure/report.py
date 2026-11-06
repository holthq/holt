#!/usr/bin/env python3
"""量内存的汇总（0.18.4）：读 measure.sh 一次的目录（samples.csv、meta.txt、top.txt、logs/workbench.log）。

  python3 -I report.py summary <目录>          一次的汇总（measure.sh 跑完自己调）
  python3 -I report.py table <目录>...         README 总表的行（Markdown，MiB 一位小数）
  python3 -I report.py plot <out.png> <目录>... 折线图（没有 matplotlib 就只说一声）
  python3 -I report.py --selftest

MiB = 字节 / 1 048 576；和活动监视器、10-28 的记录对照时用十进制 MB（字节 / 1 000 000）。
"""

import os
import re
import statistics
import sys
import tempfile
from datetime import datetime

MIB = 1048576
PEAK_WINDOW = 10  # 启动峰值：前 10 s
SETTLE = 60  # 稳定段：起来（全开时从"全开："那行）60 s 以后
HOUR = 3600
FRONT_GRACE = 5  # 到过前台的那段之后再不算几秒


class Run:
    def __init__(self, path):
        self.path = path
        self.meta = {}
        with open(os.path.join(path, "meta.txt"), encoding="utf-8") as f:
            for line in f:
                k, _, v = line.rstrip("\n").partition("=")
                self.meta[k] = v
        self.label = self.meta.get("label") or os.path.basename(path)
        self.samples = []  # (秒, 字节, shell 个数, shell RSS 字节)；头 15 s 每 0.5 s 一个点，秒带一位小数
        with open(os.path.join(path, "samples.csv"), encoding="utf-8") as f:
            next(f, None)
            for line in f:
                parts = line.strip().split(",")
                if len(parts) == 4:
                    self.samples.append((float(parts[0]), *(int(x) for x in parts[1:])))
        at = self.meta.get("allopen_at", "")
        self.base = int(at) if at.isdigit() else 0
        self.cpu = top_cpu(os.path.join(path, "top.txt"), self.meta.get("pid", ""))
        self.lines, self.frames = read_log(os.path.join(path, "logs", "workbench.log"), self.meta.get("start", ""))
        self.front = read_front(os.path.join(path, "front.txt"))

    def peak(self):
        early = [s for s in self.samples if s[0] <= PEAK_WINDOW]
        return max(early, key=lambda s: s[1]) if early else None

    def stable(self):
        """稳定段：SETTLE 以后，去掉这个 Holt 在前台的那几段（连同回到后面以后 FRONT_GRACE 秒，帧内存放得慢）。"""
        spans = front_spans(self)
        return [s for s in self.samples if s[0] >= self.base + SETTLE and not any(a <= s[0] <= b + FRONT_GRACE for a, b in spans)]

    def halves(self):
        """跑了约 2 h 以上：第 1 h vs 最后 1 h 的中位数（2 h 时就是第 2 h，8 h 时是第 8 h）；20 min 以上：稳定段前一半 vs 后一半；再短的 None。"""
        st = self.stable()
        if not st:
            return None
        end = st[-1][0]
        if end - self.base >= 2 * HOUR - 60: # 最后一个点可能差一个采样间隔才到 2 h
            a = [s[1] for s in st if s[0] < self.base + HOUR]
            b = [s[1] for s in st if s[0] > end - HOUR]
            names = ("第 1 h", "最后 1 h")
        elif end - self.base >= 20 * 60:
            mid = len(st) // 2
            a, b = [s[1] for s in st[:mid]], [s[1] for s in st[mid:]]
            names = ("前一半", "后一半")
        else:
            return None
        if not a or not b:
            return None
        return names, statistics.median(a), statistics.median(b)

    def ready(self):
        for line in self.lines:
            m = re.match(r"全部就绪 (\d+) ms", line)
            if m:
                return int(m.group(1))
        return None


def mib(b):
    return b / MIB


def top_cpu(path, pid):
    """top -l 7 -s 10 -pid 的 %CPU：头一次是 0（top 自己的规矩），取后面几次的平均；没有 None。"""
    if not pid or not os.path.exists(path):
        return None
    vals = []
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            parts = line.split()
            if len(parts) >= 2 and parts[0] == pid:
                try:
                    vals.append(float(parts[1]))
                except ValueError:
                    pass
    vals = vals[1:]
    return sum(vals) / len(vals) if vals else None


def read_log(path, start):
    """日志里的就绪行、全开行（去掉前面的日期时间），和每一帧（MYGO_FRAME_STATS=all）离启动几秒。"""
    lines, frames = [], []
    if not os.path.exists(path):
        return lines, frames
    t0 = datetime.strptime(start, "%Y-%m-%d %H:%M:%S") if start else None
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            m = re.match(r"^(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d) (.*)$", line.rstrip("\n"))
            stamp, text = (m.group(1), m.group(2)) if m else ("", line.rstrip("\n"))
            if text.startswith(("就绪 ", "全部就绪", "全开：")):
                lines.append(text)
            elif text.startswith("mygo: frame ") and t0 and stamp:
                frames.append((datetime.strptime(stamp, "%Y/%m/%d %H:%M:%S") - t0).total_seconds())
    return lines, frames


def read_front(path):
    """front.txt：前台换成谁的那一刻（秒, pid, 名字）；量的那个自己在前台时名字是可执行文件名（Holt，老的构建是 Grove / Workbench），别的是 ps 的 comm（路径）。"""
    out = []
    if os.path.exists(path):
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                parts = line.split(None, 2)
                if len(parts) >= 2 and parts[0].isdigit():
                    out.append((int(parts[0]), parts[1], parts[2].strip() if len(parts) > 2 else "?"))
    return out


def front_spans(run):
    """量的那个（meta 里的 pid）在前台的几段 [(起, 止)]；到量完还在前台的止是最后一个点。
    别的 Holt（别的会话的冒烟）到前台不算：那时这个在后面。"""
    spans, since = [], None
    end = run.samples[-1][0] if run.samples else 0
    pid = run.meta.get("pid", "")
    for sec, who, name in run.front:
        mine = who == pid if pid else name == run.meta.get("exe", "Holt")
        if mine and since is None:
            since = sec
        elif not mine and since is not None:
            spans.append((since, sec))
            since = None
    if since is not None:
        spans.append((since, end))
    return spans


def summary(run):
    m = run.meta
    out = [f"{run.label}：{m.get('exe', 'Workbench')} {m.get('version', '?')}，{m.get('start', '?')} 起，{len(run.samples)} 个点（每 {m.get('interval', '?')} s）"]
    load = re.search(r"load averages?: (.*)$", m.get("uptime_before", ""))
    if load:
        out.append(f"  起前负载 {load.group(1)}")
    if run.meta.get("allopen", ""):
        at = m.get("allopen_at", "?")
        out.append(f"  全开（{m['allopen']}）：{'没打出那行' if at == 'none' else f'第 {at} s 打出那行'}")
    p = run.peak()
    if p:
        out.append(f"  启动峰值（前 {PEAK_WINDOW} s）{mib(p[1]):.1f} MiB（第 {p[0]:g} s）")
    st = run.stable()
    if st:
        v = [s[1] for s in st]
        out.append(f"  稳定（第 {run.base + SETTLE} s 起，{len(st)} 个点）中位 {mib(statistics.median(v)):.1f}、最大 {mib(max(v)):.1f}、"
                   f"最后 {mib(v[-1]):.1f} MiB（十进制 {statistics.median(v) / 1e6:.1f} MB）")
    else:
        out.append("  稳定段没有点（跑得太短）")
    h = run.halves()
    if h:
        (na, nb), a, b = h
        out.append(f"  {na}中位 {mib(a):.1f} vs {nb}中位 {mib(b):.1f}（{mib(b - a):+.1f} MiB）")
    if run.samples:
        last = run.samples[-1]
        out.append(f"  shell {last[2]} 个，合计 RSS {mib(last[3]):.1f} MiB")
    out.append(f"  CPU（起来 15 s 后 top 6 次平均）{run.cpu:.2f}%" if run.cpu is not None else "  CPU 没采到")
    if run.frames:
        late = sum(1 for t in run.frames if t >= run.base + SETTLE)
        out.append(f"  帧：共 {len(run.frames)} 帧，最后一帧第 {run.frames[-1]:.0f} s，稳定段里 {late} 帧")
    spans = front_spans(run)
    if spans:
        out.append("  " + run.meta.get("exe", "Holt") + " 到过前台（这几段光标闪、帧数涨，不算）：" + "、".join(f"第 {a:g}–{b:g} s" for a, b in spans))
    elif run.front:
        out.append("  一直在后台")
    out.extend("  " + line for line in run.lines)
    for k in ("exit",):
        if k in m:
            out.append(f"  退出码 {m[k]}")
    return "\n".join(out)


TABLE_HEAD = ("| 标签 | 版本 | 启动峰值 | 稳定中位 | 最大 | 最后 | shell（个 / MiB） | CPU | 全部就绪 |\n"
              "| --- | --- | --- | --- | --- | --- | --- | --- | --- |")


def table_row(run):
    p, st = run.peak(), [s[1] for s in run.stable()]
    last = run.samples[-1] if run.samples else (0, 0, 0, 0)
    cell = lambda v: f"{mib(v):.1f}" if v is not None else "—"
    ready = run.ready()
    return "| {} | {} | {} | {} | {} | {} | {} / {:.1f} | {} | {} |".format(
        run.label + ("（到过前台）" if front_spans(run) else ""), run.meta.get("version", "?"), cell(p[1] if p else None),
        cell(statistics.median(st) if st else None), cell(max(st) if st else None), cell(st[-1] if st else None),
        last[2], mib(last[3]), f"{run.cpu:.2f}%" if run.cpu is not None else "—",
        f"{ready} ms" if ready is not None else "—")


def plot(png, runs):
    try:
        import matplotlib
        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
    except ImportError:
        print("没有 matplotlib，不出图")
        return False
    fig, ax = plt.subplots(figsize=(9, 4.5))
    for run in runs:
        ax.plot([s[0] / 60 for s in run.samples], [mib(s[1]) for s in run.samples], label=run.label, linewidth=1.4)
    ax.set_xlabel("min")
    ax.set_ylabel("phys_footprint (MiB)")
    ax.grid(alpha=0.3)
    ax.legend()
    fig.tight_layout()
    fig.savefig(png, dpi=144)
    print(f"图：{png}")
    return True


def selftest():
    with tempfile.TemporaryDirectory() as d:
        os.makedirs(os.path.join(d, "logs"))
        with open(os.path.join(d, "meta.txt"), "w", encoding="utf-8") as f:
            f.write("label=idle\nversion=0.18.4\nstart=2026-11-01 20:00:00\ninterval=5\npid=4242\nallopen=\n"
                    "uptime_before=20:00  up 3 days, 2 users, load averages: 1.50 1.40 1.30\nexit=0\n")
        with open(os.path.join(d, "samples.csv"), "w", encoding="utf-8") as f:
            f.write("sec,footprint_bytes,shells,shell_rss_bytes\n")
            f.write(f"0.0,{100 * MIB},1,{5 * MIB}\n2.5,{230 * MIB},1,{5 * MIB}\n")  # 头 15 s 每 0.5 s（只写一个）
            for sec in range(5, 1800 + 1, 5):
                fp = 220 * MIB if sec == 5 else (100 * MIB if sec < 60 else (88 * MIB if sec < 960 else 90 * MIB))
                f.write(f"{sec}.0,{fp},1,{5 * MIB}\n")
        with open(os.path.join(d, "top.txt"), "w", encoding="utf-8") as f:
            f.write("Processes: 1\nPID    %CPU MEM\n4242   0.0  88M\n")
            for v in (0.4, 0.6, 0.5, 0.5, 0.4, 0.6):
                f.write(f"Load Avg: 1.0\n\nPID    %CPU MEM\n4242   {v}  88M\n")
        with open(os.path.join(d, "logs", "workbench.log"), "w", encoding="utf-8") as f:
            f.write("2026/11/01 20:00:01 打开 x\n2026/11/01 20:00:02 就绪 a：状态 12 ms、树没读、历史没读\n"
                    "2026/11/01 20:00:02 全部就绪 812 ms（窗口 300 ms）\n"
                    "2026/11/01 20:00:01 mygo: frame 1 took 9.0 ms: build 1\n2026/11/01 20:01:30 mygo: frame 2 took 1.0 ms: build 1\n")
        with open(os.path.join(d, "front.txt"), "w", encoding="utf-8") as f:
            f.write("0 19923 /Applications/Claude.app/Contents/MacOS/Claude\n")
        run = Run(d)
        assert run.peak() == (2.5, 230 * MIB, 1, 5 * MIB), run.peak()
        assert len(run.stable()) == (1800 - 60) // 5 + 1
        (na, nb), a, b = run.halves()
        assert (na, mib(a), mib(b)) == ("前一半", 88, 90), (na, a, b)
        assert abs(run.cpu - 0.5) < 1e-9, run.cpu
        assert run.ready() == 812
        s = summary(run)
        for want in ("启动峰值（前 10 s）230.0 MiB（第 2.5 s）", "中位 88.0、最大 90.0", "前一半中位 88.0 vs 后一半中位 90.0（+2.0 MiB）",
                     "shell 1 个，合计 RSS 5.0 MiB", "0.50%", "全部就绪 812 ms", "起前负载 1.50 1.40 1.30",
                     "帧：共 2 帧，最后一帧第 90 s，稳定段里 1 帧", "一直在后台"):
            assert want in s, (want, s)
        row = table_row(run)
        assert row == "| idle | 0.18.4 | 230.0 | 88.0 | 90.0 | 90.0 | 1 / 5.0 | 0.50% | 812 ms |", row

        # 全开：稳定段从"全开："那行起算；2 h 以上比第 1 h 和最后 1 h
        with open(os.path.join(d, "meta.txt"), "a", encoding="utf-8") as f:
            f.write("allopen=TODO\nallopen_at=30\n")
        with open(os.path.join(d, "samples.csv"), "a", encoding="utf-8") as f:
            for sec in range(1805, 7300, 5):
                f.write(f"{sec},{(90 if sec < 3630 else 95) * MIB},3,{15 * MIB}\n")
        run = Run(d)
        assert run.base == 30 and run.stable()[0][0] == 90
        (na, nb), a, b = run.halves()
        assert (na, nb) == ("第 1 h", "最后 1 h") and mib(a) == 90 and mib(b) == 95, (na, nb, a, b)
        assert "全开（TODO）：第 30 s 打出那行" in summary(run)
        with open(os.path.join(d, "front.txt"), "a", encoding="utf-8") as f:
            f.write("100 4242 Workbench\n130 19923 /Applications/Claude.app/Contents/MacOS/Claude\n"
                    "200 5151 /tmp/x/Workbench.app/Contents/MacOS/Workbench\n7000 4242 Workbench\n")  # 5151 是别的 Holt
        run = Run(d)
        assert front_spans(run) == [(100, 130), (7000, 7295)], front_spans(run)
        st = [s[0] for s in run.stable()]
        assert 95 in st and 140 in st and not [x for x in st if 100 <= x <= 135 or x >= 7000], st  # 到量完还在前台的：最后一个点也不算
        assert "第 100–130 s、第 7000–7295 s" in summary(run) and table_row(run).startswith("| idle（到过前台） |")
    print("selftest ok")


def main(argv):
    if argv[:1] == ["--selftest"]:
        selftest()
        return 0
    if len(argv) >= 2 and argv[0] == "summary":
        print(summary(Run(argv[1])))
        return 0
    if len(argv) >= 2 and argv[0] == "table":
        print(TABLE_HEAD)
        for p in argv[1:]:
            print(table_row(Run(p)))
        return 0
    if len(argv) >= 3 and argv[0] == "plot":
        plot(argv[1], [Run(p) for p in argv[2:]])
        return 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
