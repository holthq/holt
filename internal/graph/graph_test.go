package graph

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

// commit 是喂给 Lanes 的一个提交：哈希和父哈希。
type commit struct {
	hash    string
	parents []string
}

// c 写起来短一点：c("M", "B", "F2") 是父提交为 B、F2 的 M。
func c(hash string, parents ...string) commit { return commit{hash, parents} }

// run 一次喂完 cs，返回每一行。
func run(cs []commit) []Row {
	var l Lanes
	return feed(&l, cs)
}

func feed(l *Lanes, cs []commit) []Row {
	rows := make([]Row, 0, len(cs))
	for _, cm := range cs {
		rows = append(rows, l.Next(cm.hash, cm.parents))
	}
	return rows
}

// connected 查线是连着的：上一行每条线的 To 在这一行要么是节点、要么有从它出发的线；
// 这一行出发的线（节点那条以外）都得是上一行连下来的；节点不在上一行连下来的泳道上，就只能是没人等的分支顶。
func connected(t *testing.T, cs []commit, rows []Row) {
	t.Helper()
	waited := map[string]bool{}
	for k, row := range rows {
		in := map[int]bool{}
		if k > 0 {
			for _, e := range rows[k-1].Edges {
				in[e.To] = true
			}
		}
		out := map[int]bool{}
		for _, e := range row.Edges {
			out[e.From] = true
			if e.From != row.Lane && !in[e.From] {
				t.Errorf("第 %d 行（%s）：泳道 %d 的线凭空出现", k, cs[k].hash, e.From)
			}
			// 不是节点的泳道只在汇入下一行的节点时拐弯
			if e.From != row.Lane && e.From != e.To && (k+1 == len(rows) || e.To != rows[k+1].Lane) {
				t.Errorf("第 %d 行（%s）：不是节点的泳道 %d 拐弯了 %v", k, cs[k].hash, e.From, e)
			}
		}
		for j := range in {
			if j != row.Lane && !out[j] {
				t.Errorf("第 %d 行（%s）：上一行连到泳道 %d 的线断了", k, cs[k].hash, j)
			}
		}
		if in[row.Lane] != waited[cs[k].hash] {
			t.Errorf("第 %d 行（%s）：有子提交等它 = %v，上一行连到节点 = %v", k, cs[k].hash, waited[cs[k].hash], in[row.Lane])
		}
		for _, p := range cs[k].parents {
			waited[p] = true
		}
	}
}

func TestNext(t *testing.T) {
	for _, tc := range []struct {
		name string
		cs   []commit
		want []Row
	}{
		{"线性：一条泳道全直", []commit{c("C", "B"), c("B", "A"), c("A")},
			[]Row{{0, []Edge{{0, 0}}}, {0, []Edge{{0, 0}}}, {0, nil}}},
		// M 合并 F2；F1、B 都从 A 分出来：F1 那行弯回主线
		{"分支后合并", []commit{c("M", "B", "F2"), c("F2", "F1"), c("B", "A"), c("F1", "A"), c("A")},
			[]Row{
				{0, []Edge{{0, 0}, {0, 1}}},
				{1, []Edge{{0, 0}, {1, 1}}},
				{0, []Edge{{0, 0}, {1, 1}}},
				{1, []Edge{{0, 0}, {1, 0}}},
				{0, nil},
			}},
		{"三个父的 octopus", []commit{c("O", "P1", "P2", "P3"), c("P1"), c("P2"), c("P3")},
			[]Row{
				{0, []Edge{{0, 0}, {0, 1}, {0, 2}}},
				{0, []Edge{{1, 1}, {2, 2}}},
				{1, []Edge{{2, 2}}},
				{2, nil},
			}},
		{"五个父的 octopus", []commit{c("O", "P1", "P2", "P3", "P4", "P5"), c("P1"), c("P2"), c("P3"), c("P4"), c("P5")},
			[]Row{
				{0, []Edge{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}}},
				{0, []Edge{{1, 1}, {2, 2}, {3, 3}, {4, 4}}},
				{1, []Edge{{2, 2}, {3, 3}, {4, 4}}},
				{2, []Edge{{3, 3}, {4, 4}}},
				{3, []Edge{{4, 4}}},
				{4, nil},
			}},
		// A1 是根，结束后泳道 0 空出来；C1 是另一个顶，用回泳道 0（中间的空位复用），B 一直在泳道 1
		{"两个独立的根，空出的泳道复用", []commit{c("A2", "A1"), c("B2", "B1"), c("A1"), c("C1"), c("B1")},
			[]Row{
				{0, []Edge{{0, 0}}},
				{1, []Edge{{0, 0}, {1, 1}}},
				{0, []Edge{{1, 1}}},
				{0, []Edge{{1, 1}}},
				{1, nil},
			}},
		{"父提交不出现：线一直到底", []commit{c("A", "Z"), c("B")},
			[]Row{{0, []Edge{{0, 0}}}, {1, []Edge{{0, 0}}}}},
		// C1 先占了泳道 1 等 P；Y 的父也是 P，但主线不往右跳，接着在泳道 0 等；
		// 到 P 那一行发现两条都在等：上一行（Y）的 {1,1} 改成 {1,0}
		{"多个子提交汇入同一个父", []commit{c("X", "Y"), c("C1", "P"), c("Y", "P"), c("P")},
			[]Row{
				{0, []Edge{{0, 0}}},
				{1, []Edge{{0, 0}, {1, 1}}},
				{0, []Edge{{0, 0}, {1, 0}}},
				{0, nil},
			}},
		// 左边已经有泳道等父提交：这一行就弯过去，空出自己这条
		{"子提交在右边，马上弯回左边", []commit{c("A", "P"), c("B", "P"), c("P")},
			[]Row{{0, []Edge{{0, 0}}}, {1, []Edge{{0, 0}, {1, 0}}}, {0, nil}}},
		// 第一个父弯回左边、空出的节点泳道接着给合并进来的父用
		{"合并提交的第一个父在左边", []commit{c("A", "P"), c("M", "P", "Q"), c("P"), c("Q")},
			[]Row{
				{0, []Edge{{0, 0}}},
				{1, []Edge{{0, 0}, {1, 0}, {1, 1}}},
				{0, []Edge{{1, 1}}},
				{1, nil},
			}},
		{"同一个父写了两遍", []commit{c("M", "A", "A"), c("A")},
			[]Row{{0, []Edge{{0, 0}}}, {0, nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(tc.cs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("\n得到 %v\n想要 %v", got, tc.want)
			}
			connected(t, tc.cs, got)
		})
	}
}

