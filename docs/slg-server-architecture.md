# 重度 SLG 分布式服务器架构设计（Go + Actor）

| 项 | 内容 |
|---|---|
| 状态 | Draft v1.2 — 默认 world 数 16/区域、默认阈值、goakt v4.6.4 |
| 日期 | 2026-10-30 |
| 范围 | 单 world 20 万峰值在线起步；全球按区域多 world（默认 16/区域，可配） |
| 读者 | 服务端团队（5–20 人）、技术负责人、运维 |
| 关联决策 | 见 §15 ADR、§18 待定事项、附录 D 默认配置 |

---

## 0. TL;DR — 已定决策

1. **逻辑单世界，物理分片**：分片键与地图坐标系解耦，玩家实体与地图区域分属两个分片空间。
2. **actor 边界 = 一致性边界 = 分片边界**：一个玩家聚合（城市 + 部队 + 队列）是一个 actor，串行处理，天然单写者。
3. **强一致只保实体级**：跨玩家操作用「预留 → 结算 → 应用」的 saga，不做全局 2PC，不追求全局可串行化。
4. **不要给每个格子建 actor**：`RegionActor` 持有 4096 格的状态数组与占用索引。
5. **战斗实例化**：王城/关卡/联盟集结进入 `BattleActor` 做确定性模拟，地图侧只写回「谁占着」。
6. **单写者 + 租约 + fencing epoch**：分片迁移时先冻结邮箱，旧 epoch 的写入一律拒绝。
7. **至少一次投递 + OpID 幂等表**：所有跨片命令携带 `OpID`，接收方保留最近 N 条去重记录。
8. **百万级定时器用分层时间轮**统一调度，到期事件路由回属主 actor；禁止每 actor 一个 `time.After`。
9. **不设 Redis**：内存（权威）+ 本地 NVMe 快照（RF=3）+ WAL（JetStream）+ MongoDB（冷归档）。
10. **排行榜三层**：`WorldRankActor`（本世界全量）+ 分层合并索引（16 world/组，跨服唯一名次）+ 结算归档（赛季历史）。
11. **唯一名次 + 可插拔排序**：默认战力降序、同分按 pid 升序；全序由 `RankOrder` 接口的 24 字节 key 定义，首版只实现默认规则。
12. **跨服规模**：单跨服组最多 512 个 world；**默认 16 world/区域（1 个合并组）**，可配置。
13. **交易首版**：只做玩家间定向交易（A 对 B），escrow + Mongo CAS 状态机；拍卖行不做。
14. **战斗提交即结算**：到达瞬间一次性确定性模拟，战斗中不可操作；实时指挥留到 v2。
15. **赛季重置**：只重置赛季进度（战力/城建/科技/部队/资源/领地），英雄与付费资产保留；时机为惰性 + 24h 扫在线。
16. **Runtime = goakt v4.6.4**（官方最新稳定版，2026-10-28 发布）；升级由开发者按需决定，升级前跑回归。
17. **存储**：快照 RF=3（owner + 2 对等节点）；MongoDB 每区域 3 分片 × 3 副本共库，分片键 `{worldID, playerID: hashed}`。
18. **全球发行**：按区域多 world（**不是**单世界跨区同服）；每区域独立一套 Gateway/Shard/etcd/NATS/Mongo。
19. **部署**：K8s 多区域集群 + GeoDNS/Anycast 接入；全局控制面只做路由、配置、观测，不做跨洲共识。
20. **区域故障**：v1 不做世界自动接管，只做入口切流与人工恢复。
21. **默认值可配**：world 数、扩容阈值、交易超时、快照间隔等全部走配置，默认值见附录 D。

---

## 1. 目标与非目标

### 1.1 目标（可验收）

| 编号 | 目标 | 验收标准 |
|---|---|---|
| G1 | 单 world 20 万峰值同时在线 | 压测 20 万 bot 稳定 2 小时，错误率 < 0.1% |
| G2 | 玩家可感知的强一致 | 资源/兵力/所有权无负数、无双花、无重复结算；对账差异为 0 |
| G3 | 指令延迟 | 网关到应答 p99 < 200 ms（不含客户端网络） |
| G4 | 大规模战斗 | 200v200 集结战结算 p99 < 1 s，地图主循环不受影响 |
| G5 | 故障恢复 | 单节点宕机，RTO < 2 min，RPO ≤ 1 s（关键操作 RPO = 0） |
| G6 | 水平扩展 | 节点数从 8 扩到 16，容量近线性，迁移期间玩家无感 |
| G7 | 可维护性 | 5–20 人团队可按模块分工，单模块可独立压测与回放 |
| G8 | 三层排行榜 | 本世界 / 跨服 / 赛季历史均可查；跨服名次唯一且绝对精确 |
| G9 | 跨服规模 | 单跨服组支持 512 个 world（32 个合并组，区域内） |
| G10 | 定向交易 | 不出现钱货两空/重复入账；崩溃后可对账恢复；单笔 p99 < 100 ms |
| G11 | 全球发行 | 按区域独立部署；新增区域不改现有区域代码；区域故障不扩散 |

### 1.2 非目标

- **不做单世界跨区同服**（全球同服定义为「全球发行 + 按区域多 world」；单世界模式延后）。
- 不做逐帧同步或实时动作战斗；战斗为「提交即结算」。
- 不自研通用游戏引擎、RPC 框架、消息中间件、Raft。
- 首版不做拍卖行、跨服国战、跨世界匹配（预留 worldID 与接口）。
- **首版不做区域故障自动接管**（已确认接受；入口切流 + 人工恢复）。

---

## 2. 约束与假设

| 类别 | 假设 |
|---|---|
| 规模 | 单 world 20 万 CCU 起步；全球总量按区域/世界线性扩展 |
| 区域 | 首发区域待定（候选：CN、SEA、EU、NA）；玩家归属一个 home region |
| 世界 | 每区域默认 16 个 world（可配）；跨服组在区域内成组（20–512 world） |
| 排序规则 | 默认战力降序、同分按 `pid` 升序；`RankOrder` 接口预留，首版仅默认实现 |
| 合并组 | 固定每 16 个 world 一个合并组；默认 16 world → 1 组；上限 512 → 32 组 |
| 交易 | 首版仅定向交易；默认 10 分钟超时；单笔最多 5 个物品堆叠 |
| 重置 | 仅赛季进度；混合时机（惰性 + 24h 扫在线） |
| Runtime | goakt v4.6.4（锁定），分片/租约/迁移/时间轮自研 |
| 地图 | 2048×2048 格，单 world；区域 64×64 格（4096 格/区域） |
| 部署 | 每区域一个 K8s 集群（16C/32G + 1 TB NVMe 节点） |
| 存储 | 本地 NVMe 快照 RF=3、JetStream WAL、MongoDB 每区域 3 分片共库；无 Redis |
| 客户端 | 长连接 WebSocket，请求-应答 + 服务端推送；GeoDNS 接入最近区域 |

### 2.1 术语

