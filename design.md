# 用户后台（Webmail）设计

## 1. 目标与非目标

**目标**：为邮箱账号所有者提供一个网页端后台，能力限定为：

1. 邮件按**会话**（同一个 `conversation_id`）分组展示，可展开阅读正文、下载附件、标记已读，
   **回复 / 全部回复**（纯文本，不引用原文）。
2. 日历：列出/创建/重命名自己的日历，查看事件，**新建、编辑、删除单次事件**。
3. 联系人：跨联系人文件夹查看与搜索，**新建、编辑、删除**联系人。

**非目标（本期不做）**：

- 新写信（回复之外的撰写）、转发、草稿保存、提交到 outbox。
- 删除、移动邮件、归档、垃圾邮件判定。
- 在网页上设置重复事件规则：编辑客户端创建的重复事件时保留原规则。
- 用户自助改密码、多用户管理（仍是 `/admin/` 的职责）。

## 2. 已确认的决策

| 决策点 | 选择 |
|---|---|
| 前端形态 | 复用现有 `web/`，SvelteKit `base` 从 `/admin` 改为 `/`；用户后台在 `/app/*`，运维后台迁到 `/admin/*` |
| 入口 | `/` 一律重定向到 `/app/` |
| 用户认证 | 邮箱账号（login name 或主地址）+ 密码 → HttpOnly 签名 Cookie，12 小时 |
| 日历 | 创建 / 重命名日历 + 查看事件列表 |
| 邮件 | 会话列表（收件箱 + 已发送合并）+ 正文 + 附件下载 + 标记已读 + 回复/全部回复 |

## 3. 路由与静态资源

### 3.1 前端路由（SvelteKit）

```
/                        302 → /app/
/app/login/              用户登录（账号密码）
/app/                    用户后台 · 邮件（会话列表）
/app/calendar/           用户后台 · 日历
/app/contacts/           用户后台 · 联系人
/admin/login/            运维登录（原 /login）
/admin/                  运维概览
/admin/users/  ...       其余原 (app) 页面
```

- `vite.config.ts` 中 `paths.base` 改为 `''`，并补 `trailingSlash: 'always'`。
- 现有 `src/routes/(app)/*` 整体移动到 `src/routes/admin/*`，`(app)/+layout.svelte` 移到 `admin/+layout.svelte`。
- 新增 `src/routes/app/` 承载用户后台：`app/+layout.svelte` 为独立侧栏布局，`app/+page.svelte` 为邮件页。
- 根路径跳转双层兜底：SvelteKit 侧 `src/routes/+page.ts` 在预渲染时产出 307 到 `/app/`；Go 根 handler 命中 `GET /` 时直接 302 到 `/app/`（反向代理把静态站单独发布时由前者兜底）。
- 全部页面预渲染（`ssr = false`），数据仍由客户端拉取。

### 3.2 Go 侧静态服务

`cmd/lifemail/routes.go`：

- 新增 `User http.Handler` 挂到 `/api/user/`。
- `/admin/` 不再 `StripPrefix`，改为直接把 `LIFEMAIL_WEB_DIR`（默认 `web/build`）挂到根上；
  `/admin/...` 是 `build/admin/...` 下的真实文件。
- 根 handler 的兜底顺序：EAS 大小写兼容路径 → `GET /` 重定向到 `/app/` → 静态文件。
- `web/handler.go` 的 immutable 缓存判定把 `/admin/_app/immutable/` 改为 `strings.Contains(path, "/_app/immutable/")`。
- `cmd/lifemail/routes_test.go` 同步更新（`/admin/` 仍返回 200；新增 `/`、`/api/user/*` 断言）。

## 4. 认证与会话

### 4.1 登录

- `POST /api/user/session` `{ "login": "...", "password": "..." }`
- 解析顺序与 EAS/IMAP 一致：`Store.UserByLogin(login)`（login name 或任一别名地址），`bcrypt.CompareHashAndPassword`。
- 失败：`401`，并用 `authlimit.Limiter` 以 `"user|<ip>"` 为 key 限流（复用 admin 的实现）。
- 成功：写 Cookie `lifemail_user`，返回当前用户资料。

### 4.2 Cookie

- 载荷：`<userID>|<expiryUnix>`
- 签名：`HMAC-SHA256(key = HMAC-SHA256(adminToken, "lifemail-user-session-v1"), payload)`，十六进制。
- 属性：`HttpOnly`、`SameSite=Lax`、`Secure`（按 `isHTTPS`）、`Path=/`、`MaxAge=12h`。
- 每次请求用 `UserByID` 复核用户仍存在；不存在即 401。
- 与 admin 的 `lifemail_admin` Cookie 互不干扰，可同时登录。
- `GET /api/user/session` 返回 `{authenticated, user?}`（未登录也返回 200）。
- `DELETE /api/user/session` 清除 Cookie。