func TestWidth(t *testing.T) {
	for _, tc := range []struct {
		row  Row
		want int
	}{
		{Row{0, nil}, 1},
		{Row{2, []Edge{{0, 0}}}, 3},
		{Row{0, []Edge{{0, 0}, {0, 4}}}, 5},
	} {
		if got := tc.row.Width(); got != tc.want {
			t.Errorf("%v 的宽是 %d，想要 %d", tc.row, got, tc.want)
		}
	}
}

// randomDAG 生成 n 个按显示顺序排好的提交（父提交都在子提交后面）：大多一个父，有合并、octopus、根，
// 还有几个父提交不在这 n 个里。
func randomDAG(rng *rand.Rand, n int) []commit {
	cs := make([]commit, n)
	for i := range cs {
		cs[i].hash = fmt.Sprintf("c%03d", i)
	}
	for i := range cs {
		var ps []string
		k := 1
		switch x := rng.Intn(100); {
		case x < 4:
			k = 0
		case x < 20:
			k = 2
		case x < 23:
			k = 3 + rng.Intn(3)
		}
		for range k {
			j := i + 1 + rng.Intn(12)
			if rng.Intn(10) == 0 {
				j = i + 1 + rng.Intn(n) // 远一点的父，可能超出 n（没读到的）
			}
			p := fmt.Sprintf("c%03d", j)
			if !slices.Contains(ps, p) {
				ps = append(ps, p)
			}
		}
		cs[i].parents = ps
	}
	return cs
}

// 跨页：先喂 200 个、再拿同一个 Lanes 喂 100 个，存成两段（像 History 分页那样 append），和一次喂完全等。
func TestPaging(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		cs := randomDAG(rand.New(rand.NewSource(seed)), 300)
		once := run(cs)
		connected(t, cs, once)

		var l Lanes
		page1 := feed(&l, cs[:200])
		page2 := feed(&l, cs[200:])
		paged := append(slices.Clip(page1), page2...)
		if !reflect.DeepEqual(paged, once) {
			for k := range once {
				if !reflect.DeepEqual(paged[k], once[k]) {
					t.Fatalf("seed %d 第 %d 行：分页 %v，一次算 %v", seed, k, paged[k], once[k])
				}
			}
		}
	}
}

// 主线（每个提交的第一个父）一直在泳道 0：别的分支汇入、合并进来都不让它挪。
func TestMainlineStraight(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cs := randomDAG(rng, 300)
		// 把 c000 → 第一个父 → … 这条链标出来
		main := map[string]bool{}
		for h := "c000"; ; {
			main[h] = true
			i := slices.IndexFunc(cs, func(cm commit) bool { return cm.hash == h })
			if i < 0 || len(cs[i].parents) == 0 {
				break
			}
			h = cs[i].parents[0]
		}
		for k, row := range run(cs) {
			if main[cs[k].hash] && row.Lane != 0 {
				t.Fatalf("seed %d：主线上的 %s 在泳道 %d", seed, cs[k].hash, row.Lane)
			}
		}
	}
}