| 术语 | 含义 |
|---|---|
| 区域（Region） | 独立部署单元（如 CN、EU），含完整 Gateway/Shard/etcd/NATS/Mongo |
| 聚合（Aggregate） | 一致性单元，如「一个玩家的城市 + 其全部部队与队列」 |
| 分片（Shard） | 放置与迁移的最小单位，对应一个或多个聚合 |
| 地图区域（MapRegion） | 64×64 格区块，对应一个 `RegionActor` |
| 跨服组（CrossGroup） | 同一区域内参与跨服榜的 world 集合 |
| 合并组（MergeGroup） | 每 16 个 world 的聚合单元，对应一个 `GroupRankActor` |
| 名次（Rank） | 唯一名次：`1 + 严格排在前面的人数`（按 `RankKey` 全序） |
| RankKey | 24 字节定长排序键：`[^power][tie][pid]`，大端 |
| Escrow | 交易托管：资产从玩家可用余额移出，暂不属于任何一方 |
| OpID | 业务操作幂等键，同一操作的所有重试共享 |
| Epoch | 分片属主租约的代际号，用于 fencing 旧写入 |
| RF | 快照总副本数；RF=3 = owner + 2 个对等节点 |
| WAL | 预写日志，崩溃回放用 |

---

## 3. 一致性模型

### 3.1 一致性单元

**一个玩家聚合 = 一个 actor = 一个 goroutine = 一个单写者。** 聚合内所有状态变更串行执行，天然线性化；聚合之间只通过消息交互。

```
PlayerActor(playerID)
├── CityState        资源、建筑、科技、城墙、buff
├── Marches[]        在途/驻守/返回的部队
├── Queues[]         建造、研究、训练队列
├── Escrow           交易托管中的资产（tradeID → assets）
├── Inventory        道具、邮件附件（元数据）
└── Timers[]         该玩家的定时器句柄
```

一个聚合 200 KB 量级（含索引），20 万聚合约 40 GB 上限；实际热数据远小于此，冷聚合可换出（见 §9.7）。

### 3.2 一致性域

| 域 | 保证 | 实现手段 |
|---|---|---|
| 单玩家聚合内 | 线性化（强一致） | 单 goroutine 串行 |
| 攻击/掠夺/占领 | 对「攻方部队」和「守方城市」各自强一致 | 预留 → 结算 → 应用 + OpID 幂等（§7.3） |
| 玩家间交易 | 原子（不会钱货两空） | escrow + Mongo CAS 状态机（§7.4） |
| 地图归属（格子/资源点） | 对区域强一致，跨区域最终一致 | RegionActor 单写者 + 预留 TTL |
| 本世界排行 | 秒级最终一致 | WorldRankActor 单写者 + 版本号去重 |
| 跨服排行 | 秒级最终一致；名次唯一且绝对精确 | 分层合并索引，求和精确（§9.8.3） |
| 赛季历史排行 | 结算后不可变 | 结算归档 MongoDB，只读服务 |
| 赛季重置 | 新赛季首次登录时幂等生效 | `SeasonID` 检查 + 24h 后台扫描（§9.8.5） |
| 会话/鉴权 | 无状态令牌，网关本地校验 | JWT + 短期 access token（§10.1） |
| 充值/支付 | 强一致 + 持久化 | 账本服务，同步 WAL + MongoDB 事务 |
| 跨区域 | 不做实时同步 | 区域独立；跨区只做账号路由与聚合观测 |

### 3.3 明确不保证

- 不保证跨玩家操作的**全局可串行化**（结果确定且可重放）。
- 不保证跨分片消息的**恰好一次**；只保证「至少一次 + 幂等」。
- 不保证排行榜与权威状态的实时一致，只保证单调收敛。
- 不保证跨区域玩家的世界互通（全球同服是「按区域多 world」，不是单世界）。

### 3.4 为什么不做全局强一致

若每个跨玩家操作都走一次复制日志提交（Raft/2PC），一次 200v200 集结战需要数百次跨节点提交，按 5–20 ms/次计算，结算延迟直接进入秒级，且热点分片会被串行化拖死。**强一致按实体买，不按世界买**，是本设计的核心取舍。

---

## 4. 总体架构

### 4.1 组件图（单区域）

```
                        ┌──────────────┐   ┌─────────────────────────┐
   Client ──WS──▶       │  Gateway x N │──▶│ RankQuery / TradeService │
                        └──────┬───────┘   │ 无状态：跨服榜 / 交易协调 │
                               │           └────────────┬────────────┘
                               │ 按 shardID 路由          │
                 ┌─────────────▼──────────────────┐      │
                 │           Shard Mesh            │◀─────┘
                 │   Node A ⟷ Node B ⟷ Node C      │  快照 RF=3
                 │   Player / Region / Battle      │
                 │   WorldRank / GroupRank         │
                 └──────┬────────────┬──────────────┘
                        │            │
        ┌───────────────┼────────────┼──────────────────┐
   ┌────▼────┐    ┌─────▼─────┐ ┌────▼──────────┐ ┌─────▼───────┐
   │  etcd   │    │   NATS    │ │ 本地 NVMe      │ │  MongoDB    │
   │ 成员/租约│    │ JetStream │ │ 快照 + 2 副本   │ │ 3 分片共库   │
   └─────────┘    │ WAL/事件   │ └───────────────┘ │ 订单/交易/榜 │
                  └───────────┘                   └─────────────┘
```

**多区域**：每个区域复制一套上图；区域之间不共享 etcd/NATS/Mongo，也不做实时状态同步。全球层只有 GeoDNS/Anycast、账号目录（player → region）、GitOps 配置、监控聚合。

### 4.2 组件职责

| 组件 | 职责 | 状态 |
|---|---|---|
| Gateway | WS 接入、JWT 校验、限流、协议编解码、断线重连、推送 | 完全无状态 |
| RankQuery | 跨服名次求和、Top-K 合并、历史榜查询；本地缓存 | 无状态 |
| TradeService | 交易协调、超时补偿、孤儿 escrow 对账；可多实例 | 无状态 |
| Shard Mesh | 托管 Player/Region/Battle/WorldRank/GroupRank actor | 内存态 + WAL |
| etcd（区域内） | 成员发现、分片表、租约、跨服组与赛季配置 | 强一致元数据 |
| NATS JetStream（区域内） | 跨节点消息、WAL 流、事件总线、踢人广播 | 持久化流 |
| 本地 NVMe | 每 30 s 脏聚合快照；RF=3 复制 | 快照与副本 |
| MongoDB（区域内） | 离线聚合、订单、交易、邮件、回放、赛季榜 | 权威冷数据 |
| 全球控制面 | GeoDNS、账号目录、GitOps、监控聚合 | 无游戏状态 |

### 4.3 关键数据流

| 流程 | 路径 |
|---|---|
| 接入 | GeoDNS/Anycast → 最近区域 Gateway → JWT 校验 → 路由到 home world |
| 登录 | 校验 JWT → PlayerActor 加载本地快照；缺失则拉对端副本 → WAL 回放 |
| 普通指令 | Gateway → PlayerActor → 内存应用 + WAL 追加 → 标记脏 → 应答 |
| 跨玩家 | PlayerActor → 目标 PlayerActor（OpID 幂等）→ 结果回传 |
| 地图操作 | PlayerActor → RegionActor（预留/确认，TTL 自动释放） |
| 定时到期 | 时间轮 → 属主 actor → 状态变更 + WAL |
| 战斗 | 守方 PlayerActor → BattleActor（确定性模拟）→ 结果写回双方 |
| 本世界榜 | PlayerActor → WorldRankActor（战力/次序键变化）→ 本榜查询 |
| 跨服同步 | WorldRankActor 每 100 ms 批量推送 → GroupRankActor |
| 跨服名次 | RankQuery → 各 GroupRankActor `CountAbove` 求和 + 1（缓存 5 s） |
| 交易 | A/B PlayerActor 锁定资产 → `trades` 文档 CAS → TradeService 推进 → 双方入账 |
| 赛季结算 | 协调器 → 冻结榜单 → 归档 → 幂等发奖 → 切换赛季 → 重置 |
| 持久化 | 30 s 脏聚合 → 本地 NVMe → 复制 2 个对等节点；离线归档 MongoDB |

