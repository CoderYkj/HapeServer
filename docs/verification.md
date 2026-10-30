# 骨架临时验证记录（冒烟）

> 本记录只说明骨架可编译、单测可跑，不代表 P0 验收。正式验收标准见 `docs/p0-task-plan.md` §7。

| 项 | 值 |
|---|---|
| 日期 | 2026-10-30 |
| 平台 | Windows / amd64 |
| Go | 1.27.3 |
| goakt | v4.6.4 |
| 模块 | hapeserver |

## 命令与结果

```text
> go build ./...
（无输出，exit 0）

> go test ./...
ok  hapeserver/internal/gateway
ok  hapeserver/internal/rank
ok  hapeserver/internal/shard
ok  hapeserver/internal/snapshot
ok  hapeserver/internal/timer
ok  hapeserver/internal/trade
ok  hapeserver/internal/wal
?   hapeserver/cmd/...                 [no test files]
?   hapeserver/internal/actor          [no test files]
?   hapeserver/internal/config         [no test files]
?   hapeserver/internal/observability  [no test files]
?   hapeserver/internal/protocol       [no test files]
?   hapeserver/internal/runtime        [no test files]
?   hapeserver/internal/runtime/goakt  [no test files]
?   hapeserver/internal/runtime/inmem  [no test files]
?   hapeserver/internal/world          [no test files]
```

## 依赖获取说明

当前环境直连 `proxy.golang.org` 超时，使用以下设置完成 `go mod tidy`：

```powershell
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOSUMDB = "off"
go mod tidy
```

`go.sum` 已生成并纳入仓库（尚未提交 git）；正常网络环境下直接 `go build ./...` 即可，无需这些环境变量。

## 尚未执行

| 项 | 原因 / 计划 |
|---|---|
| `go test -race ./...` | Windows 下 race 需要 CGO/工具链；由 CI（ubuntu）执行 |
| `docker compose up -d` | 本机未安装 Docker；NATS/Mongo 集成测试在 CI 或开发机执行 |
| `cmd/world` 长驻冒烟 | 会阻塞等待信号；P0 M1 引入健康检查与优雅停机测试后补 |
| 1k bot 压测 | 依赖 H03/H04，尚未实现 |

## 复现

```powershell
cd D:\HapeServer
go build ./...
go test ./...
```
## 增量验证（2026-10-30）

### F02 跳表

- `go test ./internal/rank -run TestAgainstSortedModel`：20,000 次随机 add/remove/rank/top-k，与排序数组模型 0 差异。
- 基准（i7-4980HQ，10 万条）：`Add` 约 1.78µs/op，`RankOf` 约 0.88µs/op。

### C02 JetStream WAL

- 本地 `nats-server 2.15.2`，JetStream 文件存储。
- `NATS_URL=... go test ./internal/wal -run TestJetStreamRoundTrip` 通过：Append 3 条、Replay 顺序正确、Truncate(2) 后仅剩 b/c。
## 增量验证：F03 / C04 / G01（2026-10-30）

### F03 WorldRankActor

- 更新带版本号：旧版本被丢弃，新版本生效。
- `Rebuild` 用投影重建并保留版本水位，重建后的旧更新仍被拒绝。
- `TopK` / `RankOf` 与底层跳表一致；非法 JSON 不影响状态。

### C04 快照存储

- 原子写：临时文件 → fsync → rename。
- 每个 shard 保留最近 3 代，自动清理更老文件。
- 最新快照 CRC 损坏时，`LoadLatest` 自动回退到上一代；无快照返回 `ErrNoSnapshot`。

### G01 Mongo 交易仓储

- `trades` 集合，`_id` 为 tradeID；`Transition` 以 `(state, version)` 做 CAS，冲突返回 `ErrConflict`。
- `ListPending` 只返回 `a_locked` / `both_locked`。
- 本机无 mongod，集成测试 `TestMongoRepo` 在未设置 `MONGO_URL` 时跳过；状态机与 CAS 逻辑由单测覆盖。

### 结果

```text
go build ./...  → 通过
go vet ./...    → 通过
go test ./...   → 全部通过（Mongo/JetStream 集成用例在无外部服务时跳过）
```
## 增量验证：F04 / C05 / G02（2026-10-30）

### F04 排行重建接线

- `PowerProjection` 接口 + `WorldRankActor.RebuildFrom`。
- `MongoProjection` 读取 `player_power`（按 world 过滤）。
- 单测：重建后名次正确、版本水位保留、重建后的旧更新仍被拒。

### C05 恢复编排

- `Recover`：最新有效快照 → 应用 → 只回放 `offset > snapshot.walOffset` 的 WAL 记录。
- 无快照时回放全部 WAL；`ApplyRecord` 出错立即停止并返回最后偏移。
- 单测覆盖：快照 + 尾部、纯 WAL、apply 失败三条路径。

### G02 escrow

- `PlayerActor.LockEscrow / RefundEscrow / SettleEscrow`。
- 锁定扣减可用余额；同一 tradeID 幂等；余额不足返回 `ErrInsufficient`。
- 退款原路返还；结算后重复 settle 不再入账；结算后退款返回 `ErrTradeClosed`。

