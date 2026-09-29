# Air Registry 开发任务清单

按编号顺序逐步完成;每个 Task 独立可交付、可验证,做完一个勾一个。需求背景见
README「目标与需求」。

## 使用说明

- 开发循环:`go run .` 启动服务(local 环境),`go test ./...` 跑测试;
  端到端验证需要本机 Docker(匿名 registry,`localhost` 免配 insecure)。
- 端点类 Task 都要补 Airway 风格测试(参考 `app/api/storage_api/storage_test.go`)。
- 建议每完成一个 Task 在 develop 分支提交一次。
- 技术约定:
  - API 基础路径 `/v2/`,digest 格式 `sha256:<64 hex>`(宽松校验:`[a-z0-9]+:[a-f0-9]{32,}`)。
  - manifest 字节**原样存取**,不做 schema 转换;docker schema2 与 OCI 媒体类型都要支持。
  - blob 存 `data/storage/`,内容寻址,路径如 `data/storage/blobs/sha256/ab/abcdef...`。
  - **Gin 路由注意**:仓库名有多级(如 `library/nginx`),而 Gin 的 `*path`
    通配只能出现在路由末尾,所以 `/v2/*path` 用一个入口接住后自行解析
    (name / 资源类型 / digest),不要在 Gin 路由里写 `:name`。

## 阶段一:基础

### T01 数据库 schema 与 migration [done]

- 目标:建好核心表,后面所有端点依赖它。
- 要点:
  - 表:`repositories`(name unique)、`blobs`(digest unique, size, path)、
    `repo_blobs`(repo_id + blob_id 唯一,记录 blob 属于哪些仓库,跨仓库
    mount 和 GC 都要用)、`manifests`(repo_id, digest, media_type,
    artifact_type 可空, subject_digest 可空, size, content 原样字节)、
    `tags`(repo_id, name, manifest_digest, updated_at;repo_id + name 唯一)。
  - Referrers 不需要单独表:`manifests.subject_digest` 反查即可。
  - 用 `airway generate model ...` 生成,再补 migration。
- 验收:`airway db:migrate` 成功;model 测试覆盖增删查。

### T02 磁盘 blob 存储服务 [done]

- 目标:封装内容寻址的 blob 文件读写,供端点层复用。
- 要点:
  - 新建 `app/services/blobstore/`:Put(io.Reader, digest) → 边写边算
    sha256,与声明 digest 不一致则报错并清理临时文件,一致则 rename 落盘;
    Has / Get / Size / Delete。
  - 路径 `data/storage/blobs/<algo>/<hex>`,写 `.tmp` 后原子 rename。
  - 已存在同 digest 时直接成功(幂等)。
- 验收:单元测试覆盖写入→读取→digest 不符→删除。

### T03 `GET /v2/` 版本探测 [done]

- 目标:registry 探测端点,所有客户端推拉前的第一步。
- 要点:`airway generate api v2`,挂 `GET /v2/`,返回 200 和响应头
  `Docker-Distribution-API-Version: registry/2.0`。
- 验收:`curl -i http://localhost:1900/v2/` 见 200 与上述响应头;测试覆盖。

## 阶段二:拉取端点

### T04 blob 下载端点 [done]

- 目标:`GET /v2/<name>/blobs/<digest>` 返回 200 + `application/octet-stream`,
  `HEAD` 同路径返回 200/404。
- 要点:
  - 从 T02 的 blobstore 取流,响应带 `Docker-Content-Digest` 头;未命中
    返回 404 `BLOB_UNKNOWN`。
  - digest 格式非法 → 400;格式合法但不存在 → 404。
  - **T09 起按仓库校验**:HEAD/GET 要求 blob 已关联本仓库(repo_blobs),
    磁盘有但未挂载/上传到本仓库 → 404,否则跨仓库"已存在"会让客户端
    跳过 mount/upload。
- 验收:测试塞入 blob 后下载字节一致;404 路径有测试。

### T05 manifest 写入服务 + `PUT /v2/<name>/manifests/<reference>` [done]