### 4.3 CSRF

所有写操作要求：

- `Content-Type: application/json`；
- `sameOrigin(r)`（复用 admin 已有实现）；
- Cookie `SameSite=Lax`。

## 5. HTTP API 契约

统一前缀 `/api/user`，除 `/session` 外均需登录；`Cache-Control: no-store`。

### 5.1 会话与文件夹

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/mail/folders` | 邮件文件夹；`[{id,name,type,class,unread,total}]`，排除 Calendar/Contacts |
| GET | `/mail/conversations?folder=<id>&limit=50` | 会话摘要列表 |
| GET | `/mail/thread?key=<b64url>` | 该会话内的消息摘要（按时间升序） |
| GET | `/mail/messages/{id}` | 单封详情：正文 text/html + 附件元数据 |
| GET | `/mail/search?q=&limit=50` | 跨文件夹搜索（主题/发件人/收件人/正文） |
| GET | `/mail/attachments/{id}` | 附件下载（`Content-Disposition: attachment`） |
| POST | `/mail/messages/{id}/read` | 标记已读并 `AddChange(folder,"change","email",id)` |
| POST | `/mail/reply` | `{messageId,body,replyAll,subject}` → 发送纯文本回复 |
| POST | `/mail/send` | `{to,cc,bcc,subject,body}` → 发送新邮件 |

会话摘要字段：

```json
{
  "key": "cmVmOjEyMy4uLg",
  "subject": "Re: 合同",
  "from": {"name": "张三", "address": "z@example.com"},
  "to": "alice@example.com",
  "lastAt": 1792558000,
  "count": 4,
  "unread": 1,
  "hasAttachments": true,
  "preview": "正文纯文本前 140 字",
  "latestMessageId": 123
}
```

- `key` = `base64url(conversation_id)`；`conversation_id` 为空时用 `m:<messages.id>`，避免新旧数据无会话标识。
- **收件箱与已发送合并**成同一个会话（同 `conversation_id`），自己发出的回复会跟原信在一起；其他文件夹（草稿/垃圾/已删除）仍各自独立，不串到收件箱。
- `folder` 省略时取收件箱；在收件箱视图里只显示「至少有一封在收件箱」的会话，在已发送视图里只显示「至少有一封在已发送」的会话。
- 排序按该会话最新一封的 `date_received` 倒序，`limit` 默认 50、上限 200。
- `thread` 返回 `{folderId,sentFolderId,messages,replyTarget}`，`messages` 按时间升序（上限 200）；
  每封带 `folderId`，前端据此标出「已发送」。
- `replyTarget` = `{messageId,subject,to,cc}`：会话里默认要回复的那封（最新一封非已发送）及其默认收件人，
  已剔除自己的地址；无可用收件人时该字段缺省。

### 5.1.1 搜索

`GET /mail/search?q=<词>&folder=<id>&limit=<n>`：

- 默认搜**整个账号**（不是只搜当前文件夹）；传 `folder` 时限定到该文件夹，
  不属于自己的 folder 返回 `404`。
- 匹配字段：`subject`、`from_addr`、`from_name`、`to_addr`、`cc_addr`、`body_text`，
  大小写不敏感，`%`/`_` 按字面量处理（`ESCAPE '\'`）。
- 结果 `{query,total,hits:[{id,folderId,folderName,key,subject,fromName,fromAddr,to,date,isRead,preview}]}`，
  `key` 是 base64url 的会话 key，前端点击后直接开该会话（跨文件夹也能开）。
- 空 `q` 返回空结果（`200`），不当错误。

### 5.1.2 回复

`POST /mail/reply`：

- 默认（`replyAll=false`）只发给原信发件人；`true` 时再加上原信 To/Cc 的其他参与者，
  自动剔除自己的主地址与别名，去重后 To 优先、其余进 Cc。
- 传入 `to` / `cc` 数组时直接使用它们（用户在前端改了收件人）：同样会去掉自己的地址与重复项，
  清理后 To 为空返回 `400`，收件人总数上限 50，地址不含 `@` 返回 `400`。
- 原信 MIME 里取不到地址时回退到 `messages.from_addr` / `to_addr` / `cc_addr` 索引列。
- 生成纯文本 MIME（base64 正文），带上 `In-Reply-To` 与最近 10 个 `References`，
  主题缺省补 `Re:`；`conversation_id` 由同一套规则推导，因此回复落在原会话里。