### 结果

```text
go build ./...  → 通过
go vet ./...    → 通过
go test ./...   → 全部通过
```
## 增量验证：G03 / G04（2026-10-30）

### G03 TradeService

- `Create`：锁定 A → 写文档；写失败自动退回 A。
- `Accept`：锁定 B → `a_locked → both_locked`；CAS 失败自动退回 B。
- `Settle`：`both_locked → settling`，双方 escrow 结算后 `→ settled`；A 已结算、B 失败时停在 settling，可重入。
- `Cancel`：`a_locked/both_locked → refunding → refunded`，双方原路退回。
- `Reconcile`：settling 续结算，过期单自动退款。

### G04 失败注入

- 创建文档失败：A 余额恢复，无残留文档。
- 接受时 CAS 冲突：B 退款，单据仍是 a_locked。
- 部分结算失败：第一次返回错误、状态 settling；第二次或 Reconcile 完成，且不重复入账。
- 过期取消：双方余额恢复，状态 refunded。

### 结果

```text
go vet ./...   → 通过
go test ./...  → 全部通过
```
## 增量验证：H02 / H03 / H04（2026-10-30）

### Ask/Respond 支持

- `actor.Envelope` 增加进程内 `Reply` 通道，`actor.Context` 增加 `Respond`。
- inmem 运行时实现真正的 Ask/Respond；goakt 适配用 `ReceiveContext.Response`。
- `PlayerActor` 通过 `ctx.Respond` 返回 `{ok,error}`，两种运行时行为一致。

### 世界 HTTP API

- `cmd/world` 启动 goakt 系统、快照存储和 `world.Service`。
- `POST /api/command`：grant / setpower / rank / top / trade_create / trade_accept / trade_settle / trade_cancel。
- `GET /api/rank?player=N`；`GET /healthz`。
- 启动时从最新快照恢复玩家；后台每 10s 落一次快照，退出时再落一次。

### 100 bot 冒烟

```text
bots=100 total=2380 ok=2380 err=0 err_rate=0.0000
p50=2.03ms p99=30.26ms
```

- 压测机与 world 同机（i7-4980HQ），goakt 运行时。
- 重启 world 后 `/api/rank?player=1` 仍能返回名次，快照恢复生效。
- 快照大小约 31 KB（100 个玩家）。

### 当前限制

- WAL 尚未接进 `cmd/world`，恢复只依赖快照；C05 的编排已就绪，待接线。
- 交易存储用 `trade.MemoryStore`，Mongo Repo 尚未在启动时选择。
- 压测走 HTTP，不是最终 WebSocket 网关；1k bot 尚未跑。
## 增量验证：WAL 接线与进程级恢复（2026-10-30）

### 接线

- `cmd/world` 启动时按 `storage.walKind` 打开 `memory` 或 `jetstream` WAL。
- `world.Service` 在应用玩家效果前写 WAL（grant / setpower / escrow lock/refund/settle）。
- 启动走 `recovery.Recover`：最新快照 + WAL 尾部；快照保存时记录当前 `wal.LastOffset()`。
- 每 10s 快照一次，退出时再落一次。

### kill -9 测试

环境：本地 `nats-server 2.15.2`（JetStream 文件存储），Go 1.27.3，goakt v4.6.4。

```text
1. 启动 world（memory=false / jetstream）
2. setpower player=1 power=777   → ok
3. grant    player=1 gold=50     → ok
4. Stop-Process -Force（等价 kill -9）
5. 重启 world（同一 stream/subject）
6. state player=1
```

结果：

```json
{"playerId":1,"resources":{"gold":1050,"wood":1000,"food":1000},"power":777,...}
```

`power` 与 `gold` 均从 WAL 回放恢复，说明进程级恢复链路成立。

### 已知限制

- 交易文档仍在 `trade.MemoryStore`，进程重启后文档丢失；玩家 escrow 效果由 WAL 回放，但 trade 状态需要切 Mongo 才能闭环。
- 快照每 10s 一次；两次快照之间的状态依赖 WAL，WAL 未做截断/轮转。
## 增量验证：Mongo 选择 / 1k bot（2026-10-30）

### Mongo 交易存储选择

- `storage.mongoUrl` 非空时，`cmd/world` 用 `trade.OpenMongo` 打开 `trades` 集合；为空时回退 `trade.MemoryStore`。
- 启动日志会打印 `trade store: mongodb db=...` 或 `trade store: memory (durability is dev-only)`。
- 本机无 mongod，Mongo 分支只验证了编译与配置选择；集成用例 `TestMongoRepo` 仍需 `MONGO_URL`。

### 1k bot 冒烟

```text
bots=1000 total=5001 ok=5001 err=0 err_rate=0.0000 p50=0us p99=780us
```

- 30s，HTTP API，world 与压测同机（i7-4980HQ），goakt v4.6.4。
- 另一次 60s：11001 请求、0 错误、p99 约 3ms（旧格式）。
- `p50=0us` 是因为本机时钟分辨率把亚微秒请求显示成 0，不是请求真的耗时 0；以 p99 为准。