- 目标:能写入 manifest 并把元数据解析入库。
- 要点:
  - reference 支持 tag 和 digest 两种。
  - 读取 body 原样保存;按 mediaType 解析 config / layers / annotations /
    subject;index 类型解析子 manifest digest 列表。
  - 计算 digest,连同原样字节写 `manifests` 行;reference 为 tag 时 upsert
    `tags` 行。
  - 把 config + layers(及 index 的子 manifest)digest 写入 `repo_blobs`。
  - subject 非空时记录 `subject_digest`(为 Referrers 铺垫)。
  - 响应 201 + `Docker-Content-Digest` 头;同 digest + tag 重复 PUT 幂等。
- 验收:测试验证 PUT 后四张表记录正确;curl PUT 一个手写 manifest JSON。

### T06 manifest 读取 + tag 列表 [done]

- 目标:`GET`/`HEAD /v2/<name>/manifests/<reference>`(tag 或 digest),以及
  `GET /v2/<name>/tags/list`。
- 要点:
  - Accept 头同时兼容 OCI 与 docker schema2 媒体类型;按存储的 mediaType
    原样返回,带 `Docker-Content-Digest` 头;未命中 404 `MANIFEST_UNKNOWN`。
  - tags/list 返回 `{"name": ..., "tags": [...]}`;先不加分页,n/last
    查询参数透传不报错即可。
- 验收:测试覆盖 tag 引用、digest 引用、404;curl 验证返回字节与 PUT 时一致。

## 阶段三:推送端点

### T07 blob 单块上传 [done]

> 注:已用 lima 虚拟机里的**真实客户端**完成验收(不用 Docker Desktop):
> `limactl create --name=reg-verify template://docker`,VM 内配
> `/etc/docker/daemon.json` 的 `insecure-registries: ["192.168.5.2:1930"]`
> (网关 IP 即宿主机),`docker push` / `docker pull` / `docker run` 全部
> 通过;另在 a1s 实例用 nerdctl(容器栈)推过 OCI index 多架构镜像,亦成功。
> 复验步骤:宿主机 `LISTEN=":1930" DSN=... go run .`,VM 里对
> `192.168.5.2:1930/<name>` 推拉即可。该 VM 已 `limactl stop`,可
> `limactl delete reg-verify` 删除。T10 的 pull 验收已顺带预验证。
>
> **T09 复验时发现的 VM 环境坑**(rootless docker 模板与 T07 当时不同):
> - 生效的 daemon 配置是 `~/.config/docker/daemon.json`,要在这里加
>   `insecure-registries`(改 `/etc/docker/daemon.json` 无效);
> - VM 继承的 `http_proxy=192.168.5.2:9567` 会拦截 dockerd 到宿主机的
>   请求,需给 user 级 docker.service 加 drop-in:
>   `~/.config/systemd/user/docker.service.d/no-proxy.conf`,内容为
>   `[Service]` + `Environment="NO_PROXY=192.168.5.2"`。
>   这两项 T09 时已配好并留在 VM 磁盘里,`limactl start` 后直接可用。

- 目标:docker push 走的主链路。
- 要点:
  - `POST /v2/<name>/blobs/uploads/` → 202,`Location` 头给出 upload URL,
    `Range: 0-0`,`Docker-Upload-UUID`。
  - `PUT <upload URL>?digest=<digest>`(带完整 body)→ 校验 digest、落盘,
    201 + `Docker-Content-Digest`。也支持 `POST ...?digest=` 一步完成。
  - 上传状态可放内存 map(UUID → 临时文件),重启丢弃即可(客户端会重试)。
- 验收:`docker push localhost:1900/demo/app:v1` 成功,docker 能完整推完。

### T08 blob 分块上传 [done]

- 目标:`PATCH`(可多次,`Content-Range` 续传)+ 最后一次 `PUT ?digest=`。
- 要点:维护已写 offset,每次响应 `Range: 0-<n-1>`;`PUT` 时统一校验 digest;
  非法 offset / digest 不符报错。