---

## 5. 世界模型与分片

### 5.1 分片空间

| 空间 | 键 | 内容 | 迁移频率 |
|---|---|---|---|
| Player shard | `hash(worldID, playerID)` | 城市、部队、队列、escrow、道具 | 低（节点增减时） |
| Region shard | `hash(worldID, regionX, regionY)` | 4096 格地块、资源点、占用索引、NPC | 低 |
| Battle shard | `hash(battleID)` | 战斗实例 | 一次性 |
| WorldRank shard | `hash(worldID, seasonID)` | 单 world 赛季排行榜跳表 | 赛季切换、故障时 |
| GroupRank shard | `hash(crossGroupID, mergeGroup, seasonID)` | 16 world 合并全序索引 | 赛季切换、故障时 |

前三个空间独立成环：**王城热点不会把玩家状态一起拖垮**。两个 Rank shard 都是单写者。

**world 数**：默认每区域 16 个（1 个合并组），由 `regions.defaultWorlds` 配置；上线后按运营需要增加，代码不假设固定数量。worldID 全局唯一，编码含区域前缀，但玩家数据不跨区域迁移。

### 5.2 实体归属规则

- **城市**：永远属于玩家聚合，不迁移（除非整个 player shard 迁移）。
- **部队**：归属攻方玩家聚合，**终身不迁移所有权**。行军途中由属主单写；到达后由目标 `RegionActor` / 守方 `PlayerActor` 发命令、回结果，属主负责落账。
- **地块/资源点**：归属 `RegionActor`；玩家建造时先向区域「预留」，确认后提交，预留 TTL 到期自动释放。
- **战斗**：`BattleActor` 由守方创建，输入是双方快照 + 随机种子，输出结果与校验哈希。
- **本世界排行榜**：归属该 `(world, season)` 的 `WorldRankActor`，etcd 租约保证唯一属主。
- **跨服索引**：归属该 `(crossGroup, mergeGroup, season)` 的 `GroupRankActor`，etcd 租约保证唯一属主。
- **交易**：权威状态在 MongoDB `trades` 文档（CAS），资产在两个 PlayerActor 的 `Escrow` 中。

### 5.3 分片表与再平衡

- 一致性哈希环 + 每节点 256 个虚拟节点，减少迁移量。
- 分片表（shard → node, epoch）存区域 etcd，watch 推送；网关与节点本地缓存。
- 触发再平衡：节点增减、单节点负载 > 1.3×均值、热点分片标记。
- 迁移协议见 §6.4；一次迁移目标是 ≤ 2 s，期间该分片消息排队不丢弃。

### 5.4 容量估算

世界级容量按**单 world 20 万 CCU** 估算；跨服层按 **512 world × 20 万 = 1.024 亿玩家** 的上限估算，默认 16 world 时对应约 320 万玩家。

| 项 | 公式 | 结果 |
|---|---|---|
| 玩家聚合 | 20 万 × 200 KB | ~40 GB 上限（热数据约 8 GB/世界） |
| 区域状态 | 1024 × (4096 × 24 B + 索引) | ~200 MB/世界 |
| 在途部队 | 60 万 × 1 KB | ~0.6 GB/世界 |
| 定时器 | 约 200 万 × 96 B | ~200 MB/世界 |
| 指令速率 | 20 万 × 0.2 条/s（均值） | 4 万条/s；峰值 3× = 12 万条/s |
| 跨片比例 | 约 20% | 均值 8 k/s，峰值 24 k/s |
| 本地快照 | 1.7 万聚合 × 40 KB | ~680 MB/节点 |
| 快照复制 | 680 MB / 30 s × 2 副本 | 出向 ~46 MB/s/节点（RF=3） |
| WAL | 12 万 × 200 B，保留 10 min | ~24 MB/s，集群 ~14 GB |
| 交易 | 峰值 100 笔/s × 6 次文档写 | ~600 写/s |
| 本世界排行 | 20 万 × 24 B key | ~20 MB/world |
| 跨服层-世界榜 | 默认 16 world × 20 MB | ~320 MB；上限 512 world ≈ 10 GB |
| 跨服层-合并组 | 默认 1 组 × 256 MB | ~256 MB；上限 32 组 ≈ 8 GB |
| 赛季归档 | 20 万 × 120 B | ~24 MB/world/season |
| MongoDB 冷数据 | 20 万 × 40 KB × 3 版本 | ~24 GB/世界 |
| 节点数（单 world） | 12 节点 × 16C/32G/1TB NVMe | 每节点 ~1.7 万聚合、~85 区域 |

**结论**：瓶颈在**跨片消息、热点区域、WAL 刷盘、快照复制带宽、跨服索引构建与 fan-out**；容量规划按这五项做。

---

## 6. Actor 运行时

### 6.1 Runtime：goakt v4.6.4 + 自研分片层

选型结论：**goakt v4.6.4**（官方 2026-10-28 发布的最新稳定版）负责 actor 运行时（生命周期、邮箱、监督、本地调度），**分片表、etcd 租约、fencing、分层时间轮仍自研**。

隔离方式：

```go
type ActorSystem interface {
    Spawn(ctx context.Context, id string, a Actor) (Ref, error)
    Tell(dst Ref, env *Envelope) error
    Ask(ctx context.Context, dst Ref, env *Envelope) (*Envelope, error)
    Stop(ref Ref) error
}
```

- goakt 实现放在 `internal/runtime/goakt`；业务 actor 只依赖 `ActorSystem`/`Actor` 抽象。
- 版本在 `go.mod` 锁定为 `v4.6.4`；是否升级由开发者按需决定，升级前跑迁移/背压/定时器回归。
- v4.6.4 刚发布 2 天，P0 先跑一轮稳定性/内存回归；若发现回归可回退到 v4.6.3。
- v4.6.4 的默认 mailbox 在 actor 收到消息后保留约 3.5 KB；本项目每 world 20 万 actor，预算约 700 MB，节点内存足够，但压测时要观察 RSS。

### 6.2 actor 类型与邮箱

| 类型 | 数量级 | 邮箱策略 |
|---|---|---|
| SessionActor | 20 万（随连接） | 有界 1024，溢出断开慢客户端 |
| PlayerActor | 20 万/世界 | 有界 256 + 系统队列（迁移/停机）优先 |
| RegionActor | 1024/世界 | 有界 4096，满时对玩家返回「繁忙」而非阻塞 |
| BattleActor | 峰值 5 千 | 无界但限速，独立 worker 池 |
| WorldRankActor | 每 world/season 1 个 | 有界 8192，更新消息可合并去重 |
| GroupRankActor | 每 16 world/赛季 1 个 | 有界 16384，批量更新按 100 ms 合并 |

邮箱实现：goakt 的 mailbox 包装为有界队列；**生产者在满时阻塞或快速失败**，绝不静默丢弃业务消息。

### 6.3 调度与并发

- 每节点 `GOMAXPROCS` 个 dispatcher，每个 dispatcher 取 actor 执行一批消息（批上限 64 条或 2 ms），避免单个热 actor 饿死其他 actor。
- actor 内**禁止阻塞调用**：磁盘、网络一律异步回调或由 side-effect 队列处理。
- `RegionActor` 的地块数组不会被并发访问；跨 actor 一律消息，无共享内存、无锁。

