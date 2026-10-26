# Changelog

本项目的所有重要变更都记录在这里。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循
[语义化版本](https://semver.org/lang/zh-CN/)。

## [0.3.5] - 2026-10-26

### Added

- 邮箱页新增 SSE 实时推送：`GET /api/user/events` 推送新邮件，列表头部显示连接状态
  （绿点），收到推送后弹出「新邮件 · 发件人：主题」并自动刷新文件夹与会话，无需手动
  点刷新
- `internal/api` 新增进程内 `EventHub`：按账号扇出、慢客户端丢弃、单账号连接数上限、
  停机时主动断开；触发点在投递层 `deliver.OnInboxDelivered`，inbound、EAS、SMTP
  本地投递都能推到网页端
- 前端新增 `mail-events.svelte.ts`：`EventSource` 封装，非 200 关闭时指数退避重连，
  重连后重新加载数据以补齐断线期间漏掉的推送

### Fixed

- 请求日志中间件包装后的 `ResponseWriter` 丢失 `http.Flusher`，SSE 会被整段缓冲到请求
  结束才输出；改用 `http.NewResponseController` 并在包装器上实现 `Unwrap()`

## [0.3.4] - 2026-10-26

### Added

- 写信与回复的收件人改为从通讯录选择：新增 `ContactPicker` 组件，已选地址以 chip
  展示，可单个移除；联系人一次性加载（上限 500）后在浏览器内按姓名、邮箱、公司
  实时过滤，支持 `↑` / `↓` / `Enter` / `Esc` 与退格删除最后一个
- 输入不在通讯录里的完整邮箱时提供「添加「xxx」为新联系人」：在弹窗里填写姓名与
  邮箱，保存后自动加入对应字段并选中
- 收件人摘要改为显示联系人姓名（如「张三 等 2 人」），不再只显示裸地址

### Changed

- 收件人对话框的收件人 / 抄送 / 密送由逗号分隔的文本框改为联系人选择器；回复模式
  仍保留「仅发件人 / 全部回复」按钮

### Fixed

- 弹窗内容高度变化时（例如候选列表展开或收起、弹窗重新居中），点击标题或描述会被
  误判为点击遮罩而关闭整个弹窗；现在只有按下位置就在遮罩上时才会关闭

## [0.3.3] - 2026-10-26

### Fixed

- 对方回复缺少 `In-Reply-To` / `References`（MIUI 邮件等客户端只在主题前加 `Re:`）
  时不再自成一个新会话：按「规范化主题 + 参与者有交集」并入最老的匹配会话，收件箱
  对话里「我方先发的那封」不会再消失。入站投递与本地/手机发信的已发送副本都适用

### Added

- `mailparse.NormalizedSubject`、`mailparse.IsReplySubject`、`store.ThreadForReply`
  与 `deliver.ConversationID`，把「无线程头的回复归属」收敛到一处，供所有投递路径复用

## [0.3.2] - 2026-10-26

### Changed

- 对话底部的回复/写信输入框默认高度从 2 行（约 44px）提高到 5 行（最小 112px），
  写长邮件时不必先手动拉伸；自动增高与 40vh 上限保持不变

## [0.3.0] - 2026-10-26

### Added

- **写新邮件**：`POST /api/user/mail/send` 支持收件人、抄送、密送，地址用逗号或空格
  分隔，自动去重，最多 50 个收件人；先写已发送副本再投递，本地走内部投递、外域走
  SMTP，失败入发件队列并返回 `queued: true`
- 写信与回复共用同一个 `draft` 状态与同一块编辑区（对话底部），不再有独立的弹窗；
  收件人/抄送/密送通过工具栏上的「收件人」对话框编辑，回复时默认填入原信地址
- 写信草稿保存在浏览器 `localStorage`，离开编辑区后点「写邮件」可恢复上次内容

### Changed

- 会话列表不再插入「草稿」条目，只展示真实会话
- 回复/写信输入框随内容自动增高，最高 40vh，超出后内部滚动

## [0.2.1] - 2026-10-26

### Changed

- 回复框快捷键改为 `Ctrl/⌘+Enter` 发送、`Enter` 换行，避免写多段正文时误发
  （邮件发出后无法撤回）

## [0.2.0] - 2026-10-26

### Added

- **用户后台 `/app/`**（账号所有者用邮箱密码登录）
  - 邮件按会话展示，收件箱与已发送合并成同一个会话；主题取会话首封
  - 阅读窗格渲染 HTML（沙箱 iframe）或纯文本，附件可直接下载，进入会话即标记已读
  - 底部聊天式回复栏：`Enter` 发送、`Shift+Enter` 换行；可编辑回复对象（默认原发件人 +
    原信 To/Cc，自动剔除自己的地址与别名）
  - `POST /api/user/mail/reply` 生成带 `In-Reply-To`/`References` 的纯文本回复，
    先写已发送副本再投递，本地走内部投递、外域走 SMTP，失败入发件队列
  - `GET /api/user/mail/search` 跨文件夹搜索主题、发件人、收件人与正文；列表上的搜索
    框默认收起，回车或点「搜索」才发起一次请求
  - 日历：创建/重命名/公开订阅，事件的新建、编辑、删除（重复事件只读，提示在客户端编辑）
  - 联系人：跨文件夹查看与搜索，新建、编辑、删除；电话/邮箱按 MS-ASCNTC 允许的类型
    动态增删

### Changed

- 前端不再 `go:embed` 进二进制：改为启动时读取 `LIFEMAIL_WEB_DIR`（默认 `web/build`），
  替换目录即可更新界面；目录缺失只影响 `/app/` 与 `/admin/`，API 照常工作
- 镜像改为 `COPY web/build /srv/lifemail/web` 并设置 `LIFEMAIL_WEB_DIR`；
  `make build` 仍先构建前端，新增 `make gobuild` 只重编后端
- `vite dev` / `vite preview` 把 `/api` 反代到 Go 服务（`LIFEMAIL_DEV_API`，默认
  `127.0.0.1:8080`），前端可热更新；严格 CSP 只在 `vite build` 时注入

### Fixed

- 会话默认回复对象为空时（原信缺少地址）前端取不到收件人列表，导致会话打不开
- 回复后会话头部的邮件封数不刷新

## [0.1.1] - 2026-10-24

### Fixed

- 控制台「设置 → 发送测试邮件」：按钮与输入框底部对齐，提示文字移到整行下方

## [0.1.0] - 2026-10-24

### Added

- **收信**
  - 按域名的兜底转发（`mail.catchall`）：本地不存在的收件人同步转发到外部地址，
    控制台「设置 → 兜底转发」可维护
  - 同一个请求内任一收件人无法解析时整封拒绝，避免重试造成部分收件人重复收信
- **管理控制台**
  - 「设置」页新增「发送测试邮件」：`POST /api/admin/smtp/test` 用已保存的 SMTP
    配置同步发送，失败时直接返回错误信息

### Changed

- `POST /api/inbound` 对未知收件人的处理：没配兜底时返回 `550`（Worker 永久退信），
  转发失败返回 `502`，不再静默标记为 `skipped`
- `deploy/cloudflare-worker.js` 遇到 `550` 调用 `message.setReject`，其余错误继续抛出让 Cloudflare 重试

## [0.0.1] - 2026-10-22

首个可部署版本：单二进制同时提供 Exchange ActiveSync、IMAPS、SMTPS、收信 API
与管理控制台。

### Added

- **EAS（ActiveSync）**
  - WAP Binary XML 编解码，协议版本 14.1
  - `Sync`、`SendMail`、`SmartReply`、`SmartForward`、`ItemOperations`
    （正文/附件/分块下载）、`Search`、`Settings`、`Oof`、`Ping`、
    `MeetingResponse`、`GetAttachment`、`Autodiscover`
  - 邮件、联系人、日历集合；重复事件与单次例外；iMIP 会议回复
  - 按设备维护 `sync_states` / `sync_items` 与单调递增的 `changes` 变更流
- **邮件协议**
  - IMAPS / SMTPS，按域名证书、SNI 选择
  - IMAP：`LIST`、`SELECT`、`FETCH`、`STORE`、`SEARCH`、`MOVE`、`APPEND`、`IDLE`
  - SMTP 提交：本地投递 + 外部地址经 Cloudflare Email Sending 转发
- **收信**
  - `POST /api/inbound` 接收 Cloudflare Email Routing Worker 转发的原始 MIME
  - 别名、plus 地址、`Message-ID` 去重、收件人自动识别
- **管理控制台 `/admin/`**
  - Token / 会话登录，账号、别名、域名与证书、发件队列、设备、日历订阅
  - 运行时设置存数据库：`mail.domains`、`smtp.*`，改完即时生效
- **日历**
  - EAS 日历同步（UTC 存储）
  - 只读 iCalendar 订阅：`/calendar/<uuid>.ics`
- **存储**
  - SQLite（默认）与 PostgreSQL，二选一由 DSN 决定
  - 版本化迁移 `internal/store/migrations/NNNN_name.<dialect>.sql`，启动时自动应用
  - blob 目录保存 MIME 原文、附件正文与队列邮件，可挂载 rclone 等远程文件系统
  - 外发队列 `outbox` 带退避重试与失败状态
- **命令行**
  - `lifemail serve`、`lifemail db status|migrate`、
    `lifemail user add|passwd|list`、`lifemail version`
- **部署**
  - 前端构建后嵌入二进制；提供 `Dockerfile`、`Caddyfile` 与 Cloudflare Worker
- **安全**
  - bcrypt 密码；管理端 Token/会话 Cookie；登录失败限流按账号 + IP 计数

### Notes

- 数据库迁移只前向、不支持降级；已发布的迁移文件带 checksum，不可修改，
  变更需新增版本。
- **不兼容** PocketBase / v0.3.x 的历史数据，不提供导入工具。
- 必须部署在 HTTPS 之后：EAS 使用 HTTP Basic Auth，凭据只能经 TLS 传输。
- blob 目录需与数据库一起备份；目录丢失等同于邮件内容丢失。

[0.0.1]: https://github.com/remoon-net/lifemail/releases/tag/v0.0.1
