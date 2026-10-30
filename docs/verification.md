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