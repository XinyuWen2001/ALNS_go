# Go + ALNS 实战版（基于你的数据格式）

这版已经接入更强的 fleet repair 规则（不是简单贪心一把梭）：
- 读取 `instance.json` 与 `ft_1.csv ... ft_k.csv`
- ALNS 破坏/修复后执行 `RebuildFleetWithOptions`
- 支持**线路车辆池（line pools）**、**按SOC优先选车**、**充电回溯搜索窗口**
- 候选解若违反电量/充电桩/占用约束会被丢弃

## 运行方式

单次：
```bash
go run ./cmd/alns -instance data/instance.json -ft_dir data_cd -seed 42 -iters 50000
```

多种子：
```bash
go run ./cmd/alns -instance data/instance.json -ft_dir data_cd -seeds 1,2,3,4,5 -iters 50000
```

输出：
- `logs/alns_时间戳/single_run_summary.csv`
- `logs/alns_时间戳/multi_seed_summary.csv`

## 新版 fleet repair 机制

1. **事件全局时序分配**：按全部线路发车时刻排序分配，避免单线局部最优。
2. **线路池约束**：先按线路行程时长和 headway 估算目标车辆数，再按 `Emax` 分配池，缓解“强电池全被某条线抢走”。
3. **候选排序**：同线已分配车辆优先，其次未分配车辆；每类内部按当前 `SOC` 降序。
4. **充电回溯**：若发车时 SOC 不足，则在给定回溯窗口内寻找最近可行充电起点。

## 下一步强化建议
1. 将运营成本 `c_l^t` 从数据侧真实加载（当前默认 0）。
2. 增加失败诊断统计（无候选/占用冲突/SOC不足/桩容量不足）。
3. 在 fleet repair 中加入“强弱两阶段修复”（先启用线路池，失败后退化为全局池）。
