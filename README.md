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
| G03 TradeService | 完成 | 创建/接受/结算/取消/对账，可从 settling/refunding 续跑 |
| G04 失败注入 | 完成 | 创建失败、接受冲突、部分结算、过期对账 |
| H02 玩家接入 | 完成 | 原生 TCP + protobuf v3；dev token；gateway 唯一入口 |
| TCP 玩家压测 | 1h soak 进行中 | 短时 p99 受启动 actor spawn 影响，待 1h 结果 |
| 全链路 protobuf | 完成 | gateway→world Command/Response typed，无 data JSON |
| 玩家推送/续传 | 完成 | Hello/Subscribe/Event；version-gated resume |
| 独立 gateway | 完成 | cmd/gateway 只做 TCP 接入，world 无玩家入口 |
| 自定义 TCP link | 完成 | 帧协议/多路复用/超时；gateway→world |
| TCP 连接池 | 完成 | -tcp-pool 默认 4，按请求轮询 |
| 交易走 TCP | 完成 | 双方各自玩家连接；1k 30s p99 1.17ms |
| 动态 TCP 池 | 完成 | SetSize + /admin/pool；soak 中 4→8 |
| 池按 playerID 哈希 | 完成 | 同一玩家固定槽位，减少跨连接乱序 |
| 1h 动态池 soak | 完成 | 719,001 请求、0 错误、p50 111µs、p99 1.22ms |
| Mongo 真实集成 | 完成 | 本机 MongoDB 7.0.14；交易重启后仍为 settled |
| Mongo 索引/TTL | 完成 | state_expireAt + 	tl_expireDate（30 天留存） |
| 副本集事务 | 完成 | 单节点 s0；WithTransaction 双写通过 |
| 对账 worker | 完成 | 每 30s Reconcile，推进 settling/过期交易 |
| 战斗 submit-settle | 完成 | 确定性胜负、attle_get；内存存储 |
| 赛季重置 | 完成 | 懒重置 + 24h sweep，escrow 未清则跳过 |
| 3-shard Mongo | 完成 | mongos + 3 shard；	rades 按 _id 哈希；chunks 覆盖 3 shard |
| 战斗持久化 | 完成 | Mongo attles 集合；重启后可查 |
| 对账指标 | 完成 | econcile_runs/touched/errors + /metrics |
| 战斗历史 | 完成 | attle_history 按玩家/赛季返回，最新优先 |
| 对账错误明细 | 完成 | /api/reconcile/errors，保留最近 20 条 |
| 赛季奖励 | 完成 | 重置前按排名发金，返回 ewarded |
| 战斗历史分页 | 完成 | (createdAt,_id) 复合游标 |
| 赛季奖励配置 | 完成 | season.rewards 模板，支持自定义区间 |
| 对账告警 | 完成 | econcile_alerts_total + 日志 hook |
| RF=3 快照 | 完成 | 3 副本 + quorum 2；本地目录模拟 |
| WAL 截断 | 完成 | 快照后按 offset 截断，Memory/JetStream 保序 |
| 快照原子 offset | 完成 | mutation barrier + SnapshotWithOffset |
| 跨进程 RF=3 | 完成 | peer 复制 + token；owner 本地丢失可从 peer 恢复 |
| 快照最高 offset | 完成 | 多写者下按 WAL offset 选最新，不按文件时间 |
| 玩家请求协议 | 完成 | protobuf v3 proto/player/v1；响应 data 为 JSON 字节 |
| WAL R3 | 完成 | 3 节点 JetStream，walReplicas=3；单节点故障可写/可恢复 |
| Prometheus 导出 | 完成 | /metrics/prometheus 文本格式 |
| NATS R3 模板 | 完成 | deploy/nats/docker-compose.yml + CI wal-r3 job |
| H03/H04 bot | 初版完成 | 100 bot 12s：2380 请求、0 错误、p99 30ms |
| C05 WAL 接线 | 完成 | JetStream WAL + 快照恢复；进程级 kill -9 验证通过 |
| Mongo 交易存储 | 完成 | storage.mongoUrl 非空时用 Mongo Repo，否则内存 |
| H05 1k bot | 1h soak 完成 | 719,001 请求、0 错误、p50 32µs、p99 1.5ms |
| H05 1h TCP soak | 待补 | 历史 WS 1h 通过；原生 TCP 版待重跑 |
| C06 崩溃注入 | 完成 | 截断快照回退、tmp 忽略、WAL 边界/坏记录；进程级快照损坏回退 |

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

# 4. 跑单机 world（HTTP 只保留内部指标/健康检查）
make run-world

# 5. 跑 gateway（玩家原生 TCP :9001）
make run-gateway

# 6. 压测（1k bot 走 gateway TCP）
go run ./cmd/bot -addr 127.0.0.1:9001 -n 1000 -rate 0.2
```

Windows 无 `make` 时直接执行 `go build ./...`、`go test ./...`。

## P0 范围

包含：运行时、玩家/区域、WAL R3、RF=3 快照、排行榜、定向交易、战斗、赛季结算、原生 TCP 网关、机器人、指标。

不包含：多 world/跨服、账号体系、K8s、深度战斗、拍卖行。

## 约定

- 所有跨 actor 消息使用 `actor.Envelope`，必须带 `MsgID` 与 `OpID`。
- 业务 actor 只依赖 `internal/actor`，不直接 import goakt。
- 交易/快照/时间轮改动必须补崩溃注入测试。
