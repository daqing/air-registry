# github.com/daqing/air-registry

一个基于 [Airway](https://github.com/daqing/airway) 的应用：一个 OCI 兼容的容器
镜像 registry,提供网页端,可搜索、浏览已存储的镜像。英文版 README 见
[README.md](README.md),两份内容一一对应。

## 目标与需求

Air Registry 是一个 OCI 兼容的容器镜像 registry——概念上与
[Zot](https://zotregistry.dev/) 类似,但基于
[Airway](https://github.com/daqing/airway) 框架从零实现(不使用任何 Zot
源代码),并提供用于搜索、浏览镜像的网页端。

### 功能需求

- **OCI Distribution Spec 兼容**:实现标准 `/v2/` API,常见 OCI 客户端
  (docker、crane、containerd 等)无需任何客户端改动即可推拉镜像:
  - 核心 push/pull:blob 上传(整体与分块)、blob 下载、manifest PUT/GET、
    tag 列表
  - 删除接口:manifest 与 blob 的 `DELETE`,以及相应的垃圾回收
  - 内容发现:`/v2/_catalog` 仓库枚举 API
  - OCI 1.1 Referrers API:通过 `subject` 关联查询镜像的附属制品(签名、
    SBOM 等)
- **存储**:blob 以内容寻址文件形式存储在磁盘(`data/storage/` 下);仓库、
  tag、digest、referrer 等元数据通过 Airway model 索引进数据库
- **网页端**:服务端渲染的 Web UI,支持按名称搜索镜像、分页浏览仓库、
  查看镜像详情(tag 列表、manifest 层、大小、digest)
- **认证**:MVP 阶段匿名读写;架构上需预留后续接入 HTTP basic auth 的空间

### 约束

- 直接依据 OCI Distribution Spec 实现——不使用 Zot 源代码,也不依赖 Zot
- 基于 Airway 框架(Go 服务端渲染视图 + `templ`、model、migration)
- 必须通过标准客户端的互操作性验证(`docker push/pull`、`crane` 等)

## 快速开始

脚手架已自动生成 `.env`——打开它，设置 `AIRWAY_ENV`（如 `local`）、`DSN` 和
`LISTEN` 监听地址（`host:port`，如 `:1900`）：

```bash
airway db:create
airway db:migrate
go run .               # 启动 HTTP 服务（等价：airway server）
```

## 常用命令

```bash
airway generate api admin          # 生成一个 API 命名空间
airway generate model post         # 生成一个模型
airway generate migration create_posts
airway db:migrate
go run . repl                      # 带本项目模型的 REPL
```

## 桌面应用（macOS / Windows / Linux）

本项目可以打包为原生桌面应用：同一套 Web 技术栈运行在桌面进程内的本地端口
上，原生 WebView 窗口直接加载它——服务端渲染、cookie 会话、重定向、
WebSocket 的行为与线上完全一致，应用代码零改造。

### 1. 生成桌面目标

```bash
airway desktop:init
```

该命令会创建 `desktop/` 目录，即完整的 Wails v3 工程：窗口引导代码、内嵌的
`db/migrate` SQL（首次启动自动迁移）、插件镜像，以及三个平台的构建资产；
同时会把 `github.com/wailsapp/wails/v3` 固定到 `go.mod`。重复执行是安全的：
它只重新同步迁移与插件，不会改动 `desktop/main.go`（需要重新生成时加
`--force`）。

### 2. 安装桌面工具链（一次性）

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.24
go install github.com/go-task/task/v3/cmd/task@latest
```

各平台构建要求：

| 平台 | 要求 |
|---|---|
| macOS 12+ | Xcode 命令行工具（`xcode-select --install`） |
| Windows 10/11 | 构建无需额外依赖；安装器自带 WebView2 引导程序 |
| Linux | GTK4 + WebKitGTK 6.0 开发包（Ubuntu 24.04+ / Debian 13+），例如 `sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev` |

### 3. 开发运行

```bash
cd desktop
wails3 task dev
```

### 4. 构建安装包

所有产物输出到 `desktop/bin/`。

#### macOS（.app）——需在 macOS 上构建

```bash
cd desktop
wails3 task package                    # 当前架构的 .app
wails3 task darwin:package:universal   # universal .app（Apple Silicon + Intel）
```

`.app` 默认 ad-hoc 签名，本机使用足够。要分发给他人，需用 Developer ID
证书签名并公证（先用 `wails3 setup` 完成一次性配置）：

```bash
wails3 task darwin:sign:notarize
```

#### Windows（.exe + NSIS 安装器）——需在 Windows 或 CI 上构建

```bash
cd desktop
wails3 task package
```

产出 `bin/<app>.exe` 以及一个 NSIS 安装器；目标机器缺 WebView2 运行时时，
安装器会自动安装。构建安装器需要安装
[NSIS](https://nsis.sourceforge.io)。正式发布请用 Authenticode 证书签名
安装器：

```bash
wails3 task windows:sign:installer
```

#### Linux（deb / rpm / AppImage）——需在 Linux 上构建

```bash
cd desktop
wails3 task package
```

构建二进制，并通过 nfpm 产出 deb、rpm 包和 AppImage（AppImage 步骤首次运行
会下载 `linuxdeploy` 工具）。deb/rpm 声明 GTK4 + WebKitGTK 6.0 依赖；老发行
版用旧栈构建（`EXTRA_TAGS=gtk3`），并相应调整
`desktop/build/linux/nfpm/nfpm.yaml`。

### 5. 交叉编译与 CI

- Windows 可执行文件可从 macOS/Linux 免 CGO 交叉编译：
  `wails3 task build GOOS=windows`。
- macOS 和 Linux 构建依赖 CGO，正式发布应在目标 OS 上进行——推荐 GitHub
  Actions 三平台矩阵（每个平台一个 job），或使用 Wails 官方 Docker 交叉
  镜像（`wails3 task setup:docker`，约 800MB）。
- 交叉编译出的产物不带签名，分发前需在目标 OS 上完成签名。

### 运行时数据与迁移

桌面构建每次启动都会自动执行迁移。数据存放在用户配置目录：macOS 为
`~/Library/Application Support/<name>`，Windows 为 `%APPDATA%\<name>`，
Linux 为 `~/.local/share/<name>`。项目新增迁移或插件后，重跑
`airway desktop:init` 刷新内嵌副本。

框架的[桌面指南](https://github.com/daqing/airway/blob/main/docs/zh-CN/desktop.md)
包含完整的原理说明与注意事项。