### 限制

- HTTP 不是最终 WebSocket 网关；1k 是短时冒烟，不是 1h 验收。
- 无 Mongo 实例，交易持久化分支未做真实集成。
## 增量验证：C06 崩溃注入（2026-10-30）

### 单元测试

- `TestTruncatedNewestFallsBack`：最新快照截断到 10 字节，`LoadLatest` 回退上一代。
- `TestTmpFileIgnored`：只存在 `.tmp` 半写文件时返回 `ErrNoSnapshot`。
- `TestAllGenerationsCorrupt`：全部代际损坏时返回 `ErrCorrupt`。
- `TestBoundarySkipsSnapshotOffset`：快照 offset=2、WAL 记录 1/2/3，只回放 3。
- `TestCorruptRecordStops`：WAL 中间出现坏 JSON，恢复在坏记录处停止，不应用后续记录。

### 进程级快照损坏

```text
1. 启动 world（memory WAL）
2. grant 50  → 等 10s 快照（gold=1050）
3. grant 25  → 等 10s 快照（gold=1075）
4. kill -9
5. 把最新一代快照截断到 10 字节
6. 重启 world
7. state player=1
```

结果：`gold=1050`，world 自动回退到上一代快照并正常启动。

### 说明

- JetStream 的消息是整条投递的，不存在「半条 WAL 消息」；半写风险主要在快照文件层，已覆盖。
- 坏 WAL 记录会让恢复返回错误并停在最后一条完整记录，不会静默跳过。
## 1h soak（2026-10-30）

环境：1000 bot、0.2 次/秒、HTTP API、JetStream WAL、goakt v4.6.4、world 与压测同机（i7-4980HQ）。

```text
bots=1000 total=719001 ok=719001 err=0 err_rate=0.0000 p50=32us p99=1507us
start=2026-10-30T13:24:20+08:00
end  =2026-10-30T14:24:25+08:00
```

- world 进程 RSS 约 235 MB（52 分钟采样），bot 约 176 MB，未见持续增长。
- 最终快照约 7.25 MB，WAL offset 439,471（只记录玩家效果，不含 rank/top 查询）。
- 无错误、无连接泄漏迹象；快照每 10s 一次，WAL 未截断（已知限制）。

> 同机压测只用于稳定性验证，不代表容量结论。
## 增量验证：WebSocket 网关（2026-10-30）

### 实现

- `internal/gateway/ws.go`：`GET /ws` 升级，JSON 请求/响应，`Authorization: dev:<world>:<player>` 绑定玩家。
- 请求复用 `world.Request`（带 `seq`），响应 `{seq, ok, data, error}`。
- 读写超时 60s + pong 续期；消息上限 1 MB；玩家不匹配直接返回错误。
- `cmd/bot -ws` 使用持久 WS 连接；跨玩家交易仍走 HTTP（两个不同玩家的会话不能共用一个连接）。

### 测试

- `TestWSCommand`：合法 token + 请求返回 `ok`。
- `TestWSRejectsOtherPlayer`：token 绑定 player=7，发送 player=8 返回错误。

### 压测

```text
100 bot / 10s：1451 请求，0 错误，p99 351µs
1000 bot / 30s：5001 请求，0 错误，p99 532µs
```

- 1000 条持久 WS 连接，world 与压测同机（i7-4980HQ）。
- p50 显示 0us 仍是本机时钟分辨率问题，以 p99 为准。

### 限制

- 还没有服务端主动 push 与 `stateVersion` 断线补差；当前是请求-响应。
- 网关仍在 world 进程内，未拆成独立 `cmd/gateway`；P1 再拆。
## 增量验证：WS 推送与 stateVersion 续传（2026-10-30）

### 协议

- 客户端可发 `{"type":"subscribe"}`；之后每条命令的 response 后追加一条 `{"type":"push","stateVersion":N,"data":<PlayerState>}`。
- 连接建立后可发 `{"type":"resume","playerId":N}`；服务端返回 `{"type":"resume","ok":true,"stateVersion":N,"data":<PlayerState>}`。
- 新玩家 resume 返回 `stateVersion=0`、无 data，第一条命令时再创建。
- 玩家不匹配仍返回 `type:"error"`。

### 测试

- `TestWSPushAndResume`：subscribe → 命令 → response → push（stateVersion=3）；新连接 resume 得到同一版本与状态。
- `TestWSCommand` / `TestWSRejectsOtherPlayer` 保持通过。

### 压测

```text
100 bot / 10s：1451 请求，0 错误，p99 215µs
1000 bot / 30s：5001 请求，0 错误，p99 191µs
```

- bot 建连后先做 resume 握手，再进入请求循环；push 被客户端识别并跳过，不会污染请求响应。
- 1000 条持久连接，同机压测。

### 当前限制

- `resume` 目前总是返回完整状态（full sync），还没有基于 `stateVersion` 的增量补差；协议字段已预留。
- push 只在订阅连接上、命令之后触发；没有独立的状态变更广播总线。
## 1h WebSocket soak（2026-10-30）