- 先写已发送副本，再投递：本地收件人走 `deliver.ToUser`，外域走 `smtp.Sender`，
  投递失败进 `outbound` 队列并返回 `502`（响应 `{ok,queued}`）。
- 只能回复自己的邮件（`messages.user_id` 不匹配返回 `404`）。

### 5.1.3 写新邮件

`POST /mail/send`：

- 参数 `to` / `cc` / `bcc` / `subject` / `body`；收件人同样剔除自己的地址与重复项，
  清理后 To 为空、正文为空或地址不含 `@` 都返回 `400`，总数上限 50。
- `Bcc` 参与投递但不写进 `To:`/`Cc:` 头；本地收件人靠 `received_as_bcc` 标记。
- 与回复共用 `buildMessage`，但**不写** `In-Reply-To` / `References`，所以自成会话。
- 先写已发送副本，再投递（本地内部投递 / 外域 SMTP / 失败入 outbound 队列），
  返回 `{ok,queued}`。
- 草稿只存在浏览器的 `localStorage`（`lifemail.compose.draft`），不写服务器；
  发送成功后清除。

### 5.2 日历

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/calendars` | `[{id,name,public,subscribeUrl,eventCount}]` |
| POST | `/calendars` | `{name}` → 新建（`CreateFolder(userID,0,name,8)`） |
| PATCH | `/calendars/{id}` | `{name}` → `RenameFolder`（自动 bump folder seq） |
| PUT | `/calendars/{id}/public` | `{public}` → `SetFolderPublic`，返回订阅 URL |
| GET | `/calendars/{id}/events?from=&to=` | 事件列表，`from`/`to` 为 unix 秒 |
| POST | `/calendars/{id}/events` | 新建单次事件，`AddChange("add","event")` |
| PATCH | `/calendars/{id}/events/{eid}` | 更新标题/时间/地点/备注，保留重复规则，`AddChange("change","event")` |
| DELETE | `/calendars/{id}/events/{eid}` | 软删除 + `AddChange("delete","event")` |

- 所有 `{id}` 必须先校验 `Folder.UserID == 当前用户` 且 `Type == 8`、未删除。
- 事件字段：`id,uid,subject,location,body,start,end,allDay,busyStatus,recurrenceType,sequence`。
- 新建事件的 UID 由服务端生成（`<uuid>@lifemail`），组织者记为当前账号。
- 订阅 URL 复用 `/calendar/<uuid>.ics`，与 `/api/me/calendars` 行为一致。

### 5.3 联系人

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/contacts?q=&limit=100` | 跨该用户所有 Contacts 文件夹；`q` 匹配 `file_as/first_name/last_name/company/email1..3` |
| GET | `/contacts/{id}` | 单条详情，校验归属 |
| POST | `/contacts` | 新建（默认写入 Contacts 文件夹），`AddChange("add","contact")` |
| PATCH | `/contacts/{id}` | 部分更新，`AddChange("change","contact")` |
| DELETE | `/contacts/{id}` | 软删除 + `AddChange("delete","contact")` |

返回字段与 `store.Contact` 对齐（姓名、公司、职务、三个邮箱、电话、地址、备注）。新建时至少要有姓名、公司或邮箱；`fileAs` 为空时按“姓+名”（中文）或“名 姓”（西文）生成。

### 5.4 实时推送（SSE）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/events` | Server-Sent Events，登录账号的邮箱变更推送；`text/event-stream`，保持打开 |

- 进程内 `EventHub`（`internal/api/events.go`）按 `userID` 扇出，**不轮询、不查库**：
  每个标签页一条空闲连接，有新邮件才写数据。单账号最多 16 条流，超出返回 `429`（客户端 5s 后重试）。
- 触发点在投递层：`deliver.OnInboxDelivered`（inbound、EAS/SMTP 本地投递都会走到）向
  `EventHub` 发 `mail` 事件，负载为 `{kind,action,folderType,subject,from,at}`（主题/发件人截断，
  只作为提示，真实数据靠重新拉取）。
- 帧格式：连接建立先发 `event: ready`（客户端据此重新加载，覆盖断线期间漏掉的事件），
  之后每个事件是 `event: <kind>` + `data: <json>`；每 25s 一条 `: ping` 注释保活。
- 前端 `#lib/mail-events.svelte.ts` 维护 `connected / revision / last` 三个响应式状态：
  非 200 响应（401/429/5xx）由客户端自己指数退避重连，网络中断交给 `EventSource` 自动重连。
  邮件页监听 `revision`，合并 400ms 内的多次推送后刷新文件夹与会话列表，并弹一条新邮件提示。
