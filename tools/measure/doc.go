// Package measure 是量内存的脚本：measure.sh 起一次真的 Holt、每隔几秒记 phys_footprint 和子 shell，
// report.py 汇总成表格的行和折线，longrun.sh 是 8 h 的壳。这里的 Go 只有它们的测试（TestMeasureScripts）。
// 用法和规矩见 AGENTS.md。
package measure