```text
bots=1000 total=719001 ok=719001 err=0 err_rate=0.0000 p50=0us p99=1320us
15:16:41 → 16:16:45
```

- 1000 条持久 WS 连接，0.2 次/秒，JetStream WAL，同机压测。
- world RSS 约 245 MB（35 分钟采样），bot 约 171 MB；未见持续增长。
- 最终快照约 7.26 MB，WAL offset 439,580。
- 全程 0 错误、0 断连。

### 修掉的一个问题

第一次 WS soak 在 60s 后出现大量连接卡死：服务端只设置了一次 60s 读超时，没有在收到消息后续期。已在每条消息后重置 deadline，修复后先跑 1000 连接 / 90s（17,001 请求、0 错误），再跑本次 1h。

> p50 显示 0us 仍是本机时钟分辨率问题，以 p99 为准。
## 增量验证：独立 gateway 进程（2026-10-30）

### 拆分

- `cmd/world` 保留权威状态、HTTP API、WAL/快照；`cmd/gateway` 只做接入。
- `gateway.Remote` 把命令转发到 world 的 `POST /api/command`；`/ws` 的 resume/push 通过 `Remote.State` 取状态。
- `/api/command` 与 `/api/rank` 用 `httputil.ReverseProxy` 透传，HTTP 客户端也可走 gateway。
- gateway 无状态，WS 会话只存在于 gateway 进程。

### 测试

```text
world :18080 + gateway :18081
100 bot / 10s / -ws：1451 请求，0 错误，p99 2236µs
```

- gateway `healthz` 200；resume/push 经 Remote 正常工作。
- 单测 `TestRemoteHandleAndState` 覆盖转发与状态解码。

### 说明

- world 目前仍保留 `/ws`（向后兼容）；下一轮可以移除，强制所有客户端走 gateway。
- gateway 到 world 是一次 HTTP 往返；P1 可换 gRPC/长连接。
## 增量验证：world 移除 WS 入口（2026-10-30）

- `cmd/world` 不再注册 `/ws`，只保留 `/api/command`、`/api/rank`、`/healthz` 作为内部接口。
- 所有 WS 客户端必须走 `cmd/gateway`；HTTP 客户端可以直连 world，也可以走 gateway 的 ReverseProxy。
- 冒烟：`GET world:18080/ws` 返回 404；gateway:18081 跑 100 WS bot / 10s：

```text
100 bot：1451 请求，0 错误，p99 1256µs
```

- README 的快速开始已改为 world + gateway 两步，bot 默认走 gateway。
## 增量验证：自定义 TCP 长连接（2026-10-30）

### 协议

- 13 字节头：`[len:4][type:1][id:8]`，payload ≤ 4 MB；类型 request/reply/error/ping/pong。
- 一个 gateway 进程对每个 world 维护一条复用连接；请求按 `id` 关联响应，支持并发在途。
- 90s 读超时，ping/pong 保活；服务端 1024 并发请求信号量，满时返回 `server busy`。
- 客户端调用超时/断连会关闭并重建连接，pending 请求返回 `ErrClosed`。

### 接线

- `cmd/world -tcp :9000` 暴露内部 link；`cmd/gateway -world-tcp 127.0.0.1:9000` 使用 TCP，否则回退 HTTP。
- 单测：100 并发调用复用、错误帧、超时。

### 压测

```text
100 WS bot / 10s：1451 请求，0 错误，p99 1357µs
1000 WS bot / 30s：5001 请求，0 错误，p99 12173µs
```

- 1000 bot 时 p99 明显高于直连 WS（约 0.2ms），主要来自 gateway→world 单连接排队与交易多步调用；0 错误。
- 单连接在 200 rps 下仍有余量，但 p99 需要靠连接池/批量/优先级优化。
## 增量验证：TCP 连接池与交易走 TCP（2026-10-30）

### 实现

- `gateway.TCPRemote` 改为 N 条 link 的池（`-tcp-pool`，默认 4），请求按轮询选择连接；单条连接失败只重建该槽。
- bot 的交易改为按玩家各自的 transport 执行：A create → B accept → A settle，全部走各自的 WS 会话，不再走 gateway 的 HTTP 代理。
- 新增 `tradePeers` 注册表；peer 尚未建连时跳过交易，不记为错误。

### 压测对比（1000 WS bot / 30s）

| 配置 | 请求 | 错误 | p99 |
|---|---|---|---|
| 单 link + 交易走 HTTP（上一版） | 5001 | 0 | 12.17ms |
| 单 link + 交易走 WS | 5001 | 0 | 1.153ms |
| 4 条 link + 交易走 WS | 5001 | 0 | 1.129ms |

- 100 bot / 10s / 4 条 link：1451 请求，0 错误，p99 984µs。
- 结论：上一版 12ms 的尾延迟主要来自交易多步调用走 HTTP 代理；在 200 rps 下连接池不是瓶颈，但保留池化以便更高 RPS。

### 说明

