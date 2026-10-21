# 架构

## 定位

lifemail 是一个面向小团队的自托管邮件服务器：**一份二进制**同时提供
Exchange ActiveSync（邮件 / 日历 / 联系人）、IMAPS、SMTPS、收信 API 和管理控制台。
默认 SQLite，数据量或并发上来后可以换成 PostgreSQL。

- 协议层（EAS / IMAP / WBXML / MIME）来自真机验证过的实现，行为细节见 [eas-notes.md](./eas-notes.md)
- 数据层、HTTP 层、前端是重写的：**不迁移**重写前的旧库（PocketBase / v0.3.x）
- 从 v1 起 schema 仍通过编号迁移演进：只前向、已发布文件不可修改；
  破坏性变更按「加列 → 回填 → 删列」分版本完成。**不存在“不用管迁移”的例外。**

---

## 目录

```
cmd/lifemail/        入口：serve / user / db / version
internal/
  app 逻辑在 cmd 中组装  依赖注入、路由注册、生命周期
  config/            环境变量 → 显式 Config
  httpx/             ClientIP / RequestID 等 HTTP 工具
  authlimit/         按「账号 + IP」的登录限流（EAS/IMAP/SMTP/后台共用）
  store/             sqlx 持久层 + 版本化迁移
    migrations/      NNNN_name.<dialect>.sql
  mailparse/         MIME 解析（go-message）
  deliver/           投递编排与「入箱后」钩子（iMIP / OOF）
  outbound/          发件队列 worker
  eas/               EAS 协议：WBXML 命令、同步、ApplicationData
  wbxml/             WBXML 1.3 编解码
  imap/              IMAPS 993
  submission/        SMTPS 465 提交
  smtp/              Cloudflare Email Sending 客户端
  ical/              只读日历订阅
  api/               收信 API、/api/admin/*、/api/me/*、/metrics
  settings/          数据库中的运行时设置（域名、证书、SMTP）
  blobstore/         附件与 MIME 原文的文件存储
web/                 SvelteKit 管理控制台（构建产物 go:embed）
deploy/              Cloudflare Email Worker
docs/                架构与协议笔记
```

请求入口（`cmd/lifemail/main.go`）：

| 路径 | 处理者 |
|---|---|
| `/Microsoft-Server-ActiveSync` | `eas.Server` |
| `/autodiscover/autodiscover.xml` | `eas.Autodiscover` |
| `/api/inbound` | `api.Inbound`（Bearer token） |
| `/api/admin/*` | `api.Admin`（Bearer token 或会话 Cookie） |
| `/api/me/*` | `api.Me`（邮箱密码 Basic Auth） |
| `/calendar/<uuid>.ics` | `ical.Handler` |
| `/admin/` | 嵌入的前端静态资源 |
| `/healthz` `/readyz` | 存活 / 就绪（含 DB 探活） |
| `/metrics` | Prometheus 文本（需 Admin Token） |

---

## 数据层

### 迁移

- schema 由 `internal/store/migrations/NNNN_name.<dialect>.sql` 定义，`//go:embed` 进二进制
- 每个版本两种方言各一个文件（`sqlite` / `postgres`），语句保持一一对应
- `schema_migrations(version, name, checksum, applied_at)` 记录状态
- **只前向**：已应用的迁移不会重跑；如果文件内容变了（checksum 不一致）启动会失败，
  要求新增一个迁移而不是改历史。**不支持降级**，回滚等于恢复备份
- 破坏性变更分三步，不要在一个版本里同时做：
  1. 新增列（可空或带默认值），代码同时读写新旧字段
  2. 回填数据
  3. 下一版删除旧列
- 每个文件在事务里执行；PostgreSQL 的迁移连接使用 simple protocol 以便一次执行整个文件
- `schema_parity_test.go` 静态比对两种方言的表、列、索引与外键；新增迁移必须两边同时加

命令：

```bash
lifemail db status     # 列出已应用 / 待应用
lifemail db migrate    # 应用待执行迁移
```

`serve` 默认自动迁移（`LIFEMAIL_AUTO_MIGRATE=1`）。希望把 DDL 从启动路径里拿掉时：

```bash
LIFEMAIL_AUTO_MIGRATE=0 ./lifemail serve
```

### 表

