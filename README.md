# nacos-config-sync

在主机上常驻运行的轻量工具：从 **Nacos 配置中心** 拉取指定配置，订阅变更，并将内容**原子写入**本地目录中的文件（文件名与 `dataId` 一致）。适用于把集中配置同步到本机路径，供应用或脚本读取。

## 功能概览

程序默认读取当前工作目录下的 `nacos.ini`，并根据其中的 `hostId` 到 Nacos 拉取 **`{hostId}.ini`**（例如 `saas-dev-app01.ini`）。按 `Ctrl+C` 发送退出信号后，会执行 `Stop()` 收尾逻辑。

- 启动时读取 `nacos.ini`，然后从 Nacos 读取 `{hostId}.ini`。
- 对 `{hostId}.ini` 中，除 `common` 外每个 `dataId` 做一次**初始拉取**，并 **Listen** 后续变更。
- `common` 为通用配置，会同时写入所有非 `common` 模块的目录（如 `/data/app/sso/config`、`/data/app/ggw/config`）。
- 若 `common` 与模块段在同一目录下存在同名 `dataId`，以模块段配置为准（`common` 不覆盖模块同名文件）。
- 如果 `path` 不存在，自动创建目录。
- 写入采用「临时文件 + `rename`」，降低写到一半被读到的风险。
- 落盘前会读取目标文件：若**内容与 Nacos 一致**则**跳过写入**，避免无意义地刷新文件修改时间（`mtime`）。
- 日志为单行 JSON（`logger` 包），便于采集。

## 环境要求

- Go **1.19+**
- 可访问的 Nacos Server（默认配置端口 **8848**）

## 快速开始

```bash
go build -o nacos-sync2 .
./nacos-sync2
```

Windows:

```powershell
go build -o nacos-sync2.exe .
.\nacos-sync2.exe
```

## 配置文件

### 1. `nacos.ini` — 连接 Nacos

示例：

```ini
[nacos]
ipAddr = 192.168.1.10
port = 8848
namespaceId = hosts
username = nacos
password = nacos
group = hosts
hostId = saas-dev-app01
logDir = /data/nacos-config-sync/logs/
cacheDir = /data/nacos-config-sync/logs/cache/
logLevel = info
; 可选：拉长 Nacos SDK 对服务端的 gRPC 应用层 HealthCheck 间隔（秒），见下文「SDK 与依赖」
; rpcKeepAliveSeconds = 30
```

字段说明（与 `config.NacosConfig` 对应）：

| 键 | 必填 | 说明 |
| --- | --- | --- |
| `ipAddr` | 是 | Nacos 服务器地址 |
| `port` | 是 | 端口（整数） |
| `namespaceId` | 是 | 命名空间 ID |
| `username` / `password` | 否 | 开启鉴权时使用，会传给 SDK |
| `group` | 是 | 用于拉取 `{hostId}.ini` 的分组 |
| `hostId` | 是 | 主机标识，用于拼接主机配置名 `{hostId}.ini` |
| `logDir` | 否 | Nacos SDK 日志目录，不填则走系统临时目录 |
| `cacheDir` | 否 | Nacos SDK 缓存目录，不填则走系统临时目录 |
| `logLevel` | 否 | SDK 日志级别，建议 `info`/`warn`；设为 `debug` 时会打印大量 gRPC 请求日志 |
| `rpcKeepAliveSeconds` | 否 | 大于 0 时写入环境变量 `NACOS_SDK_RPC_KEEP_ALIVE_SECONDS`（秒），拉长 SDK 空闲时的 gRPC `HealthCheckRequest` 间隔；不写或 `0` 不改环境变量（未设 env 时默认 **5s**）。详见「SDK 与依赖」。 |

### 2. `{hostId}.ini` — 本机要同步哪些配置

示例（`saas-dev-app01.ini`）：