// Reserve：开着"所有分支"时 HEAD 固定在泳道 0，它上面别的分支更新的提交在泳道 1 起，泳道 0 上没有线穿过去。
func TestReserve(t *testing.T) {
	reserved := func(head string, cs []commit) []Row {
		var l Lanes
		l.Reserve(head)
		return feed(&l, cs)
	}
	for _, tc := range []struct {
		name string
		head string
		cs   []commit
		want []Row
	}{
		// F 分支比 HEAD（H）新：没留泳道时 F 在 0、H 在 1
		{"别的分支更新", "H", []commit{c("F2", "F1"), c("F1", "A"), c("H", "A"), c("A")},
			[]Row{{1, []Edge{{1, 1}}}, {1, []Edge{{1, 1}}}, {0, []Edge{{0, 0}, {1, 0}}}, {0, nil}}},
		// X 是 HEAD 的子提交（别的分支接着 HEAD 往下做）：在自己的泳道上等，到 H 上面一行再弯进泳道 0
		{"HEAD 的子提交更新", "H", []commit{c("X", "H"), c("H", "A"), c("A")},
			[]Row{{1, []Edge{{1, 0}}}, {0, []Edge{{0, 0}}}, {0, nil}}},
		// 合并提交 M 的第二个父是 HEAD：开一条泳道（2）连过去，到 H 那一行汇入——M 就在 H 上面，连 M 的那条线直接弯进泳道 0
		{"合并进来的是 HEAD", "H", []commit{c("M", "B", "H"), c("H", "A"), c("B", "A"), c("A")},
			[]Row{{1, []Edge{{1, 1}, {1, 0}}}, {0, []Edge{{0, 0}, {1, 1}}}, {1, []Edge{{0, 0}, {1, 0}}}, {0, nil}}},
		{"HEAD 就是第一个", "C", []commit{c("C", "B"), c("B", "A"), c("A")},
			[]Row{{0, []Edge{{0, 0}}}, {0, []Edge{{0, 0}}}, {0, nil}}},
	} {
		got := reserved(tc.head, tc.cs)
		connected(t, tc.cs, got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s：%v，想要 %v", tc.name, got, tc.want)
		}
	}

	// 随机的图：HEAD 那一行在泳道 0，上面的行泳道 0 上没有节点、没有穿过去的线（只有紧挨着 HEAD 的上一行弯进来），
	// HEAD 的第一父链一直在泳道 0；分页（包括正好在 HEAD 那一行分开）和一次喂完一样
	for seed := int64(1); seed <= 20; seed++ {
		cs := randomDAG(rand.New(rand.NewSource(seed)), 300)
		hi := 40 + int(seed)*3
		head := cs[hi].hash
		once := reserved(head, cs)
		connected(t, cs, once)
		chain := map[string]bool{}
		for h := head; ; {
			chain[h] = true
			i := slices.IndexFunc(cs, func(cm commit) bool { return cm.hash == h })
			if i < 0 || len(cs[i].parents) == 0 {
				break
			}
			h = cs[i].parents[0]
		}
		for k, row := range once {
			switch {
			case k < hi:
				if row.Lane == 0 {
					t.Fatalf("seed %d：HEAD 上面的第 %d 行节点在泳道 0", seed, k)
				}
				for _, e := range row.Edges {
					if e.From == 0 || e.To == 0 && k != hi-1 {
						t.Fatalf("seed %d：HEAD 上面的第 %d 行有泳道 0 的线 %v", seed, k, e)
					}
				}
			case chain[cs[k].hash] && row.Lane != 0:
				t.Fatalf("seed %d：HEAD 的第一父链上的 %s 在泳道 %d", seed, cs[k].hash, row.Lane)
			}
		}
		for _, cut := range []int{hi, hi + 1, 200} {
			var l Lanes
			l.Reserve(head)
			page1 := feed(&l, cs[:cut])
			paged := append(slices.Clip(page1), feed(&l, cs[cut:])...)
			if !reflect.DeepEqual(paged, once) {
				t.Fatalf("seed %d：在第 %d 行分页和一次喂完不一样", seed, cut)
			}
		}

		// 留的提交一直没来：泳道 0 一直空着，别的和没留时一样、整体往右挪一条
		plain := run(cs)
		for k, row := range reserved("不在里面", cs) {
			want := Row{Lane: plain[k].Lane + 1}
			for _, e := range plain[k].Edges {
				want.Edges = append(want.Edges, Edge{e.From + 1, e.To + 1})
			}
			if !reflect.DeepEqual(row, want) {
				t.Fatalf("seed %d 第 %d 行：%v，想要 %v（没留时 %v）", seed, k, row, want, plain[k])
			}
		}
	}

	// 空的哈希、喂到一半再留都不算
	var l Lanes
	l.Reserve("")
	if got := feed(&l, []commit{c("B", "A")}); got[0].Lane != 0 {
		t.Errorf("Reserve(\"\") 之后第一个提交在泳道 %d", got[0].Lane)
	}
	l.Reserve("Q")
	if l.reserved != "" {
		t.Errorf("喂到一半 Reserve 还是留了泳道 0（%q）", l.reserved)
	}
}
