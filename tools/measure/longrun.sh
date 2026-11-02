#!/bin/zsh
# 开一天（8 h）的内存：measure.sh 的壳，-m 480、每 30 s 一个点，其余参数照传。只写不跑（v1 最长跑 2 h），给用户自己起：
#   tools/measure/longrun.sh day build/darwin-arm64/Grove.app
# 起了以后别碰那个窗口（它在后台量着）；跑完 summary.txt 里有"第 1 h 中位 vs 最后 1 h 中位"（涨没涨），report.py plot 出折线。
set -u
(( $# >= 2 )) || { print -u2 "用法：longrun.sh <标签> <Grove.app> [measure.sh 的其余参数]"; exit 2; }
exec ${0:A:h}/measure.sh "$1" "$2" -m 480 -i 30 "${@:3}"
