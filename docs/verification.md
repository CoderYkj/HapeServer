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