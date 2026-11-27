package app

import (
	"slices"
	"testing"
)

func TestTTYChildPIDs(t *testing.T) {
	// ps 继承 Holt 的控制终端，即使它是进程组组长也不能拿来认领。
	const table = `
	 19  10  19 ttys009
	 13  10  13 ttys001
	 12  10  12 ??
	 17  10  17 ?
	 16  10  16 -
	 15  11  15 ttys005
	 14  10  11 ttys004
	 10   1  10 ttys001
	 11  10  11 ttys007
	`
	if got, want := ttyChildPIDs(table, 10), []int{11, 19}; !slices.Equal(got, want) {
		t.Fatalf("终端 shell PID = %v，想要 %v", got, want)
	}
}