- 验收:`crane push`(走 chunked)成功;测试覆盖多次 PATCH 续传。

### T09 跨仓库 blob mount [done]

> 注:已用 reg-verify VM(lima)里的真实 docker 复验。同一 busybox 推到
> `mount-test/base` 再推到 `mount-test/app`,后者输出
> `55d2dadd4bbc: Mounted from mount-test/base`;服务端日志确认
> `POST .../mount-test/app/blobs/uploads/?mount=<digest>&from=mount-test/base`
> 返回 201,而 base 首次推送时 `?from=mount-test/app` 不命中返回 202
> 并自动降级为普通上传(docker 会按本地 tag 自动尝试兄弟仓库)。磁盘上
> 两个仓库共享同一份 blob 文件。VM 环境坑见 T07 注。

- 目标:`POST /v2/<name>/blobs/uploads/?mount=<digest>&from=<repo>`。
- 要点:目标 digest 在 `from` 仓库存在时,直接复制 `repo_blobs` 关联,
  返回 201 + `Docker-Content-Digest`;不存在则降级为普通上传(202)。
- 实现:`blobs.Mount`(源仓库→blob→repo_blobs 三段校验,幂等链接);
  `startUpload` 识别 `mount`+`from` 参数;digest 非法仍 400。
- 顺手修了两个验收中暴露的问题:
  - **并发上传竞态**:docker 并行推层,新仓库首次并发 `POST ?digest=` 时
    `EnsureRepository` 撞 `UNIQUE constraint failed: repositories.name` 返回
    500;`EnsureRepository`/`ensureBlob`/`linkBlobToRepo` 均改为撞约束后
    重读并接受已有行(有回归测试 `TestConcurrentMonolithicUploadsToNewRepo`)。
  - **blob HEAD/GET 按仓库校验**(见 T04 注):否则磁盘全局内容寻址会让
    客户端误以为目标仓库已有 blob,永远走不到 mount。
- 验收:同一份基础镜像推到两个仓库,docker 日志确认走了 mount;测试覆盖
  命中与降级两条路径。

## 阶段四:发现、删除与回收

### T10 端到端拉取验证 [done]

- 目标:确认 pull 链路在真实客户端下完整可用。
- 验收:
  - `docker pull localhost:1900/demo/app:v1` 成功,运行 `docker run --rm
    localhost:1900/demo/app:v1` 无异常。
  - `crane manifest` / `crane ls` / `crane blob` 均正常。
- 验收记录(2026-09-29,reg-verify VM + 宿主机 crane v0.22.1):
  - VM 内推 `192.168.5.2:1930/demo/app:v1/v2`(busybox,rmi 后重新
    `docker pull` 再 `docker run --rm ... echo` 正常;T09 起 blob 按仓库
    校验,本次 pull 顺带再次确认该改动无回归)。
  - `crane ls` → v1/v2;`crane manifest` → OCI manifest 字节完整;
    `crane blob` 下载层字节数与 manifest 中 size 一致(1915390)。
  - 注:crane 用 `go run github.com/google/go-containerregistry/cmd/crane@latest`
    跑在宿主机,localhost 默认按 HTTP 直连;VM 环境配置见 T07 注。

### T11 `/v2/_catalog` [done]

- 目标:仓库枚举,支持 `n` / `last` 分页。
- 实现:新服务 `app/services/catalog`,`List(last, n)` 按 name 字典序、
  `last` 开区间、`n+1` 探一行判断后续页;`v2_api/catalog.go` 返回
  `{"repositories": [...]}`(空库为 `[]` 非 null),有后续页时带
  `Link: </v2/_catalog?n=..&last=..>; rel="next"`;`n` 非法(<1 或非数字)
  400。`_catalog` 为规范保留字,在 Dispatch 最前面单独路由。
- 验收:`curl 'http://localhost:1900/v2/_catalog?n=2'` 分页正确;测试覆盖
  (空库、排序、Link 翻页、仅 last、越界、非法 n、非 GET 405→404)。

### T12 删除 manifest [done]

