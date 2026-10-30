# HapeServer

重度 SLG 服务端的 Go 实现仓库。当前处于 **P0：1k bot 单机闭环** 阶段。

设计文档：

- [架构设计 v1.2](docs/slg-server-architecture.md)
- [P0 任务清单与验收用例](docs/p0-task-plan.md)

## 工具链

| 项 | 版本 |
|---|---|
| Go | 1.27.3（冒烟验证通过；goakt v4.6.4 要求 ≥1.26） |
| goakt | v4.6.4 |
| NATS | 2.15.x + JetStream |
| MongoDB | 7.x（P0 单节点） |

## 临时验证（冒烟）

2026-10-30 已用本机 Go 1.27.3 做临时冒烟验证（Windows/amd64）：

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test ./...` | 9 个包全部通过 |

详细记录见 [docs/verification.md](docs/verification.md)。该结果只说明骨架可编译、单测可跑，**不代表 P0 验收**；正式验收标准见 [P0 任务清单](docs/p0-task-plan.md) §7。

`go test -race` 与 `docker compose` 尚未在本机执行。

## P0 进度

| 任务 | 状态 | 证据 |
|---|---|---|
| F02 跳表 | 完成 | PT-01：2 万次随机操作对比排序数组 0 差异；RankOf ~0.9µs |
| C02 JetStream WAL | 完成 | 本地 NATS 2.15.2 集成通过（Append / Replay / Truncate） |
| F03 WorldRankActor | 完成 | 版本去重、重建、TopK/RankOf 单测通过 |
| C04 快照存储 | 完成 | 原子写、保留 3 代、损坏自动回退 |
| G01 Mongo 交易仓储 | 完成 | CAS 状态迁移 + 待处理查询；集成测试需 MONGO_URL |
| F04 排行重建接线 | 完成 | player_power 投影 + RebuildFrom 单测 |
| C05 恢复编排 | 完成 | 快照 + WAL 尾部回放单测 |
| G02 escrow | 完成 | 锁定/退款/结算幂等单测 |

## 目录

```
cmd/
  gateway/        WS 接入、dev token
  world/          单机分片节点
  bot/            压测机器人
internal/
  actor/          ActorSystem 抽象、Envelope、Ref
  runtime/        goakt 适配 + inmem 测试实现
  config/         配置加载与默认值
  shard/          分片键
  timer/          时间轮
  wal/            预写日志
  snapshot/       快照格式
  world/          PlayerActor / RegionActor
  rank/           RankKey / RankOrder / 榜单
  trade/          交易状态机
  protocol/       协议消息
  gateway/        会话
  observability/  指标
docs/             架构与 P0 计划
deploy/k8s/       P1 部署清单（占位）
test/             压测与模拟（占位）
```

## 快速开始

```bash
# 1. 安装依赖
go mod tidy

# 2. 起本地依赖（NATS / Mongo）
docker compose up -d

# 3. 构建与测试
make build
make test

# 4. 跑单机 world
make run-world

# 5. 压测（1k bot）
go run ./cmd/bot -n 1000 -rate 0.2
```

Windows 无 `make` 时直接执行 `go build ./...`、`go test ./...`。

## P0 范围

包含：运行时、玩家/区域、时间轮、WAL、本地快照、排行榜、定向交易、网关/机器人、指标。

不包含：集群/分片迁移、RF=3 复制、K8s、战斗、赛季结算、跨服组、拍卖行。

## 约定

- 所有跨 actor 消息使用 `actor.Envelope`，必须带 `MsgID` 与 `OpID`。
- 业务 actor 只依赖 `internal/actor`，不直接 import goakt。
- 交易/快照/时间轮改动必须补崩溃注入测试。