- 连接池目前按请求轮询，未按 playerID 哈希；同一玩家的命令可能落到不同 link，world 侧仍按 actor 串行，语义正确。
- 交易现在要求双方都有活跃 WS 会话，符合真实客户端模型。
## 增量验证：动态 TCP 池 + playerID 哈希（2026-10-30）

### 实现

- `TCPRemote.SetSize(n)` 支持运行时扩缩；扩容追加槽位，缩容关闭被移除槽位的连接。
- `GET/POST /admin/pool?size=N` 暴露当前池大小与调整入口。
- 槽位选择改为 `playerID * 2654435761 % poolSize`，同一玩家固定落在一个槽位。
- 连接池仍保留每槽独立重连与断链隔离。

### 压测

```text
1000 WS / 30s，pool 2→8（第 10s 扩容）：5001 请求，0 错误，p99 1000µs
1h WS soak，pool 4→8（第 30m 扩容）：719001 请求，0 错误，p50 111µs，p99 1218µs
```

- 扩容时 gateway 连接数从 1004 升到 1010（1000 WS + 8 TCP + 健康检查），无错误、无断连。
- 最终快照 7.25 MB，WAL offset 439,336。
- 1h 期间 bot / gateway / world RSS 稳定（约 182 / 173 / 205 MB）。

### 说明

- 缩容在 P0 只用于演练；生产缩容应先排空目标槽位的在途请求，再关闭连接。
- 哈希在池大小变化时会让部分玩家换槽，但 world 侧按 actor 串行，语义不受影响。
## 增量验证：MongoDB 真实集成（2026-10-30）

### 环境

- 本机无 MongoDB，下载便携版 `mongodb-windows-x86_64-7.0.14` 到工作区缓存，`mongod --dbpath ... --port 27017` 启动。
- 未写入仓库；CI 用 `mongo:7` service container 复现。

### 单测

```text
MONGO_URL=mongodb://127.0.0.1:27017 go test ./internal/trade -run TestMongo -v
TestMongoRepoPersistsAcrossReconnect  PASS
TestMongoRepo                          PASS
```

- 新增 `TestMongoRepoPersistsAcrossReconnect`：创建 → 状态推进 → 关闭 repo → 重新连接 → 读回 `both_locked` / version 2。
- CI 新增 `integration` job，带 `mongo:7` 服务和健康检查，运行 `TestMongo*`。

### world 重启集成

```text
world（Mongo trade store）: create → accept → settle
trade_get before restart: state=settled version=4
kill -Force + 重启 world
trade_get after restart:  state=settled version=4
```

- 交易文档真实落到 MongoDB，进程重启后仍可读回。
- 本次用 memory WAL，玩家状态重启后会重建；交易文档持久化是独立验证的。生产应与 JetStream WAL/快照组合使用。
## 增量验证：Mongo 索引/TTL 与副本集事务（2026-10-30）

### 索引

- `state_expireAt`：`{state:1, expireAt:1}`，服务 `ListPending` 与对账查询。
- `ttl_expireDate`：`{expireDate:1}`，`expireAfterSeconds=0`；`Repo.Create` 写入 `expireDate = expireAt + 30d`，TTL 在逻辑过期 30 天后清理。
- 旧的单字段 `state_1` 索引在启动时尽力删除。
- `TestMongoIndexes` 断言两个索引存在且 TTL 的 `expireAfterSeconds=0`。

### 副本集

- 本机 `mongod --replSet rs0 --port 27018`，测试通过 admin 命令自动 `replSetInitiate`。
- `TestMongoReplicaSetTransaction` 在 `WithTransaction` 内双写两条文档，提交后 count=2，PASS。
- CI 新增 `replica` job：`mongo:7 --replSet rs0`，等端口后跑同一测试。

```text
TestMongoIndexes                     PASS
TestMongoRepoPersistsAcrossReconnect PASS
TestMongoReplicaSetTransaction       PASS
```

### 说明

- 目前业务代码没有使用跨文档事务；本测试验证的是驱动/集群具备事务能力。交易状态机仍以单文档 CAS（`version + state`）为准。
- TTL 是冷数据清理，不替代对账；过期未结算的交易仍由 `ListPending` + 协调器处理。
## 增量验证：对账 worker / 战斗 / 赛季重置（2026-10-30）

### 对账

- `world.Service.Reconcile` 委托 `trade.Service.Reconcile`，`cmd/world` 每 30s 跑一次，`touched>0` 时打日志。
- API 增加 `{"action":"reconcile","k":100}` 便于手工触发。
- 复用已有 `TestReconcileExpired` / `TestReconcileResumesSettling`。

### 战斗

- `internal/battle`：`Resolve` 高战力胜、平局防守方胜；`Submit` 直接落到 `settled`。
- API：`battle_submit`（power/defPower）、`battle_get`；P0 用内存存储，重启丢失。

```text
battle_submit → winner=1, state=settled
battle_get    → 同一条记录
```

### 赛季重置