- 目标:`DELETE /v2/<name>/manifests/<digest>`(按 tag 删除时先解析到
  digest)。
- 实现:`manifests.Delete` — 复用 `Find` 解析引用;删除该 digest 的全部
  tags 行与 manifests 行;对本仓库 repo_blobs 只解除"仅被此 manifest 引用"
  的 blob 关联(同仓库其他 manifest 仍引用的保持关联);随后 T13 的 GC
  自动回收孤儿 blob 的文件与表行;响应 202,未命中 404
  `MANIFEST_UNKNOWN`。
- **已知限制(有意不做)**:删除 subject 后,其 referrers 的
  `subject_digest` 成为孤儿,不做级联删除/清空;index 的子 manifest 也
  不级联(可按 digest 单独拉取/删除)。
- 验收记录(2026-09-29,宿主机 crane v0.22.1):
  - `crane copy busybox localhost:1930/demo/app:v1`(完整多架构 OCI
    index,10 平台 + attestation)→ `crane delete localhost:1930/demo/app:v1`
    → 再 `crane manifest` 返回 404 `MANIFEST_UNKNOWN`;tags/list 空。
  - 删除 index 后子 manifest 按 digest 仍可拉(非级联);只被已删 manifest
    引用的层解除关联(HEAD 404),仍被其他 manifest 引用的层保持 200。
- 测试覆盖:按 digest/按 tag 删除、多 tag 指向同 digest、同仓库共享层保留、
  跨仓库 mount 关联保留、blob 文件删除后仍在磁盘待 GC、未知引用 404。

### T13 垃圾回收(GC) [done]

- 目标:清理不再被任何 manifest 引用的 blob。
- 实现:新服务 `app/services/gc`。`Run(store)` 扫描全部 manifests 收集
  存活 digest(manifest 自身内容 digest + config/layers + index 子
  manifest),其余 blob 删文件 + 删 blobs 行 + 删 repo_blobs 关联,返回
  `{kept, deleted}` 统计。挂载点:DELETE manifest 成功后自动执行(失败仅
  记日志不影响 202);手动兜底 `./tmp/air-registry-e2e gc`(main.go 在
  CLI 分发前拦截,用与 server 相同的 `DSN`/`STORAGE_ROOT`)。
- 语义注意:auto-GC 以"全库 manifests"为存活依据,所以删 index 不级联
  时子 manifest 仍存活、其 blob 不会被回收(T12 的非级联语义);上传
  blob 不触发 GC,孤儿由 DELETE 或手动 gc 清。
- 验收记录(2026-09-29,crane + 手工命令):
  - 推单架构 busybox(2 blob)→ 删 manifest → 磁盘文件 0。
  - DELETE 的 auto-GC 顺带清掉同库孤儿 blob。
  - 上传孤儿 → 手动 `gc` 回收(deleted 1)→ 重复执行幂等(deleted 0)。
- 测试覆盖:引用中的 blob 全保留、DELETE 后文件+行+关联全清、显式
  `gc.Run` 幂等。

## 阶段五:OCI 1.1 Referrers

### T14 Referrers API [done]

- 目标:`GET /v2/<name>/referrers/<digest>` 返回 OCI index。
- 实现:`manifests.Referrers(repoName, digest, artifactType)` 按
  `subject_digest` 反查(manifests 表无 annotations 列,从 content 现解析,
  `Meta` 相应新增 `Annotations` 字段);handler 组装
  `application/vnd.oci.image.index.v1+json`,descriptor 含 digest、
  mediaType、size、artifactType(可空则省略)、annotations(非空才带);
  `?artifactType=` 精确过滤;未知仓库/无结果一律 200 空 index
  (`"manifests":[]` 非 null),digest 非法 400。
- 验收记录(2026-09-29):crane 推 single-arch busybox 后手写 PUT sbom
  referrer,`curl .../referrers/<digest>` 返回正确 envelope(Content-Type
  为 index 媒体类型,descriptor 五项齐全),过滤 1/0 正确;T15 将用 oras
  做端到端。