- 边界：事件不跨进程（单实例部署），也不覆盖 IMAP `APPEND` 直插库；断线与多标签页的最终一致
  由 `ready` 触发的重新加载保证。响应带 `X-Accel-Buffering: no`，反向代理不得缓冲。
- 停机时 `EventHub.Close()` 先结束所有流，避免优雅关闭等浏览器断开。

## 6. 数据层新增（`internal/store`）

无 schema 变更，不需要迁移。

```go
// 文件夹未读数/总数，一条聚合，避免逐个 Count。
func (s *Store) FolderCounts(userID string) ([]FolderCount, error)

type Conversation struct {
    Key          string // 裸 conversation_id，空则 "m:<id>"
    Subject      string
    LastAt       int64
    LatestID     int64
    Count        int
    Unread       int
    HasAttachments bool
}

// 一条 SQL 分组，再按 LatestID 批量取摘要（两段查询，SQLite/PG 都可移植）。
func (s *Store) ConversationSummaries(folderID int64, limit int) ([]Conversation, error)
func (s *Store) ConversationMessages(folderID int64, key string, limit int) ([]*Message, error)

// sql.Expr 里用 CAST 保证 PostgreSQL 也能拼接：
// CASE WHEN conversation_id = '' THEN 'm:' || CAST(id AS TEXT) ELSE conversation_id END
func (s *Store) SearchContacts(userID, q string, limit int) ([]*Contact, error)
func (s *Store) EventsByFolderBetween(folderID, from, to int64) ([]*Event, error)
```

已存在、直接复用：`FoldersByUser`、`MessagesByFolder`、`MessagesByID`、`AttachmentsByMessage`、`AttachmentByID`、`MarkMessageRead`、`CalendarFolders`、`CreateFolder`、`RenameFolder`、`SetFolderPublic`、`EventsByFolder`、`ContactByID`、`ContactsByFolder`、`AddChange`。

补充 store 单测：会话聚合（同 `conversation_id` 多封、空 id 回退、跨文件夹隔离）、联系人搜索、事件时间过滤。

## 7. 前端信息架构

### 7.1 用户布局（`(user)/+layout.svelte`）

- 左侧固定侧栏（移动端抽屉）：邮件 / 日历 / 联系人。
- 顶栏：当前账号显示名与主地址、主题切换、退出登录。
- 沿用现有 11 个手写组件；新增 `Avatar`、`Tabs`、`Pane` 等在 `web/src/lib/components/`。

### 7.2 邮件页

三栏（≥1024px）：

```
┌ 文件夹 ┬ 会话列表 ────────────┬ 阅读窗格 ─────────────┐
│ 收件箱  │ ● 张三  合同   4封 1未读 │ 主题 / 发件人 / 时间   │
│ 已发送  │   李四  周报   2封      │ 正文（HTML 沙箱）      │
│ 草稿    │ ...                     │ 附件按钮              │
└─────────┴─────────────────────────┴───────────────────────┘
```

- 会话列表项：未读圆点、参与者、主题、时间（Luxon 相对时间）、封数、附件图标。
- 点击会话 → 阅读窗格加载 `thread`，按时间正序展示；进入即对未读消息调用 `read`。
- 会话列表顶部有搜索框：**输入不触发请求**，回车或点「搜索」才发一次（服务端很轻量，
  防抖自动搜索压力太大）；结果里每条带文件夹标签，点击直接打开会话（不受当前文件夹限制）。
- 写信**占据整个阅读窗格**（不是弹窗、也不是叠在底部）：点列表头部的「写邮件」后，
  阅读区换成 `写邮件 · 收件人 chip · 发送 · 关闭` + 主题 + 大正文框；打开会话时会先存草稿
  再回到回复模式，关闭写信则回到进入前的会话。
- 收件人 chip 打开同一个弹窗（写信模式多一个「密送」字段）。
- 阅读窗格底部是**聊天式回复区**（与会话连在一起，不再弹窗）：
  - 工具栏：当前回复对象（默认= 原发件人 + 原信 To/Cc，点开可编辑 To/Cc），以及「仅发件人」「全部回复」快捷切换；
  - 输入框：Enter 发送、Shift+Enter 换行，发送中禁用；主题自动为 `Re: 首封主题`，不需手填；
  - 发送成功后清空输入、刷新列表与会话，新副本带「已发送」标记。
- 会话内的消息带上「已发送」小标记（`folderId === sentFolderId`），区分自己发出的那些。
- 实时推送：列表头部有一个绿点表示 SSE 已连接；收到 `mail` 事件弹「新邮件 · 发件人：主题」，
  并把 400ms 内的多次推送合并成一次列表/未读数刷新（见 5.4）。