### 6.4 单写者、迁移与 fencing

```
Active ──(rebalance)──▶ Draining ──▶ Frozen ──▶ Transferring ──▶ Active(new epoch)
   ▲                        │             │              │
   └────── abort ───────────┘             │              └── 失败回滚到 Frozen
                                          └── 快照 + WAL 位置打包
```

1. `Draining`：停止接收新业务消息，队列排空。
2. `Frozen`：旧节点停止处理，落一次全量快照并记录 WAL offset。
3. `Transferring`：新节点加载快照 + 回放 offset 之后的 WAL，写入 etcd `epoch+1`。
4. 旧节点此后收到的任何写入必须携带 `epoch`，小于当前值的直接拒绝。
5. 消息重放依赖 OpID 幂等，迁移期间的重复投递不会产生二次扣减。

### 6.5 时间轮

- 分层时间轮：槽 100 ms，Level0 512 槽（51.2 s），Level1 512 槽（7.3 h），Level2 覆盖 30 天。
- 每个定时器记录 `(ownerShard, actorID, timerID, deadline, kind, payloadRef)`。
- 到期事件按 `ownerShard` 批量路由（每 100 ms 一批）。
- 持久化：`TimerSet/TimerCancel` 写 WAL，崩溃后按快照 + WAL 重建。
- 精度 ±100 ms；**不允许**用 `time.After` 为每个行军建定时器。

### 6.6 失败与背压

- actor panic 被 supervisor 捕获：记录毒消息、回滚该条状态、继续处理；连续失败 3 次隔离聚合并告警。
- 节点级背压：邮箱水位 > 80% 时，网关对该分片的新请求返回「排队中」。
- 全局背压：WAL 落后 > 5 s 或快照复制滞后 > 10 min 时，暂停非关键写入。

---

## 7. 跨片协议

### 7.1 消息信封

```go
type Envelope struct {
    MsgID    string
    OpID     string // 幂等键
    WorldID  uint32
    SrcShard uint32
    DstShard uint32
    Src      ActorRef
    Dst      ActorRef
    Kind     uint16
    Epoch    uint32 // fencing
    Deadline int64
    Payload  []byte
}
```

### 7.2 幂等

- 每个 PlayerActor 维护 `seenOps`（最近 1024 条 OpID + 结果摘要），随快照持久化。
- 重复 OpID 直接返回上次结果。
- 重试：指数退避（50 ms → 1 s，最多 8 次），超过 `Deadline` 走补偿。
- 所有改变状态的操作必须有 OpID。

### 7.3 攻击 Saga

```
攻方 A                    守方 D
  │ 1. 预留部队 + WAL       │
  │ 2. ──ResolveAttack────▶│ 3. 校验/结算（或开 BattleActor）
  │                        │ 4. 扣守方损失、算战利品 + WAL
  │ 5. ◀──SettleResult─────│
  │ 6. 应用损失/战利品（OpID 去重）
  │ 7. WAL: MarchReturn
```

- 部队在步骤 1 被预留，不可能重复派出——双花在源头消灭。
- 守方是自身损失的唯一写者，攻方是自身部队的唯一写者，无需 2PC。
- 守方不可达超过 Deadline：攻方补偿（返程、无战利品）。
- 攻方崩溃：守方结果已持久化，重启后按 OpID 补拉。

### 7.4 交易（首版：定向交易）

只做 A 对 B 的定向交易，**不做拍卖行**。

```
a_locked → both_locked → settling → settled
      └──────────────┴──▶ refunding → refunded（超时/取消）
```

1. **A 发起**：PlayerActor A 校验并锁定资产到 `Escrow[tradeID]`，写 WAL；写 `trades` 文档 `state=a_locked`。失败立即退回。
2. **B 接受**：PlayerActor B 同样锁定，文档 CAS `a_locked → both_locked`。
3. **结算**：TradeService 发现 `both_locked`，向双方发 `SettleTrade{tradeID}`；双方校验 escrow，转入对方并记 `settledTrades`。
4. **收尾**：双方 ack 后文档 CAS `→ settled`。
5. **超时**：默认 10 分钟，CAS `→ refunding`，双方退款。

**为什么无 2PC**：escrow 中的资产不属于任何人，任何中间状态可退回；每个 PlayerActor 单写者；`tradeID` 幂等；Mongo CAS 是唯一权威顺序。

| 崩溃点 | 处理 |
|---|---|
| 锁定后、写文档前 | 对账扫孤儿 escrow，10 分钟退回 |
| A 入账、B 未入账 | 重试 Settle，B 幂等应用 |
| 协调器崩溃 | 后台 reconciler 扫表续跑 |
| 文档 settled、快照未落盘 | WAL 回放 + `settledTrades` 幂等 |

数据：`trades {_id, world, season, a, b, aAssets, bAssets, state, version, expireAt}`，TTL 审计 30 天。限制：等级 ≥ 20、每日 20 笔、单笔 5 个堆叠、白名单、税率 0–5%、审计。

### 7.5 超时与补偿矩阵

| 场景 | 超时 | 处理 |
|---|---|---|
| 跨片命令无应答 | 3×RTT 或 2 s | 重试（同 OpID），最多 8 次 |
| 守方长时间不可达 | Deadline 5 s | 攻方补偿：返程、退预留 |
| 区域预留未确认 | TTL 30 s | 自动释放地块 |
| 迁移中断 | 10 s | 回滚到 Frozen |
| 交易超时 | 10 min | 退款并记录 `trade_expired` |
| 跨服查询单 Group 超时 | 100 ms | 标记 partial |
| GroupRank 增量缺口 | 1 个同步周期 | 全量重同步 |
| WAL 落后 > 5 s | — | 暂停非关键写入并告警 |

---

## 8. 战斗实例化

### 8.1 确定性模拟（提交即结算）

- **到达即结算**：行军时间真实存在；到达瞬间由守方创建 `BattleActor`，用双方快照 + `seed` 一次性模拟到结束，战斗中不可操作。
- 输入：双方部队快照、科技/ buff、地形、`seed`、`tick=100 ms`。
- 输出：逐 tick 事件日志、最终损失、战利品、结果哈希。
- 同一输入重放必须得到同一哈希。

### 8.2 生命周期

```
守方创建 → BattleActor → 模拟至结束 → 结果持久化 → 通知双方 → 销毁
```

- 单场 200v200 约 2000 tick，< 50 ms；独立 worker 池，不占地图主循环。
- 未来实时指挥：把输入从「快照」换成「指令流 + tick 回放」，接口预留 `InputSource`。

### 8.3 反作弊

- 客户端只提交意图，不提交伤害数值。
- 保留输入日志 + 结果哈希，支持客服回放。
- 异常结果进入风控队列。

---

## 9. 存储与恢复

### 9.1 存储分层

| 层 | 介质 | 内容 | 作用 | 保留 |
|---|---|---|---|---|
| L0 | actor 内存 | 在线聚合 | 唯一权威写者 | 在线期间 |
| L1 | 本地 NVMe | 每 30 s 脏聚合快照 | 快速恢复（本机） | 最近 3 代 |
| L1' | 对等节点 NVMe | 同一快照的 2 份副本（RF=3） | 任意 2 节点丢失时接管 | 最近 2 代 |
| WAL | JetStream | 状态变更事件 | 回放快照之后的增量 | 10 min |
| L2 | MongoDB（区域内） | 离线聚合、订单、交易、邮件、回放、赛季榜 | 冷数据与最终兜底 | 永久（回放 90 天 TTL） |

