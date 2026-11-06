package testutil

import "errors"

// ErrNoFIFO 是这个平台没有命名管道（Windows）：调用 MakeFIFO 的测试把它当成"这条测不了"，跳过。
var ErrNoFIFO = errors.New("testutil: 这个平台没有 FIFO")
