# test

- `bot/`：1k bot 场景与压测报告（H03/H04/H05）
- `sim/`：虚拟时钟确定性回放（J01）
- `chaos/`：kill -9、快照损坏、WAL 半条记录（J02）

P0 阶段先由 `cmd/bot` 承担最小压测入口。