### 9.2 为什么去掉 Redis

Redis 方案需 6 主 6 从、约 192 GB 预置内存承载 8 GB 数据，写入峰值约 270 MB/s，且多一个有状态集群。改为**本地 NVMe + RF=3**：每节点 680 MB 快照、23 MB/s 本机写 + 46 MB/s 复制，恢复直接读本机盘。

**快照不能替代 WAL**：全量快照每秒一次要 2 GB/s，不可行；WAL 才是 RPO ≤ 1 s 的关键。

### 9.3 本地快照与对等复制（RF=3）

```
snap-<world>-<shard>-<epoch>-<walOffset>-<ts>.bin
[magic 8B][version 2B][shard 4B][epoch 4B][walOffset 8B][count 4B]
[record: pid 8B | len 4B | zstd(protobuf state)]
[CRC32C 4B]
```

1. 写临时文件 → `fsync` → 原子 rename。
2. 更新 `manifest.json`。
3. gRPC 流（4 MB chunk）推送给环上**后两个节点**，对端校验 CRC 后 ack。
4. 收到 ≥ 1 个 ack 才允许截断 WAL。

保留：本地 3 代、对端 2 代；复制滞后 > 5 min 告警，> 10 min 背压。任意 2 台丢失仍有 1 份副本；3 台同时丢失才回退 MongoDB。

### 9.4 数据模型

**MongoDB（每区域）**：3 分片 × 3 副本，单库共库，分片键 `{worldID: 1, playerID: "hashed"}`。

| 集合 | 文档 | 说明 |
|---|---|---|
| `players` | `{_id:{world,pid}, profile, state:BinData, version, updatedAt, lastLoginAt}` | 在线/近期玩家 |
| `player_archive` | 同上 | 7 天未登录归档 |
| `player_power` | `{world, pid, power, tie, version, updatedAt}` | 排行榜重建投影 |
| `orders` / `payments` | 订单号、玩家、金额、状态、流水 | 唯一索引 + 幂等键 |
| `trades` | `{_id, world, season, a, b, aAssets, bAssets, state, version, expireAt}` | 交易状态机 |
| `mail` | 邮件与附件领取状态 | 按 pid |
| `battle_logs` | `{battleID, seed, inputs, resultHash, createdAt}` | 回放，90 天 TTL |
| `sessions` | `{jti, pid, refreshHash, expiresAt, revoked}` | refresh token |
| `seasons` | `{_id:{world,season}, crossGroup, reset:{scope,timing,sweepHours}, rankOrder:{name,version}, ...}` | 赛季配置 |
| `season_rankings` | `{_id:{world,season,pid}, rank, power, tie, sortKey}` | 历史榜 |
| `season_top` / `cross_season_top` | Top-K 快照 | 历史榜展示 |
| `group_rank_snap` | 合并索引快照 | 故障恢复 |
| `season_settlements` / `season_rewards` | 结算幂等 / 发奖唯一键 | 防重 |
| `worlds` | `{_id, region, name, crossGroup, mergedInto}` | 世界目录 |
| `audit` | 管理操作、风控事件 | 永久 |

### 9.5 刷盘与 RPO

| 操作 | 策略 | RPO |
|---|---|---|
| 普通指令 | 内存 + WAL 异步（50 ms 批量） | ≤ 1 s |
| 充值/交易/跨玩家结算 | 同步 WAL（3 副本）后才应答 | 0 |
| 快照 | 30 s 错峰，只写脏聚合 | 由 WAL 补齐至 ≤ 1 s |
| 快照复制 | 异步 RF=3，≥1 ack 才允许截断 | ≤ 30 s |
| GroupRank 快照 | 每 5 min 写 Mongo | 可由增量补齐 |
| MongoDB | `w:majority` + journal | ≤ 60 s |
| 赛季归档 | 幂等重试 | 完成后不可变 |
| 3 台副本同时丢失 | 每日 Mongo 全量兜底 | ≤ 24 h（灾难） |

### 9.6 恢复流程

1. 节点启动，从区域 etcd 读取分片与 epoch。
2. 加载本机最新快照；缺失/CRC 失败 → 向对等节点拉取（最多 2 份）。
3. 回放 WAL；重建定时器。
4. 节点整体丢失：从其余 2 份副本取最新快照 + WAL。
5. 3 份副本都不可用：回退 Mongo，进入人工核对。
6. GroupRank：加载快照 + 16 个 world 增量；缺口用 `player_power` 重建（320 万条，15–30 s）。
7. 交易：TradeService 扫 `trades` 的 `a_locked/both_locked`，推进或退款。

目标：本机恢复 < 1 min；跨节点接管 < 2 min；GroupRank 接管 < 60 s。

### 9.7 冷热分层

- 在线：内存 + 30 s 快照（本机 + 2 副本）。
- 7 天未登录：内存卸载，MongoDB `player_archive` 长期保存。
- 内存水位 > 80%：卸载最久未活跃聚合。
- 30 天无活跃 world：榜单与合并索引停止同步，访问时重建。
- 赛季结束：全量排序与 Top-K 归档。

### 9.8 排行榜

#### 9.8.1 排序规则：唯一名次 + 可插拔 RankOrder

默认战力降序、同分按 `pid` 升序：

```
rank(player) = 1 + count( key < rankKey(player) )
```

```go
type RankKey [24]byte            // [^power][tie][pid]，大端
type RankOrder interface {
    Name() string
    Version() uint32
    Key(e RankEntry) RankKey
}
```

- 默认 `default@1`：`key = (^power, 0, pid)`。
- 自定义实现首版不内置，只留接口；按 `name@version` 注册，未注册的名字启动失败。
- 规则写入 `seasons.rankOrder`，赛季内不可变；三张榜必须同版本。
- 24 字节定长 key：跳表字节比较、Mongo `sortKey` 范围计数、历史口径一致。

#### 9.8.2 本世界：WorldRankActor

- 每 `(world, season)` 一个 actor，etcd 租约 10 s，单写者。
- order-statistic skiplist（key 为 `RankKey`，带 `span`），支持 `TopK/RankOf/Around`。
- 更新带 `oldKey/newKey/version`，同一 actor 内原子删旧插新。
- 内存约 20 MB/world；重建从 `player_power`（< 1 s）。

#### 9.8.3 跨服：分层合并索引

- 单全局索引不可行：1.024 亿条、约 8 GB、100 万次/秒更新。
- 每 16 world 一个 `GroupRankActor`，持有 320 万条全序索引（约 256 MB）。
- 默认 16 world → 1 组；上限 512 world → 32 组。
- 更新：WorldRankActor 每 100 ms 批量推 `(pid, oldKey, newKey, version)`；组内单 goroutine 原子删旧插新。
- 名次：`1 + Σ CountAbove(rankKey)`，多组并行，p99 目标 < 100 ms；缓存 5 s。
- Top-K：合并各组 `TopK(200)`，位置即名次；缓存 1 s。
- 重建：5 min 快照 + 增量；缺口全量重建（15–30 s）。

#### 9.8.4 赛季历史

```
active → settling → 导出 season_rankings/season_top → 合并 cross_season_top
       → 发奖 → 切换赛季 → 重置 → archived
```

- 单 world 名次 O(1)；跨服历史 Top-K 已固化。
- 跨服历史名次：每 world `count(sortKey < mySortKey)`，并行 + 缓存，p99 < 200 ms。
- 归档 ≈ 24 MB/world/season。

#### 9.8.5 赛季重置：范围与时机

**范围：只重置赛季进度，不动英雄与付费资产。**