- `PlayerActor.resetSeason` 清空 power/资源/城市/行军/队列，保留 paid 资产不在 P0 模型内。
- 有 active escrow 时返回 `ErrActiveEscrow`，不强制重置。
- `Service.SweepSeason` 懒重置所有过期玩家；`cmd/world` 启动时 sweep 一次，之后每 24h sweep。
- 冒烟：两名玩家 season 1 → `season_reset` 到 2 → `reset=2`，玩家资源回到 1000/1000/1000，power=0。
- 单测 `TestResetSeason` 覆盖 escrow 保护与字段清理。
## 增量验证：3-shard MongoDB（2026-10-30）

### 拓扑

```text
config server :27100  --configsvr --replSet cfg
shard1        :27101  --shardsvr  --replSet shard1
shard2        :27102  --shardsvr  --replSet shard2
shard3        :27103  --shardsvr  --replSet shard3
mongos        :27200  --configdb cfg/127.0.0.1:27100
```

- `tools/shard-setup` 通过 admin 命令自动 `replSetInitiate` 四个副本集。
- `TestMongoSharded`（`MONGO_SHARDED_URL=mongodb://127.0.0.1:27200`）：
  - `addShard` 三个 shard；
  - `enableSharding` + `shardCollection trades {_id:"hashed"}`；
  - `OpenMongo` 在分片集合上建 `state_expireAt` 与 TTL 索引成功；
  - create / transition / get / ListPending 通过；
  - 写入 100 条后查询 `config.chunks`（按 collection UUID），chunks=6，覆盖 shard1/shard2/shard3。

```text
chunks=6 shards=map[shard1:true shard2:true shard3:true]
PASS
```

### 说明

- 本地用便携版 `mongod/mongos` 起集群，脚本未进仓库；`tools/shard-setup` 可复用。
- 分片键 `_id: hashed` 适合按交易 ID 点查；如果后续按 `world + state` 扫对账，需要再评估分片键或加二级路由字段。
- CI 暂未加 3-shard job（需要 7 个容器），本地已完整验证。
## 增量验证：战斗持久化与对账指标（2026-10-30）

### 战斗持久化

- `battle.OpenMongo` 创建 `battles` 集合，索引：
  - `world_season_created`：`{world:1, season:1, createdAt:-1}`
  - `attacker_created` / `defender_created`
- `world.Service.UseBattleStore` 在 `storage.mongoUrl` 非空时切换到 Mongo store。
- 修了一个真实 bug：`Battle.ID` 只有 `json:"_id"`，没有 `bson:"_id"`，Mongo 自动生成了 ObjectID，按 ID 查不到；补上 bson tag 后测试通过。
- `TestMongoBattlePersists`：提交 → 关闭 store → 重开 → 读回 winner/power/state。
- world 冒烟：`battle_submit` → kill/重启 → `battle_get` 仍返回同一 `battleId`、winner=1、state=settled。
- CI 的 Mongo integration job 现在同时跑 `./internal/battle` 与 `./internal/trade`。

### 对账指标

`/metrics` JSON 现在包含：

```json
{"commands_total":4,"errors_total":0,"rank_lookup_total":1,
 "reconcile_runs_total":1,"reconcile_touched_total":0,
 "reconcile_errors_total":0,"reconcile_last_touched":0,
 "trade_ok_total":0,"trade_fail_total":0}
```

- `Reconcile` 每次累加 runs；成功累加 touched 并更新 last_touched gauge；失败累加 errors。
- `Handle` 统一累加 commands/errors；rank/top 累加 rank_lookup；交易动作累加 trade_ok/fail。
- `/metrics` 由 world 进程直接输出，P1 再接 Prometheus。
## 增量验证：战斗历史 / 对账错误 / 赛季奖励（2026-10-30）

### 战斗历史

- `battle_history`（`playerId`, `season`, `k`）：按 attacker/defender 过滤，`createdAt` 倒序，limit 默认 20。
- Memory 与 Mongo store 都实现 `ListByPlayer`；Mongo 索引改为 `attacker_season_created`、`defender_season_created`。
- 冒烟：同一玩家 2 场战斗，`season=1` 返回 2 条，最新在前。
- `TestHistory` 覆盖全量/按赛季过滤；`TestMongoBattlePersists` 增加 Mongo 历史查询。

### 对账错误明细

- `trade.Service.ReconcileDetailed` 返回每条失败交易的 `id + 动作 + 错误`；原 `Reconcile` 保持 `(touched,error)`。
- `world.Service.ReconcileErrors()` 保留最近 20 条；`GET /api/reconcile/errors` 返回。
- 指标新增 `reconcile_last_error_count`，`reconcile_errors_total` 按失败交易累加。
- 单测 `TestReconcileDetailedErrors`：强制 transition 失败，`Touched=0`、`Errors=1`。

### 赛季奖励

- `SettleSeason` 在重置前按最终排名计算奖励：1 名 5000、2–10 名 2000、11–100 名 1000、101–1000 名 500、其余 100（仅 power>0）。
- 先重置、后发奖；有 escrow 的玩家跳过且不发奖。
- `season_reset` 现在返回 `{reset,rewarded,skipped}`。
- 冒烟：两名玩家 → `reset=2, rewarded=2`；排名 1 的玩家重置后 gold=1000+5000=6000。