- 测试覆盖:空结果(未知 subject/未知仓库)、descriptor 字段、artifactType
  过滤、非法 digest、非 GET 拒绝。

### T15 Referrers 端到端验证

- 目标:真实签名/SBOM 附件链路可用。
- 验收:
  - `oras attach localhost:1900/demo/app:v1 sbom.json
    --artifact-type application/vnd.example.sbom`
  - `oras discover localhost:1900/demo/app:v1` 能看到附件;
    `curl .../referrers/<digest>` 返回内容一致。

## 阶段六:网页端

### T16 首页 + 仓库列表页

- 目标:改造 `app/views/home`,首页放搜索框 + 最近推送仓库;新增
  `/repos` 分页列表(名称、tag 数、总大小、更新时间)。
- 要点:服务端渲染 templ + 分页组件(`app/assets/js/ui/pagination.tsx`
  已有现成组件);按 updated_at 倒序。
- 验收:浏览器打开可见仓库列表,分页可点;空库时有 empty state。

### T17 镜像搜索

- 目标:按仓库名模糊搜索。
- 要点:搜索走 `/repos?q=`,SQL `LIKE`(注意转义 `%`/`_`);结果页与列表页
  复用;无结果显示 empty state + 清空搜索入口。
- 验收:输入关键词过滤正确,特殊字符不报错;测试覆盖。

### T18 镜像详情页

- 目标:`/repos/<name>` 展示该仓库的 tag 列表与 manifest 详情。
- 要点:
  - tag 表:tag 名、digest、大小、推送时间。
  - 点开 tag:layers(config + 各层,digest、size、mediaType)、总大小、
    manifest digest、annotations。
  - 有 referrers 时列出附件(artifactType + digest,链到对应 manifest)。
- 验收:多 tag、多架构(index)镜像展示正确;测试覆盖。

## 阶段七:收尾

### T19 配置整理

- 目标:把硬编码项收进配置。
- 要点:storage 根目录(`DATA_DIR`,默认 `data/storage`)、上传大小上限、
  认证开关占位(默认关,留出 middleware 扩展点)。
- 验收:改配置生效;`.env.example` 同步;测试不依赖具体目录。

### T20 OCI Distribution Spec conformance 测试

- 目标:用官方套件验证兼容性。
- 要点:clone `opencontainers/distribution-spec`,用其 conformance 测试,
  环境变量指向本地实例,覆盖 pull / push / content discovery / referrers
  各组。
- 验收:全部通过;把失败项修完或明确记录已知差异。

### T21 文档与清理

- 目标:收尾。
- 要点:README 补使用方式(docker 配置示例、网页入口)、已知限制;过一遍
  代码,删掉脚手架遗留的无用文件;确认注释与实现一致。
- 验收:`go vet ./... && go test ./...` 全绿;两份 README 同步。

## 后续扩展(暂不做,仅记录)

- HTTP basic auth(推拉分权)、htpasswd 风格用户管理
- TLS / 反代部署指南
- 网页端管理操作(删除 tag / 仓库)
- 按仓库配额、只读模式等策略
- 定时 GC / 存储用量统计

## 附录:端点 ↔ Task 对照

| 端点 | 方法 | Task |
|---|---|---|
| `/v2/` | GET | T03 |
| `/v2/<name>/blobs/<digest>` | GET / HEAD | T04 |
| `/v2/<name>/manifests/<ref>` | PUT | T05 |
| `/v2/<name>/manifests/<ref>` | GET / HEAD | T06 |
| `/v2/<name>/tags/list` | GET | T06 |
| `/v2/<name>/blobs/uploads/` | POST | T07 |
| `<upload>` | PUT(`?digest=`) | T07 / T08 |
| `<upload>` | PATCH | T08 |
| `/v2/<name>/blobs/uploads/?mount=&from=` | POST | T09 |
| `/v2/_catalog` | GET | T11 |
| `/v2/<name>/manifests/<digest>` | DELETE | T12 |
| GC(内部 + `go run . gc`) | — | T13 |
| `/v2/<name>/referrers/<digest>` | GET | T14 |