| 类别 | 字段 | 处理 |
|---|---|---|
| 赛季进度 | 战力、城建、科技、部队、资源、地图领地、赛季积分 | 重置 |
| 英雄与养成 | 英雄等级、装备、专精 | 保留 |
| 账号资产 | 充值货币、皮肤、VIP、成就 | 保留 |
| 社交 | 好友、联盟 | 保留（联盟是否解散由玩法定） |

**时机：混合（惰性 + 24h 扫在线）。**

1. 结算完成、发奖后切换到新 `seasonID`。
2. 玩家首次登录/首条指令：检查 `state.SeasonID != currentSeason`，执行一次 `ApplyReset`，写回 `SeasonID`。
3. 开服 24h 内后台任务分批扫在线玩家主动重置。
4. 新赛季榜只收 `SeasonID == current` 的更新。
5. 幂等：`SeasonID` 写回即完成，重复登录不重复扣减。

顺序固定：**归档 → 发奖 → 切换赛季 → 重置**。

#### 9.8.6 备选：并列名次

若产品接受「1,1,3」，跨服只需分数分布，内存大降；当前需求是唯一名次，仅作备选。

#### 9.8.7 一致性与降级

| 场景 | 行为 |
|---|---|
| GroupRank 滞后 < 200 ms | 正常 |
| 滞后 > 5 s | 标注「更新中」 |
| 单个 Group 超时 | partial，返回其余组求和 |
| GroupRank 重建 | 返回上一份快照并标注 |
| 赛季切换瞬间 | 旧赛季只读，新赛季空榜 |
| 历史未归档 | 返回「未归档」，不猜测 |
| 排序版本不一致 | 拒绝服务并重建 |
| 跨服组配置变更 | 只在赛季边界生效 |

### 9.9 存储容量汇总

| 项 | 估算 |
|---|---|
| 本地快照 | 680 MB/节点；3 代 ≈ 2 GB |
| 对等副本 | 每节点额外 1.4 GB |
| 复制带宽 | 46 MB/s/节点（RF=3） |
| WAL | ~1.2 GB/节点（10 min） |
| MongoDB | 24 GB/世界 + 赛季榜 |
| 排行 | 默认 320 MB（世界榜）+ 256 MB（合并组） |

---

## 10. 网关与协议

### 10.1 连接与会话（无状态）

- 每 Gateway 1–2 万 WS 连接，20 万在线需 12–20 个网关。
- **access token**：JWT（Ed25519），15 min，含 `pid/world/region/exp/jti`；网关本地校验。
- **refresh token**：30 天，哈希存区域 MongoDB `sessions`。
- **踢人**：区域 NATS 广播 `kick.<pid>`。
- 断线重连：客户端带 `stateVersion`；PlayerActor 保留最近 256 个增量。

### 10.2 协议

- `seq` 单调递增，服务端 `ack(seq)`；超时重发同 `seq`，服务端按 OpID 幂等。
- 推送带 `stateVersion`，客户端按序应用。
- protobuf；压缩阈值 512 B；心跳 10 s。

### 10.3 限流与防刷

- 每连接令牌桶（20 条/s，突发 40）；登录、交易、排行榜独立限流。
- PlayerActor 内每玩家 50 条/s 预算。
- 非法指令计数，超阈值断连并风控。

---

## 11. 集群与运维（K8s 多区域）

### 11.1 区域模型

- 每区域一个独立 K8s 集群，包含完整一套 Gateway/Shard/etcd/NATS/Mongo。
- 玩家注册时归属一个 home region；GeoDNS/Anycast 路由到最近区域。
- 区域之间**不共享** etcd/NATS/Mongo，也不做实时状态同步。
- 每区域默认 16 个 world（1 个合并组），由 `regions.defaultWorlds` 配置；后续按运营增加。
- 跨服组在区域内成组；跨区域排行榜/跨区玩法延后。
- 全球控制面：GitOps、账号目录、Prometheus 联邦/Thanos、日志聚合。
- **区域故障**：v1 不做世界自动接管；只做入口切流与人工恢复。

### 11.2 部署拓扑（单区域）

| 组件 | 数量 | 规格 | K8s 形态 |
|---|---|---|---|
| Gateway + RankQuery + TradeService | 12–20 | 8C/16G | Deployment + HPA，L4 LB 接入 |
| Shard Node | 12 | 16C/32G/10Gbps + 1 TB NVMe | StatefulSet + local PV，跨 AZ 反亲和 |
| etcd | 3 | 4C/8G | StatefulSet，独立 PV |
| NATS | 3 | 8C/16G | StatefulSet + JetStream PVC |
| MongoDB | 3 分片 × 3 副本 | 16C/64G | Operator 或托管，跨 AZ |

- 快照对等节点依赖 StatefulSet 的 `pod-N` DNS + headless Service。
- Shard Node 不用 HPA；扩缩容走「加入 → 迁移 → 摘除」。
- 区域上线：新建集群 → 部署 → 注册 world/区域 → GeoDNS 切流。

### 11.3 成员与租约

- 区域 etcd lease 15 s，每 5 s 续约；失联 30 s 触发迁移。
- 分片表版本号单调递增，watch 增量更新。
- 迁移先拿 etcd CAS epoch，旧节点写入因 epoch 过期被拒。
- Rank actor 独立租约（10 s）。

### 11.4 滚动升级

1. 摘流 → 2. 迁移/排空 → 3. 升级 Pod 并回归 → 4. 重新加入。

### 11.5 混沌测试

- kill Pod、etcd follower、NATS 节点；同时 kill 一台节点的 2 份副本。
- 注入跨 AZ 延迟/丢包；模拟入口 DNS 故障。
- 校验：无负数资源、无重复扣减、无丢失行军、交易无钱货两空、名次校验为 0。

---

## 12. 可观测性

| 指标 | 采集点 | 告警阈值 |
|---|---|---|
| `actor_mailbox_depth` | 运行时 | p99 > 200 持续 1 min |
| `actor_process_p99` | 运行时 | > 20 ms |
| `cross_shard_msg_rate` | 运行时 | 突增 3× 或失败率 > 1% |
| `timer_lag` | 时间轮 | > 500 ms |
| `wal_append_lag` | 存储 | > 2 s |
| `snapshot_write_lag` | 快照 | > 60 s |
| `snapshot_replicate_lag` | 快照复制 | > 5 min |
| `group_rank_sync_lag` | 跨服索引 | > 5 s |
| `group_rank_gap_total` | 跨服索引 | > 0 |
| `rank_order_mismatch_total` | 排序规则 | > 0 |
| `cross_rank_query_p99` | 跨服名次 | > 100 ms |
| `trade_settle_failures` | 交易 | > 0 |
| `trade_orphan_escrow` | 交易对账 | > 0 |
| `reset_sweep_lag` | 赛季重置 | 24 h 内未完成 |
| `season_settle_duration` | 赛季结算 | > 10 min/world |
| `region_gateway_5xx` | 区域接入 | > 1% |
| `login_success_rate` | 网关 | < 99% |

- 全球：Prometheus 联邦/Thanos；日志按区域聚合；`MsgID` 全链路，采样 1%。
- 每日对账：资源守恒、部队守恒、订单流水、交易资产守恒。

---

## 13. 安全与合规