```ini
[common]
namespaceId = saas 
group = common
dataId = core-common-cache.properties,core-common-mq-config.xml,core-common-redis.properties,dubbo.properties,logback.xml

[project01]
group = project01
dataId = db-project01.properties,nzp-project01-bss-server.properties,nzp-core-project01-server.properties,yunsdk.properties
path = /data/app/project01/config

[project02]
group = project02
dataId = ddb-project02.properties,nzp-project02-server.properties,nzp-ems-project02-server.properties
path = /data/app/project02/config
```

说明：

- **`[common]`**：公共配置段，字段包括 `namespaceId`、`group`、`dataId`；会分发到所有业务模块目录。
- **其它段名**：视为模块名，字段为 `group`、`dataId`、`path`；`namespaceId` 可省略（默认继承 `common.namespaceId`）。
- **`path`**：本地目录，同步后的文件路径为 `{path}/{dataId}`。
- **`dataId`**：支持英文逗号分隔多个 ID，如 `app.properties,bootstrap.yml`。
- 为避免混用命名空间，模块若显式配置 `namespaceId`，其值必须与 `common.namespaceId` 一致。

## 项目结构

```text
.
├── main.go
├── config/
│   └── config.go
├── syncer/
│   └── syncer.go
├── atomicfile/
│   └── atomicfile.go
├── logger/
│   └── logger.go
├── third_party/
│   └── nacos-sdk-go-v2/    # 基于官方 v2.2.7 的本地替换，见「SDK 与依赖」
├── go.mod
└── README.md
```

## 运行行为

- 启动后先拉取一次所有目标配置并写盘，再注册监听。
- 程序会监听 `{hostId}.ini` 本身的变更；新增/删除 `dataId` 或调整 `path` 后可自动生效，无需重启进程。
- 同一个 `(namespaceId, group, dataId)` 仅注册一次监听，避免重复订阅。
- 配置变更后，按映射关系分发写入对应目录。
- 当某模块移除某个 `dataId`（或该 `dataId` 不再映射到某目录）时，程序会删除本地对应文件。
- 收到退出信号时，先取消监听，再关闭 Nacos 客户端连接。
- 单个 `dataId` 拉取/监听失败不会导致进程退出，程序会继续处理其它配置并在日志里输出失败项。

## 注意事项

- 如未配置 `logDir` / `cacheDir`，程序会自动使用系统临时目录（Windows 与 Linux 都可用）。
- Linux 生产环境建议显式配置 `logDir`/`cacheDir` 到持久目录（如 `/var/log/...`、`/var/cache/...`），并提前创建目录与授权。
- 请确保目标 `path` 目录可写；目录不存在时程序会尝试自动创建。
- 未在 Nacos 中创建对应 `dataId` 时，`GetConfig` 可能失败，请先在控制台或通过 API 发布配置。

## SDK 与依赖

- `go.mod` 使用 `replace`，将 `github.com/nacos-group/nacos-sdk-go/v2 v2.2.7` 指向本仓库下的 **`third_party/nacos-sdk-go-v2`**（官方 **v2.2.7** 源码加极小补丁）。
- 补丁目的：官方常量 `KEEP_ALIVE_TIME` 固定为 **5 秒**，无法在配置中调整；补丁使 SDK 在空闲时发送 **`HealthCheckRequest`** 的周期可读环境变量 **`NACOS_SDK_RPC_KEEP_ALIVE_SECONDS`**（正整数，单位秒）。`nacos.ini` 中的 **`rpcKeepAliveSeconds`** 会在启动客户端前写入该环境变量（仅当值大于 0）。
- 若日志中出现大量 `grpc request nacos server success, request=... HealthCheckRequest ...`，多为 **`logLevel=debug`** 所致；不需要排查问题时可将 `logLevel` 设为 **`info`**。
- 另：SDK 内 gRPC 传输层 keepalive 与上述应用层健康检查不同，可通过环境变量 **`nacos.remote.grpc.keep.alive.millis`** 调整（与 `rpcKeepAliveSeconds` 无关）。

