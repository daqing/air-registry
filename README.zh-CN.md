# Air Registry

一个基于 [Airway](https://github.com/daqing/airway) 的应用:一个 OCI 兼容的
容器镜像 registry,提供网页端,可搜索、浏览已存储的镜像。概念上与
[Zot](https://zotregistry.dev/) 类似,但基于 Airway 框架从零实现——不使用
任何 Zot 源代码,也不依赖 Zot。

已通过官方 OCI Distribution Spec conformance 套件(v1.1.1,74/74 项:
pull、push、content discovery、content management)。

## 功能特性

- **OCI Distribution Spec API** — 标准 `/v2/` 端点,常见 OCI 客户端
  (docker、crane、containerd、oras 等)无需任何客户端改动即可推拉:
  - blob 上传(整体与分块,支持断点续传)、blob 下载
  - manifest PUT/GET/HEAD/DELETE,tag 列表(支持 `n`/`last` 分页)
  - blob `DELETE`,跨仓库 blob mount
  - `/v2/_catalog` 仓库枚举
  - OCI 1.1 Referrers API(含 `?artifactType=` 过滤),查询通过 `subject`
    关联的签名、SBOM 等附属制品
- **存储** — blob 以内容寻址文件形式存储在磁盘(`DATA_DIR` 下,默认
  `data/storage/`);仓库、tag、digest、referrer 等元数据通过 Airway model
  索引进数据库
- **垃圾回收** — 每次删除 manifest 后自动执行;手动 `gc` 命令可清扫
  上传后从未被引用的孤儿 blob
- **网页端** — 服务端渲染页面:按名称搜索仓库、分页浏览、查看镜像详情
  (tag、层、多架构平台、annotations、referrers)
- **认证** — 默认匿名读写;`REGISTRY_AUTH_ENABLED` 为 basic-auth/htpasswd
  后端的占位开关(路线图中)

## 快速开始

```bash
cp .env.example .env   # 然后设置 AIRWAY_ENV=local、DSN 和 LISTEN
go run . db:migrate    # 建表
go run .               # 启动服务
```

按 `.env.example` 的默认值,服务监听 `127.0.0.1:1905`。本地典型的
`DSN` 为 `sqlite://./tmp/registry-dev.db`。

## 使用 registry

### docker

registry 使用纯 HTTP,需要把地址加入 docker 的 insecure registry 列表。
编辑 `/etc/docker/daemon.json`(Docker Desktop:设置 → Docker Engine):

```json
{
  "insecure-registries": ["192.168.1.10:1905"]
}
```

(`192.168.1.10` 为运行 registry 的主机;`localhost` 无需配置。)重启
docker 后即可推送:

```bash
docker tag myapp:v1 192.168.1.10:1905/demo/app:v1
docker push 192.168.1.10:1905/demo/app:v1
docker pull 192.168.1.10:1905/demo/app:v1
```

### crane / oras

两者都可直接以 HTTP 访问 `localhost`:

```bash
crane ls localhost:1905/demo/app
crane copy busybox localhost:1905/library/busybox:latest

oras attach localhost:1905/demo/app:v1 sbom.json:application/json \
  --artifact-type application/vnd.example.sbom
oras discover localhost:1905/demo/app:v1
```

### 用 Caddy 启用本地 HTTPS

仓库自带 `Caddyfile`,在 `https://air-registry.localhost:8443` 终结 TLS
并反代到应用的 `LISTEN` 地址(取自 `.env`,默认 `127.0.0.1:1905`)。Caddy 的内部 CA 会自动签发和续期证书,无需
真实域名或 ACME 配置:

```bash
brew install caddy   # 一次
caddy trust          # 一次:把 Caddy 本地根 CA 加入系统信任链
just caddy           # 与应用同时运行
```

浏览器、`crane`、`oras` 之后可直接访问 `air-registry.localhost:8443`,
无需 insecure-registry 配置。Docker Desktop 的 daemon 运行在独立 VM 中,
使用自己的信任链,因此 `docker` 需要执行:

```bash
just copy-docker-cert
```

它会把 Caddy 根证书复制到 `~/.docker/certs.d` 并重启 Docker
Desktop;之后 `docker push air-registry.localhost:8443/demo/app:v1` 即可
走 HTTPS,无需 `insecure-registries` 配置。

[Lima](https://lima-vm.io/) (`nerdctl.lima`) 除了同样需要信任 CA,还需要
绕过代理:Lima 会把宿主机的 HTTP(S) 代理设置传播进 VM,但不带
`*.localhost` 的 `NO_PROXY` 条目,registry 请求会死在代理里(报 `EOF`)。
执行:

```bash
just setup-lima-client
```

它会把 `air-registry.localhost` 指向宿主机(`host.lima.internal`)、信任
Caddy 根证书,并在 nerdctl CLI 与 rootless containerd daemon 都能读到
的位置加上 `NO_PROXY`,然后重启实例。之后即可在 VM 里直接
`nerdctl push air-registry.localhost:8443/demo/app:v1`。`limactl delete`
重建 VM 后需要重跑一次。

### 网页端

打开 `http://localhost:1905/` ——首页有搜索框和最近推送的仓库;`/repos`
分页列出全部仓库(支持 `?q=` 搜索);`/repos/<name>` 展示该仓库的 tag,
点击 tag 查看 manifest 详情(config、layers、总大小、annotations),index
可下钻各平台。referrers 会列出并链接到对应 manifest。

## 配置

所有配置均为环境变量(见 `.env.example`):

| 变量 | 默认值 | 含义 |
|---|---|---|
| `LISTEN` | `127.0.0.1:1905` | HTTP 监听地址 |
| `DSN` | — | 数据库 DSN,如 `sqlite://./tmp/registry-dev.db` |
| `DATA_DIR` | `./data/storage` | blob 存储根目录(`STORAGE_ROOT` 作为旧名仍兼容) |
| `MAX_UPLOAD_SIZE` | `0`(不限) | 单请求 blob 上传上限(字节),超限返回 413 |
| `REGISTRY_AUTH_ENABLED` | `false` | 认证开关占位:开启后 `/v2/` 请求一律 401,直到接入凭据后端 |
| `URL_PREFIX` | — | 反代部署时把网页端和 API 挂在子路径下 |

## 垃圾回收

删除 manifest 会解除仅被它引用的 blob 关联并自动触发一次 GC。只上传
从未被引用的 blob(例如半途而废的推送)会一直留在磁盘上,直到手动回收:

```bash
DSN=sqlite://./tmp/registry-dev.db go run . gc
# gc: kept 12 blobs, deleted 1
```

## 已知限制

- **尚无内置认证** —— 能访问 registry 的人即可推送和删除;在 basic-auth
  后端落地前,请仅在私有网络中部署。
- **无内置 TLS** —— 超出可信局域网时请用反代(nginx、Caddy)终结
  HTTPS;记得同时移除 docker 的 `insecure-registries` 配置。
- **上传 session 在内存中** —— 重启服务会丢弃进行中的分块上传,客户端
  会自动重试。
- **单节点、以 SQLite 为主** —— 无集群、无消息队列;schema 经由 Airway
  支持 Postgres/MySQL,但日常只验证 SQLite。
- **无配额与只读模式** —— 仓库配额、只读开关均为路线图事项。

## 开发

```bash
go run .            # 启动服务(local 环境下前端 bundle 在内存中热构建)
go test ./...       # 单元与 API 测试
go vet ./...
```

OCI conformance 套件(vendored 在 `deps/distribution-spec`,已被
gitignore)可对本地实例执行——确切命令见
[docs/TASKS.md](docs/TASKS.md) 的 T20 记录。任务历史与验收记录都在该文件中。