- TLS；access token 15 min + refresh 轮换；JWT 私钥按区域 KMS。
- 服务端权威；`seq` + `OpID` + 时间窗防重放。
- 内部 mTLS；etcd/NATS/Mongo 只开内网；快照盘加密。
- **数据驻留**：账号/PII 留在 home region，不跨区复制；专项合规评审延后。
- 交易风控：等级/额度、同 IP/设备告警、白名单、审计。
- 区域合规差异（实名/防沉迷）在区域层实现。

---

## 14. 性能模型与压测

### 14.1 模型

```
rps        = CCU × 人均指令率 × 峰值系数
跨片 rps    = rps × 跨片比例
每节点负载  = 分片数 × 每分片消息率 + 定时器到期率
WAL 带宽    = 写指令率 × 平均消息大小 × 副本数
复制带宽    = 脏快照大小 / 快照间隔 × (RF - 1)
跨服同步    = world 数 × 每秒批量包数
交易负载    = 笔数/s × 每笔文档写次数
```

20 万 CCU、0.2 条/s、峰值 3×、跨片 20%：12 万 rps，2.4 万跨片 rps，24 MB/s WAL，46 MB/s 复制；默认 16 world 每 100 ms 约 160 个批量包/秒。

### 14.2 压测场景

| 场景 | 描述 | 观察 |
|---|---|---|
| 登录风暴 | 5 分钟内 20 万登录 | 网关 CPU、快照加载、Mongo 回源 |
| 日常 | 20 万 bot 随机操作 2 h | p99、错误率、内存 |
| 王城争夺 | 5000 玩家集中同一区域 | 热点邮箱、战斗池、地图 tick |
| 集结战 | 200v200 × 50 场并发 | 结算延迟、跨片消息 |
| 交易 | 100 笔/s，含超时/取消 | 锁定/入账延迟、孤儿 escrow |
| 跨服榜 | 默认 16 world；上限 512 world | 同步滞后、CountAbove p99、fan-out |
| 赛季结算 | 默认 16 world + 24h 重置扫描 | 归档、发奖幂等、重置完成率 |
| 区域故障 | kill 区域入口/DNS，切流到备用入口 | 会话重连、区域隔离、告警 |

### 14.3 验收

- 20 万 CCU 稳定 2 h：错误率 < 0.1%，p99 < 200 ms，无 OOM。
- 王城：区域邮箱不打满，战斗 p99 < 1 s。
- 交易：100 笔/s 下 p99 < 100 ms；崩溃注入无钱货两空。
- 跨服：默认 16 world 名次 p99 < 100 ms；Top-100 p99 < 50 ms；暴力校验 0 差异。
- 赛季：单 world 结算 < 10 min；重复发奖 0；24h 在线重置完成率 100%。
- 故障：RTO < 2 min；对账 0 差异；GroupRank 接管 < 60 s。

---

## 15. 技术选型（ADR 摘要）

| 决策 | 选项 | 结论 | 理由 | 风险 |
|---|---|---|---|---|
| Runtime | goakt / protoactor-go / 自研 | **goakt v4.6.4** | 官方最新稳定版；分片/定时器仍自研 | 新版本回归；可回退 v4.6.3 |
| 传输 | gRPC / NATS | 内部 NATS，网关↔分片 gRPC | 扇出与重试 / 低延迟请求应答 | 双栈运维 |
| 部署 | 单区域 VM / 多区域 K8s | **多区域 K8s** | 区域隔离、弹性、GitOps | 有状态负载运维复杂 |
| 全球形态 | 单世界跨区 / 区域多 world | **区域多 world，默认 16/区域** | 延迟与合规可控；world 数可配 | 全球玩家不能同世界 |
| WAL | JetStream / Kafka | JetStream | 与 NATS 同栈；3 副本 | 大流量调优 |
| 热快照 | Redis / 本地 NVMe + RF=3 | **本地 NVMe + RF=3** | 省内存；容忍 2 节点丢失 | 自研复制/校验 |
| 跨服榜 | 全局索引 / 分层合并 | **16 world/组分层合并** | 唯一名次要求全序；上限 512 world | fan-out、内存 |
| 交易 | 拍卖行 / 定向交易 | **定向交易 + escrow** | 无 2PC、可对账 | 风控/RMT |
| 赛季重置 | 全量 / 仅赛季进度 | **仅赛季进度，混合时机** | 保留付费资产 | 重置遗漏 |
| 会话 | Redis / JWT | **JWT + Mongo refresh** | 无共享会话存储 | 撤销窗口 |
| 冷存储 | MongoDB / PostgreSQL | **MongoDB 3 分片共库** | 文档模型匹配快照 | 事务弱 |
| 战斗 | 确定性模拟 / 逐帧 | **提交即结算** | 可回放、成本低 | 战斗中不可操作 |

## 16. 风险登记册

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | 王城热点打爆单分片 | 高 | 高 | 战斗实例化 + hot shard + 区域再切分 |
| R2 | goakt v4.6.4 回归或集群语义不匹配 | 中 | 高 | P0 回归；版本锁定；可回退 v4.6.3；`ActorSystem` 隔离 |
| R3 | saga 补偿漏洞导致资源不守恒 | 中 | 高 | 幂等 + 回放 + 每日对账 |
| R4 | 20 万压测成本高 | 中 | 中 | bot 集群 + 云上短时压测 |
| R5 | 3 份副本同时丢失回档 | 低 | 高 | 跨 AZ + 每日 Mongo 全量 + 演练 |
| R6 | 快照复制滞后/损坏 | 中 | 高 | CRC + manifest + 截断门禁 |
| R7 | 迁移期间消息丢失 | 低 | 高 | 冻结 + WAL offset + OpID |
| R8 | 冷聚合加载尖刺 | 中 | 中 | 预加载 + 限流 + 索引 |
| R9 | 跳表 rank 错位 | 中 | 高 | 属性测试 + 每日暴力校验 |
| R10 | JWT 撤销窗口 | 低 | 中 | 短 TTL + kick |
| R11 | Mongo 归档延迟/写放大 | 中 | 中 | outbox + 批量 upsert |
| R12 | 合并组同步缺口 | 中 | 高 | seq + 全量重同步 + 校验 |
| R13 | 赛季结算中断/重复发奖 | 中 | 高 | 幂等状态机 + 唯一键 |
| R14 | 历史榜膨胀 | 中 | 中 | 分区 + 3 年热存 |
| R15 | 跨服组/重置策略不一致 | 低 | 中 | 组级校验 + 赛季边界生效 |
| R16 | 同分规则不被接受 | 中 | 中 | pid 可改；RankOrder 预留 |
| R17 | 重置遗漏/重复扣减 | 中 | 高 | 惰性 + SeasonID 幂等 + 扫描 |
| R18 | GroupRank 重建 > 60 s | 中 | 中 | 快照 + 增量 + 多组互备 |
| R19 | RankOrder 版本切换重建 | 中 | 中 | 版本写入 seasons + 启动校验 |
| R20 | 多组 fan-out p99 超标 | 中 | 中 | 根聚合（每 8 组） |
| R21 | 交易 RMT/工作室 | 中 | 中 | 限额 + 设备/IP 告警 + 审计 |
| R22 | 孤儿 escrow | 中 | 中 | 对账任务 10 分钟退回 |
| R23 | 重置扫描 24h 未完成 | 中 | 中 | 优先在线 + 登录兜底 |
| R24 | 区域入口/DNS 故障导致该区不可用 | 中 | 高 | 多入口 Anycast + 切流演练 + 人工恢复 |
| R25 | 区域合规差异 | 中 | 高 | 区域层实现；跨境专项延后 |

## 17. 演进路线