| 表 | 用途 |
|---|---|
| `users` | 账号（登录名不带域名，`name` 存小写） |
| `aliases` | 账号可收信的地址；`is_primary=1` 的是默认发件地址 |
| `folders` | 邮件 / 日历 / 联系人集合；`uid_validity` 供 IMAP |
| `messages` | 邮件可搜索字段；MIME 原文在 blob |
| `attachments` | 附件元数据；正文在 blob |
| `contacts` | 联系人 |
| `events` | 日历事件，UTC；重复规则平铺 8 列（`recurrence_interval=0` 表示不重复） |
| `event_exceptions` | 重复事件的单次例外，主键 `(event_id, occurrence)`，单次修改是普通 UPSERT |
| `changes` | 每个文件夹的单调递增变更流（EAS Sync 的基础） |
| `sync_states` | 每台设备每个文件夹消费到的位置与 sync key |
| `sync_items` | 设备已知的 ItemId，用来区分 Add / Change |
| `devices` | EAS 设备（协议版本、策略 key、擦除标记、最近 IP） |
| `settings` | 运行时设置键值 |
| `outbox` | 外部投递队列；MIME 在 blob |

所有账号 / 文件夹子表都带 `ON DELETE CASCADE` 外键，删除账号是一条 `DELETE FROM users`，
不再手写级联。payload 文件在删除账号前单独收集并清理。

### 存储分层

- **数据库**：可搜索、可索引的字段（主题、发件人、正文纯文本/HTML、时间、标志位、路径）
- **blob 目录**：MIME 原文（`raw/`）、附件正文（`att/`）、排队邮件（`out/`），
  按 id % 256 分目录、文件名带内容哈希
- blob 目录可以是 rclone 挂载点；**必须与数据库一起备份**

SQLite 连接串固定开启 `journal_mode(WAL)`、`busy_timeout(10000)`、`foreign_keys(ON)`、
`synchronous(NORMAL)`、`_txlock=immediate`。

---

## 同步模型（EAS）

```
写入邮件/事件/联系人
        │
        ▼
   changes(seq, folder_id, item_id, kind)   ← 单调递增
        │
        ▼
sync_states(device, folder, seq, sync_key)  ← 每台设备消费到哪
        │
        ▼
sync_items(device, folder, item_id)         ← 设备已知条目，决定 Add/Change
```

- 客户端提交的修改会产生变更记录，但在本次响应里跳过，不回显
- 删除是软删除；`DeletesAsMoves` 为真时移动到「已删除」
- `PruneChanges` 只删除所有设备都消费过的记录（`seq <= MIN(sync_states.seq)`）

---

## 认证与限流

- 密码：bcrypt；账号可用登录名或任一地址登录（地址 → 账号的解析在 `store.UserByLogin`）
- 限流：`authlimit` 按「账号 + 客户端 IP」计数，10 分钟内 5 次失败锁定 5 分钟
  - EAS 返回带 `Retry-After` 的 401
  - SMTPS 返回 `454 4.7.0`
  - 后台登录返回 429
- EAS 额外有一层 5 分钟的密码校验缓存（key = 账号 + 密码摘要 + 当前 hash），
  避免每个长轮询请求都跑一次 bcrypt；改密码后缓存自动失效
- 客户端 IP **总是**取 `X-Forwarded-For` 第一个地址：本服务假定 100% 部署在 CDN / 反向代理后面
- 后台会话：`HttpOnly + SameSite=Strict` Cookie，HMAC 签名，12 小时过期；
  写操作要求 JSON 且校验 `Origin`

---

## 运行时设置

域名、TLS 证书路径、SMTP 参数存在 `settings` 表，通过 `GET/PUT /api/admin/settings` 读写，
保存后证书即时重新加载。SMTP 密码永不回显，只返回 `password_set`。

---

## 前端

- SvelteKit 3 + Svelte 5 runes + TypeScript + Tailwind CSS 4
- `adapter-static`，`paths.base=/admin`，全部页面预渲染，SSR 关闭
- `#lib/*` 是源码内的共享模块（组件、API 客户端、格式化、状态）
- 构建产物通过 `go:embed` 进二进制；`_app/immutable/*` 返回一年 immutable 缓存，
  HTML 返回 `no-cache`
- API 客户端集中在 `web/src/lib/api.ts`，类型在 `lib/types.ts`；401 统一回到登录页
- 页面按职责拆分：概览 / 用户 / 地址别名 / 域名证书 / 发件队列 / 设备 / 日历订阅 / 设置 / 诊断

---

## 测试

- Go：`go test ./...`，覆盖 WBXML、EAS 命令与同步、IMAP、SMTP 提交、收信 API、管理接口、
  日历 feed、迁移与外键级联
- 前端：`npm run check`（svelte-check 全量类型检查）
- 构建：`make build`（先 `npm ci && npm run build`，再交叉编译静态二进制）
- 本地全量校验：`make check test`
