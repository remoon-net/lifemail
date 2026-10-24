# Changelog

本项目的所有重要变更都记录在这里。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循
[语义化版本](https://semver.org/lang/zh-CN/)。

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