```json
{"season":2,"reset":2,"rewarded":2,"skipped":0}
{"gold":6000,"wood":1000,"food":1000,"power":0}
```
## 增量验证：历史分页 / 奖励配置 / 对账告警（2026-10-30）

### 战斗历史分页

- 游标改为 `(createdAt, _id)` 复合键：`before`、`beforeId`。
- Memory 与 Mongo 都按 `createdAt desc, _id desc` 稳定排序；Mongo 查询用 `$or` 处理同一毫秒的边界。
- 返回 `{items, nextCreatedAt, nextId}`；只有取满 limit 时才给下一页游标。
- `TestHistoryPagination`：3 场战斗分两页（2+1），无重复/遗漏。

### 赛季奖励配置

- `config.Season.Rewards`：`{minRank, maxRank, gold}`，`maxRank=0` 表示无上限。
- `world.RewardTier` + `SetRewards`；`cmd/world` 启动时从配置注入，未配置则用默认表。
- `configs/default.json` 已带默认表。
- `TestRewardForRank` 覆盖自定义区间与无上限区间。

### 对账告警

- `world.Service.SetAlertHook`：一次对账出现失败时回调完整错误列表。
- 新增 `reconcile_alerts_total`，每次有失败的对账轮次 +1；`reconcile_last_error_count` 保留最近一轮失败数。
- `cmd/world` 注册 hook，输出 `ALERT reconcile failures=N first=...`。
- 说明：告警是进程内 hook，P1 接 Prometheus Alertmanager/日志采集。
## 增量验证：RF=3 快照与 WAL 截断（2026-10-30）

### RF=3 快照

- `snapshot.ReplicaSet`：对 N 个本地目录写入，quorum 默认 `N/2+1`；读取时选 `WALOffset` 最大的有效副本。
- 单副本损坏时自动回退其他副本；副本全坏返回 `ErrCorrupt`，全空返回 `ErrNoSnapshot`。
- `storage.snapshotDirs` + `storage.snapshotQuorum` 配置；未配置时回退单目录。
- 测试：
  - `TestReplicaSetSaveAndLoad`：3 副本写入、最高 offset 优先、损坏后回退。
  - `TestReplicaSetQuorum`：只剩 1 个可写副本时 Save 返回 quorum 错误。

### 快照 offset 一致性

- 新增 `Service.SnapshotWithOffset`：用 `mutMu` 屏障阻塞所有变更动作，读取状态与 `wal.LastOffset()` 后一起返回，避免「快照状态旧、offset 新」导致截断丢记录。
- `Handle` 对变更动作持读锁；`Reconcile` 自持读锁；快照持写锁。
- `saveSnapshot`：保存成功后才 `Truncate(offset+1)`，失败不截断。

### WAL 截断

- Memory WAL 修复了截断后 offset 归零的 bug：现在保留绝对 offset，`Truncate(2)` 后 `LastOffset=2`，追加返回 3。
- JetStream 截断改为从上次已删除位置增量删除，避免每次 O(n) 扫描。
- `TestRecoverAfterTruncate`：快照 offset=2、截断后只回放 3/4。
- `TestJetStreamRoundTrip` 增加截断后的 offset 断言（2、3，LastOffset 仍为 3）。

### 冒烟

```text
3 个 snapshotDirs，每目录 1 个快照
grant 50 → 10s 快照 → kill -9 → 重启
state.player1.gold = 1050
```

JetStream WAL + RF=3 组合同样通过。

### 边界

- 3 个目录目前在同一块磁盘，属于**逻辑 RF=3**；真实 RF=3 需要 3 块独立 NVMe/节点，P1 再接远端副本或对象存储。
- 本地 `-race` 因 cgo 未启用跑不了；CI 的 race job 继续覆盖。
## 增量验证：跨进程 peer RF=3（2026-10-30）

### 实现

- `snapshot.ReplicaSet` 现在同时支持本地目录与远端 peer：
  - `storage.snapshotPeers`：peer 的 HTTP base URL；
  - `storage.replicaToken`：内部复制 token；
  - `POST/GET /internal/snapshot`：peer 写入/读取端点。
- 写入 quorum 覆盖本地 + peer；读取选 `WALOffset` 最大的有效副本。
- 被动副本用 `storage.disableSnapshot: true`，只接收 peer 快照，不主动生成空快照。
- 修了两个多写者问题：
  1. `Store.LoadLatest` 之前按文件时间选最新，peer 写入的旧 offset 可能盖住高 offset；现在按 `WALOffset` 选最高。
  2. peer 暂时不可达时不再被当成「快照损坏」，world 可以先于 peer 启动。

### 三进程故障验证

```text
A :18100  snapshotDir=a  peers=B,C
B :18101  snapshotDir=b  peers=A,C  disableSnapshot=true
C :18102  snapshotDir=c  peers=A,B  disableSnapshot=true

A grant 50 → 10s 快照
replica_a=1 replica_b=1 replica_c=1
kill A + 删除 a 目录
重启 A（本地为空）
state.player1.gold = 1050
```