## 最小可运行示例

1) 在程序运行目录创建 `nacos.ini`：

```ini
[nacos]
ipAddr = 192.168.1.10
port = 8848
namespaceId = hosts
username = nacos
password = nacos
group = hosts
hostId = saas-dev-app01
logLevel = info
```

2) 在 Nacos 的 `group=hosts` 下发布配置：

- `dataId = saas-dev-app01.ini`
- `content` 示例：

```ini
[common]
namespaceId = saas
group = common
dataId = dubbo.properties,logback.xml

[project01]
group = project01
dataId = db-project01.properties,db-rbac.properties
path = /data/app/project01/config

[project02]
group = project02
dataId = db-project02.properties,db-erc.properties
path = /data/app/project02/config
```

3) 启动程序：

```bash
go build -o nacos-sync2 .
./nacos-sync2
```

Windows:

```powershell
go build -o nacos-sync2.exe .
.\nacos-sync2.exe
```

启动后会先做一次初始拉取，再持续监听变更；`common` 配置会同时写入各业务模块的 `path` 目录。

## 常见故障排查

- `GetConfig` 失败（启动即退出）
  - 检查 `nacos.ini` 的 `ipAddr`、`port`、`namespaceId`、`group` 是否正确。
  - 确认 Nacos 中已发布 `dataId={hostId}.ini`，且分组与 `nacos.group` 一致。
  - 若开启鉴权，确认 `username`、`password` 正确。

- 业务配置未落盘
  - 检查 `{hostId}.ini` 的 `group`、`dataId` 是否在对应 namespace/group 下真实存在。
  - 检查 `path` 写权限（Linux 建议 `ls -ld` 目标目录并确认运行用户可写）。
  - 查看标准输出 JSON 日志中的 `error` 字段定位失败原因。

- `common` 配置未分发到所有模块目录
  - 确认 `{hostId}.ini` 存在非 `common` 模块段，并正确配置了各模块 `path`。
  - 确认 `common` 段存在且 `dataId` 不为空。
  - 若模块显式配置了 `namespaceId`，必须与 `common.namespaceId` 一致。

- Nacos 有变更但本地文件没更新
  - 确认变更的是同一个 `(namespaceId, group, dataId)`。
  - 检查程序是否仍在运行，且未出现持续错误日志。
  - 检查是否有其他进程覆盖了目标文件内容。

- Windows 下运行正常但 Linux 失败
  - 优先检查目录权限和 SELinux/AppArmor 策略。
  - 建议在 Linux 显式配置 `logDir`、`cacheDir` 到持久目录，并提前创建与授权。

## Windows 编译 Linux 可执行文件

你这次出现的：

```bash
-bash: ./nacos-sync2: cannot execute binary file
```

通常是因为把 **Windows 目标文件**（PE 格式，如 `.exe`）上传到了 Linux，Linux 无法直接执行。

在 Windows 下可使用 Go 交叉编译生成 Linux 二进制：

### PowerShell（推荐）

```powershell
$env:CGO_ENABLED="0"
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -o nacos-sync2 .
```

若目标机器是 ARM64（如部分云主机）：

```powershell
$env:CGO_ENABLED="0"
$env:GOOS="linux"
$env:GOARCH="arm64"
go build -o nacos-sync2 .
```

### CMD

```cmd
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -o nacos-sync2 .
```

编译后上传到 Linux：

```bash
chmod +x nacos-sync2
./nacos-sync2
```

可在 Linux 自检文件类型：

```bash
file nacos-sync2
```

期望看到类似：

- `ELF 64-bit LSB executable, x86-64`（amd64）
- `ELF 64-bit LSB executable, ARM aarch64`（arm64）

如果看到 `PE32` / `PE32+`，说明还是 Windows 可执行文件，需要重新按上述方式交叉编译。