- HTML 正文用 `<iframe sandbox srcdoc>`（无 `allow-scripts`）渲染，保留内联样式、禁用脚本；无 HTML 时用 `whitespace-pre-wrap` 展示纯文本。
- 附件按 `attachments` 元数据渲染，下载走 API。
- 移动端：列表与阅读用单栏切换（返回按钮）。

### 7.3 日历页

- 日历列表（侧栏）+ 右侧按天分组的事件列表（默认今天起 30 天，可切换范围）。
- 「新建日历」弹窗输入名称；行内重命名；公开订阅开关与复制链接。
- 事件只读展示：时间、地点、重复标记、组织者。

### 7.4 联系人页

- 左侧字母/搜索框，右侧列表 + 详情双栏；移动端单栏。
- 无 `q` 时按 `file_as` 列出前 100 条；搜索防抖 250ms。
- 详情展示邮箱（`mailto:` 链接）、电话（`tel:`）、地址、备注。

## 8. 安全与边界

- 所有资源查询必须带 `user_id` 归属校验；`folder_id`、`message_id`、`attachment_id`、`event_id` 都不得直接信任客户端。
- 附件下载用 `Content-Type` 从 DB 取；`Content-Disposition` 用 `filename*=UTF-8''` 编码，避免响应拆分。
- HTML 正文不注入主文档 DOM；iframe `sandbox` 无脚本权限。
- 邮件 Markdown 不解析；纯文本原样显示。
- 不回传 `raw_path`、`password_hash`、其它用户信息。
- Cookie 签名密钥从 admin token 派生：admin token 未配置时用户后台登录返回 `503`（与 `/api/me` 的 Basic 认证仍可用）。

## 9. 对外契约影响

| 项 | 影响 |
|---|---|
| `messages.id` / `folders.uid_validity` / `calendar_uuid` | 不变 |
| `changes` / `sync_*` | 新增 Web 端已读操作会写 `changes`，按正常增量同步语义处理，不改变协议 |
| `/admin/` 静态资源 | 路径仍是 `/admin/*`，但 `_app` 资源从 `/admin/_app/*` 变为 `/_app/*`；运维后台整站重新构建后自洽 |
| `/api/me/*` | 不动，保留给脚本与订阅管理 |

## 10. 测试与验收

- `go test ./internal/store/`：新增会话聚合、联系人搜索、事件区间测试。
- `go test ./cmd/lifemail/`：路由与静态服务（`/` 302、`/app/login/`、`/admin/users/`、`/api/user/session`）。
- `go test ./internal/api/`：登录成功/失败/限流、越权访问他人在资源（folder/message/contact）返回 404。
- 回复：`TestUserReply` 覆盖回复全部（To/Cc 剔除自己、本地投递到两位收件人、已发送副本与会话归并、跨账号 404），
  `TestBareAddress` 覆盖收件人地址规范化。
- `cd web && pnpm run check`：类型检查。
- 手工验收（`make build` + 本地运行）：
  1. `/` 跳转到 `/app/`，`/app/login/` 用账号密码登录，刷新保持登录；
  2. 收件箱按会话分组，封数与未读数正确；打开后变已读；
  3. 回复/全部回复发出后，已发送副本出现在同一会话并标「已发送」;
  3. 附件可下载；HTML 正文不执行脚本；
  4. 新建日历后出现在列表，重命名保留 UUID（订阅链接不变）；
  5. 联系人可搜索、可看详情；
  6. `/admin/login/` 运维登录不受影响。

## 11. 实施阶段（每阶段完成后确认并提交）

1. **P0 基础重构**：base → `/`、admin 路由迁移、Go 静态服务与测试更新（不改功能）。
2. **P1 后端**：store 查询 + `/api/user/*` + 单测。
3. **P2 邮件**：用户布局 + 会话列表 + 阅读窗格 + 附件 + 已读。
4. **P3 日历**：列表 / 新建 / 重命名 / 公开开关 / 事件列表。
5. **P4 联系人**：列表 / 搜索 / 详情。
6. **收尾**：`make verify` 一次，提交。

## 12. 风险与取舍

- **base 变更是最大风险**：所有手写的 `href` 与 `resolve` 调用都要复查；P0 单独提交，便于回滚。
- 会话聚合使用 `conversation_id` 现有语义，不新增索引；单账号邮件量小，`folder_id` 已有索引可用。
- 不做 HTML 清洗（无第三方依赖），改用 iframe 沙箱，避免引入 sanitizer 的体积与漏洞面。
- 前端首次加载增加用户后台页面，但仍是静态预渲染，无运行时 Node。