| 阶段 | 范围 | 退出标准 | 周期 |
|---|---|---|---|
| P0 | 单机：goakt v4.6.4 接入 + ActorSystem 抽象 + Player/Region + 时间轮 + WAL + 本地快照 + WorldRankActor + RankOrder + 定向交易 + 1k bot | 1k bot 稳定 1 h；崩溃可回放；交易崩溃注入无钱货两空 | 6–8 周 |
| P1 | 单区域 K8s：分片表、租约、迁移、RF=3、跨片 saga、GroupRank、交易对账、5 万 bot | kill Pod RTO < 2 min；名次/交易对账 0 差异 | 8–12 周 |
| P2 | 20 万压测、战斗实例化、热点隔离、默认 16 world 跨服、赛季结算与重置、第二个区域上线 | 通过 §14.3；新区域不改代码上线 | 8–12 周 |

团队分工（8 人）：

- 2 人：runtime（goakt）/ 分片 / 迁移 / 时间轮
- 2 人：玩法（城市、行军、资源、建造）
- 1 人：战斗模拟
- 1 人：存储（快照/WAL/Mongo）、交易对账、赛季结算
- 1 人：网关、JWT、协议、排行榜
- 1 人：K8s、压测、可观测性

### 17.1 单 world 扩容方案（默认阈值，可调）

单 world 从 20 万向 30/50 万扩展时，按以下阶梯处理，**优先不改架构**：

| 阶段 | 触发条件（默认值） | 动作 | 风险 |
|---|---|---|---|
| S1 加节点 | CCU > 70% 容量，或 actor p99 > 30 ms 持续 7 天 | Shard Node 12 → 16/20，哈希自动再平衡 | 迁移期间跨片消息上升 |
| S2 加网关 | 单网关 > 1.5 万连接，或入口 p99 > 50 ms | Gateway 扩到 24–32 | 会话重连抖动 |
| S3 抬规格 | 内存 > 70%，或快照复制 > 60% 带宽 | 16C/32G → 32C/64G | 故障域变大 |
| S4 拆热点子系统 | 世界级单写者 p99 超标 | Rank 按分数段分片；世界事件按地区拆 | 逻辑复杂度上升 |
| S5 拆 world | 单 world 持续 > 50 万 | 新开 world + 赛季分流；不做在线拆服 | 社区割裂 |

- 以上阈值是**默认配置**（附录 D），不是硬编码；用真实数据校准后调整。
- 前三级不改代码，只改副本数与规格；S4/S5 单独评审。
- 扩容只在维护窗口执行；扩容后跑 24 h 稳定性与对账。

## 18. 待定事项与延后清单

### 18.1 已定默认（可调，不阻塞开发）

1. **world 数**：默认每区域 16 个（1 个合并组），配置项 `regions.defaultWorlds`；具体数量由运营调整。
2. **扩容阈值**：默认 70% 水位、30 ms p99、50 万拆 world 线；全部走配置，按真实数据校准。
3. **goakt 版本**：锁定 v4.6.4；升级由开发者按需决定，升级前跑回归。

### 18.2 延后（不阻塞 v1.0）

- **拍卖行**：不做；未来若做，单服拍卖优先。
- **单世界全球同服**：延后。
- **跨区域排行榜**：暂不考虑。
- **数据跨境合规专项**：暂不处理；区域层先满足本地合规。
- **自定义 RankOrder 首个实现**：接口已预留。
- **512 → 1024 world**：届时评估根聚合/分带索引。
- **历史跨服名次延迟优化**：当前方案够用。
- **合服策略**：`worldID` 不复用，`mergedInto` 已预留。
- **跨区域账号迁移/全球账号**：延后。
- **区域故障自动接管**：已确认 v1 不做，人工恢复。

---

## 附录 A：核心接口

```go
type Actor interface {
    Receive(ctx Context, env *Envelope)
}

type Context interface {
    Self() Ref
    Tell(dst Ref, env *Envelope)
    Ask(ctx context.Context, dst Ref, env *Envelope) (*Envelope, error)
    Schedule(d time.Duration, kind uint16, payload any) TimerID
    Cancel(TimerID)
    Now() time.Time
}

type PlayerState struct {
    PlayerID  uint64
    SeasonID  uint32
    Escrow    map[string]Assets
    Settled   Ring[string]
    Resources Resources
    City      CityState
    Marches   []MarchState
    Queues    []QueueState
    SeenOps   Ring[OpID]
    Version   uint64
}

type RankEntry struct {
    PID   uint64
    Power int64
    Tie   int64
}

type RankKey [24]byte

type RankOrder interface {
    Name() string
    Version() uint32
    Key(e RankEntry) RankKey
}
```

## 附录 B：建议目录结构

```
cmd/gateway            WS 接入 / JWT 校验
cmd/world              分片节点
internal/actor          ActorSystem 抽象 / envelope / supervisor
internal/runtime/goakt  goakt 适配实现
internal/shard          ring / lease / migration / fencing
internal/timer          hierarchical wheel
internal/wal            append / replay / truncate
internal/snapshot       write / verify / replicate / restore
internal/rank/world     skiplist / lease / rebuild
internal/rank/group     merged skiplist / batch apply / snapshot
internal/rank/order     rank key encoding / registry / versions
internal/rank/season    settlement / archive / reset policy
internal/rank/query     fan-out sum / top-k merge / cache
internal/trade          escrow / state machine / reconciler
internal/auth           jwt / refresh / kick
internal/world          player / region / march / building
internal/battle         deterministic simulation
internal/ledger         orders / payments
internal/store/mongo    archive / orders / trades / mail / battlelog / sessions
internal/protocol       protobuf / envelope / errors
internal/observability  metrics / trace / audit
deploy/k8s              helm / kustomize / gitops
deploy/regions          区域清单与灰度切流
test/sim                虚拟时钟确定性回放
test/bot                压测 bot 集群
```

## 附录 C：状态机

```
March:  Idle → Marching → Arrived → Fighting? → Returning → Idle
                  │                     │
                  └──── Recalled ───────┘
                                   └── Dead（全灭）

Shard:  Active → Draining → Frozen → Transferring → Active(epoch+1)
                    │            │
                    └── abort ───┘

Rank:   Loading → Serving → Rebuilding → Serving
                    │            │
                    └── lease lost ──▶ Loading

Season: active → settling → archived
              │          │
              └── abort ─┘（回滚到 active）

Trade:  a_locked → both_locked → settling → settled
            │            │
            └────────────┴──▶ refunding → refunded
```

## 附录 D：默认配置

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `regions.defaultWorlds` | `16` | 每区域默认 world 数（1 个合并组） |
| `rank.groupSize` | `16` | 合并组大小（world 数） |
| `capacity.scaleTrigger` | `0.70` | 扩容水位（CCU/容量） |
| `capacity.actorP99` | `30ms` | 扩容 actor p99 阈值 |
| `capacity.splitWorldAt` | `500000` | 单 world 拆服阈值 |
| `trade.timeout` | `10m` | 交易超时 |
| `trade.maxDaily` | `20` | 每日交易笔数 |
| `reset.sweepHours` | `24` | 重置扫描窗口 |
| `snapshot.interval` | `30s` | 脏聚合快照间隔 |
| `snapshot.rf` | `3` | 快照副本数 |
| `shard.virtualNodes` | `256` | 每个节点的虚拟节点数 |
| `rank.fanoutTimeout` | `100ms` | 跨服名次单组超时 |

---

*本文档为 v1.2：默认值已定并列在附录 D；§18.1 仅列可调项，§18.2 为延后清单。*