- A 从 B/C 的 peer 副本恢复，证明快照不是只写本地。
- 单测 `TestPeerReplication`、`TestPeerAuth`、`TestPeerQuorumFailure`、`TestStorePicksHighestOffset` 覆盖复制、鉴权、quorum 与最高 offset。

### 边界

- 三个 world 在同一台机器上、不同进程/目录；真实跨节点需要把 `snapshotPeers` 指向其他节点，并加 TLS/内网 ACL。
- **WAL 还没有跨节点复制**：owner 在两次快照之间的 WAL 仍只在本机 JetStream；要做到完整 R3，需要 JetStream 3 节点集群或 WAL peer 复制。
## 增量验证：R3 JetStream WAL（2026-10-30）

### 集群

```text
n1 :4221  cluster :6221
n2 :4222  cluster :6222
n3 :4223  cluster :6223
JetStream store_dir 各自独立
```

- 三个 `nats-server` 组成 `hape` route 集群。
- `wal.JetStreamConfig.Replicas=3`，stream 创建时 `Replicas:3`；`storage.walReplicas` 配置。
- `TestJetStreamRoundTrip` 增加 `StreamInfo.Config.Replicas` 断言。

### 单节点故障

```text
kill n3
stream R3 仍有 2/3 副本
world（disableSnapshot=true，纯 WAL）grant 50
kill -9 world + 重启
state.player1.gold = 1050
```

- 纯 WAL 恢复成功，证明一条 NATS 节点下线时 R3 stream 仍可写、可回放。
- 同时配合前面的 snapshot peer RF=3：快照跨进程 3 副本，WAL 跨节点 3 副本。

### 边界

- 三个 NATS 节点仍在同一台机器；生产需 3 台/3 AZ，并配置 TLS 与鉴权。
- R3 只能容忍 1 个节点故障；2 个节点丢失时 stream 不可写。
- 本地验证未把 NATS 集群配置纳入仓库（用临时 conf），CI 暂未加 3 节点 NATS job。
## 增量验证：Prometheus 导出与 NATS R3 模板（2026-10-30）

### Prometheus

- `Metrics.Prometheus()` 输出 text exposition：`_total` 后缀为 counter，其余为 gauge，按名称排序。
- `cmd/world` 新增 `GET /metrics/prometheus`；原 JSON `/metrics` 保留。
- 示例：

```text
# TYPE commands_total counter
commands_total 4
# TYPE reconcile_last_touched gauge
reconcile_last_touched 0
# TYPE reconcile_runs_total counter
reconcile_runs_total 1
```

- 单测 `TestPrometheus` 检查 counter/gauge 类型与值。

### NATS R3 模板

- `deploy/nats/docker-compose.yml`：3 个 `nats:2.10` 节点，独立数据卷，cluster name `hape`，端口 4221–4223。
- `deploy/nats/README.md`：本地启动、R3 测试命令、K8s StatefulSet 的 routes 写法。
- CI 新增 `wal-r3` job：compose 起 3 节点 → 等端口 → `NATS_REPLICAS=3` 跑 `TestJetStreamRoundTrip`。

### 说明

- 本机没有 Docker，compose 与 CI job 未在本机执行；R3 行为已用本地 3 个 `nats-server` 进程验证（见上一节）。
- Prometheus 端点未做鉴权，生产应放内网或加 basic auth/ingress 限制。
## 增量验证：玩家接入统一为原生 TCP（2026-10-30）

### 协议

- 玩家连接使用 `internal/link` 的帧协议：`[len:4][type:1][id:8][payload]`，请求/响应按 `id` 关联。
- 请求 JSON：`{token, request:{action, playerId, ...}}`；响应 JSON：`{ok, data, error}`。
- token 仍是 dev token `dev:<world>:<player>`；每个请求携带，P1 换会话级 JWT。
- gateway 只监听 TCP；HTTP `:8081` 仅保留 `/healthz` 与 `/admin/pool`，不再有 `/ws`、`/api/command` 代理。

### 代码变化

- 删除 `internal/gateway/ws.go`、`ws_test.go`、`remote.go`、`remote_test.go`。
- 新增 `internal/gateway/player_tcp.go`、`player_tcp_test.go`。
- `cmd/bot` 改为 `link.Dial` + `Call`，移除 HTTP/WebSocket 传输与 `-ws`。
- `cmd/gateway` 默认 `-addr :9001 -admin :8081 -world-tcp 127.0.0.1:9000`。
- world 的 HTTP 只保留指标/健康检查等内部接口。

### 测试与压测

```text
TestPlayerTCPCommand / TestPlayerTCPAuth / TestPlayerTCPMismatch  PASS
100 bot / 10s：1451 请求，0 错误，p99 999µs
1000 bot / 30s：5001 请求，0 错误，p99 1170µs
```

### 当前限制

- 原生 TCP 还没有服务端主动 push 与增量 resume；客户端重连后需重新查询 state。
- token 每请求携带，增加了少量字节与鉴权开销。
- 玩家连接没有应用层心跳；服务端 90s 读超时会断开完全空闲的连接。