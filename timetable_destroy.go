package operators

import (
	"math/rand"
	"sort"

	"alns_go/internal/instance"
	"alns_go/internal/solution"
)

// DepMove is a small record for destroy/repair diagnostics.
type DepMove struct {
	L int
	T int
}

// -----------------------------
// Operator 0: Line Interval Removal
// -----------------------------

type DestroyLineIntervalOptions struct {
	MinLen int
	MaxLen int
}

// Destroy_LineIntervalRemoval removes a contiguous time chunk [t, t+len) from a random line.
func Destroy_LineIntervalRemoval(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, opt DestroyLineIntervalOptions) ([]DepMove, error) {
	L := len(inst.Lines)
	if L == 0 {
		return nil, nil
	}

	// Pick a line
	l := rng.Intn(L)
	dep := sol.Dep[l]
	if len(dep) == 0 {
		return nil, nil
	}

	T := inst.T
	if opt.MinLen <= 0 {
		opt.MinLen = 10
	}
	if opt.MaxLen <= opt.MinLen {
		opt.MaxLen = opt.MinLen + 30
	}

	// Pick length
	length := opt.MinLen + rng.Intn(opt.MaxLen-opt.MinLen+1)
	// Pick start time
	start := rng.Intn(T)
	end := start + length

	newDep, removedTs := removeDepInWindow(dep, start, end)
	sol.Dep[l] = newDep

	// Record moves
	moves := make([]DepMove, len(removedTs))
	for i, t := range removedTs {
		moves[i] = DepMove{L: l, T: t}
	}
	return moves, nil
}

// -----------------------------
// [升级版] Operator 1: Worst FT Block Removal
// -----------------------------

type DestroyWorstFTBlockOptions struct {
	Q             int
	BlockSize     int // 每次移除的连续班次数量
	WithinOneLine bool
}

// Destroy_WorstFT_Block 找到 FT 最差的班次，并将它及其相邻的班次作为一个“区块”一并移除
func Destroy_WorstFT_Block(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, opt DestroyWorstFTBlockOptions) ([]DepMove, error) {
	if opt.Q <= 0 { opt.Q = 5 }
	if opt.BlockSize <= 0 { opt.BlockSize = 3 }

	type candidate struct {
		l, idx int
		ft     float64
	}
	cands := make([]candidate, 0, 1024)

	L := len(inst.Lines)
	targetL := -1
	if opt.WithinOneLine {
		targetL = rng.Intn(L)
	}

	for l := 0; l < L; l++ {
		if targetL != -1 && l != targetL { continue }
		for i, t := range sol.Dep[l] {
			if t >= 0 && t < inst.T {
				cands = append(cands, candidate{l: l, idx: i, ft: inst.FT[l][t]})
			}
		}
	}

	// 按 FT 升序排序（最差的在前面）
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].ft < cands[j].ft
	})

	if len(cands) == 0 { return nil, nil }

	count := opt.Q
	if count > len(cands) { count = len(cands) }
	
	toRemove := make(map[int]map[int]bool)
	removedMoves := make([]DepMove, 0, count*opt.BlockSize)

	// 选取前 Q 个最差的班次
	for i := 0; i < count; i++ {
		c := cands[i]
		l := c.l
		idx := c.idx

		if toRemove[l] == nil {
			toRemove[l] = make(map[int]bool)
		}

		// 确定区块边界：以目标 idx 为中心，前后辐射
		half := opt.BlockSize / 2
		startIdx := idx - half
		if startIdx < 0 { startIdx = 0 }
		endIdx := startIdx + opt.BlockSize - 1
		if endIdx >= len(sol.Dep[l]) { endIdx = len(sol.Dep[l]) - 1 }

		// 将整个区块内的班次标记为移除
		for j := startIdx; j <= endIdx; j++ {
			t := sol.Dep[l][j]
			if !toRemove[l][t] {
				toRemove[l][t] = true
				removedMoves = append(removedMoves, DepMove{L: l, T: t})
			}
		}
	}

	// 执行移除操作
	for l := 0; l < L; l++ {
		if toRemove[l] == nil { continue }
		newDep := make([]int, 0, len(sol.Dep[l]))
		for _, t := range sol.Dep[l] {
			if !toRemove[l][t] {
				newDep = append(newDep, t)
			}
		}
		sol.Dep[l] = newDep
	}

	return removedMoves, nil
}

// -----------------------------
// [升级版] Operator 2: Block Random Removal
// -----------------------------

type DestroyBlockRandomOptions struct {
	Q             int
	BlockSize     int // 每次移除的连续班次数量
	WithinOneLine bool
}

// Destroy_Block_Random 随机选择一个起始班次，连续移除它后面的 BlockSize 个班次
func Destroy_Block_Random(inst *instance.Instance, sol *solution.Solution, rng *rand.Rand, opt DestroyBlockRandomOptions) ([]DepMove, error) {
	if opt.Q <= 0 { opt.Q = 3 }
	if opt.BlockSize <= 0 { opt.BlockSize = 3 }

	type depRef struct {
		l   int
		idx int
	}
	pool := make([]depRef, 0, 512)

	L := len(inst.Lines)
	targetL := -1
	if opt.WithinOneLine {
		targetL = rng.Intn(L)
	}

	for l := 0; l < L; l++ {
		if targetL != -1 && l != targetL { continue }
		for idx := range sol.Dep[l] {
			pool = append(pool, depRef{l: l, idx: idx})
		}
	}

	if len(pool) == 0 { return nil, nil }

	// 打乱发车池
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	
	count := opt.Q
	if count > len(pool) { count = len(pool) }
	picked := pool[:count]

	toRemove := make(map[int]map[int]bool)
	var moves []DepMove

	for _, p := range picked {
		if toRemove[p.l] == nil {
			toRemove[p.l] = make(map[int]bool)
		}

		// 从随机选中的班次开始，往后连续移除 BlockSize 个
		startIdx := p.idx
		endIdx := p.idx + opt.BlockSize - 1
		if endIdx >= len(sol.Dep[p.l]) { endIdx = len(sol.Dep[p.l]) - 1 }

		for j := startIdx; j <= endIdx; j++ {
			t := sol.Dep[p.l][j]
			if !toRemove[p.l][t] {
				toRemove[p.l][t] = true
				moves = append(moves, DepMove{L: p.l, T: t})
			}
		}
	}

	// 执行移除操作
	for l := 0; l < L; l++ {
		if toRemove[l] == nil { continue }
		newDep := make([]int, 0, len(sol.Dep[l]))
		for _, t := range sol.Dep[l] {
			if !toRemove[l][t] {
				newDep = append(newDep, t)
			}
		}
		sol.Dep[l] = newDep
	}

	return moves, nil
}

// -----------------------------
// Helpers
// -----------------------------

func normalizeDep(dep []int, T int) []int {
	if len(dep) == 0 {
		return dep
	}
	tmp := make([]int, 0, len(dep))
	for _, t := range dep {
		if t >= 0 && t < T {
			tmp = append(tmp, t)
		}
	}
	sort.Ints(tmp)
	out := tmp[:0]
	last := -1
	for _, t := range tmp {
		if t != last {
			out = append(out, t)
			last = t
		}
	}
	return out
}

func removeDepInWindow(dep []int, t1, t2 int) (newDep []int, removed []int) {
	newDep = make([]int, 0, len(dep))
	removed = make([]int, 0, 8)
	for _, t := range dep {
		if t >= t1 && t <= t2 {
			removed = append(removed, t)
		} else {
			newDep = append(newDep, t)
		}
	}
	return newDep, removed
}