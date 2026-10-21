# AGENTS.md

给自动化助手和未来维护者的硬性约定。**动手前先读这一页。**

本项目的 EAS/IMAP 行为来自真机验证（见 `docs/eas-notes.md`），数据层与前端是重写的。
“不兼容重写前的旧库”**只**意味着不需要迁移 PocketBase / v0.3.x 的历史数据，
**不**意味着以后可以跳过迁移。

---

## 1. 数据库迁移（最容易误读的地方）

schema 唯一来源是 `internal/store/migrations/NNNN_name.<dialect>.sql`，由 `migrate.go`
按版本号执行并记录 checksum。

必须遵守：

- **已发布的迁移文件只读。** 不要修改 `0001_init.*.sql` 或任何已提交版本的 SQL；
  checksum 不一致会让服务拒绝启动。需要改动就新增 `0002_...`、`0003_...`。
- **两种方言同时改。** 每个版本必须同时提供 `.sqlite.sql` 和 `.postgres.sql`，
  表、列、索引、外键保持一致；`schema_parity_test.go` 会静态比对。
- **破坏性变更分三个版本**，不要一次做完：
  1. 新增列（可空或带默认值），代码同时读写新旧字段
  2. 回填数据
  3. 下一版删除旧列
- **只前向，不支持降级。** 回滚 = 恢复备份，不要把 “down migration” 加进来。
- 新迁移写完后至少验证：
  ```bash
  go test ./internal/store/
  LIFEMAIL_DB_DSN=/tmp/scratch.db go run ./cmd/lifemail db status   # 或 make build 后用 ./lifemail
  ```
- PostgreSQL 需要真实数据库验证时（**只能指向临时库**）：
  ```bash
  LIFEMAIL_TEST_PG_DSN='postgres://user:pass@localhost:5432/lifemail_test?sslmode=disable' \
    go test -tags integration ./internal/store/ -run Postgres
  ```

## 2. 不要随手改的对外契约

这些字段/规则已经是客户端或外部订阅方依赖的：

| 对象 | 约束 |
|---|---|
| `messages.id` | 就是 IMAP UID；不能重编号，不能复用 |
| `folders.uid_validity` | 创建后不变；变了会让 IMAP 客户端全量重同步 |
| `folders.calendar_uuid` | 公开订阅 URL 的凭据；变了外部订阅全断 |
| blob key（`blobstore.Key`）与 `messages.raw_path` / `attachments.path` / `outbox.raw_path` | 改规则会产生孤儿文件，必须配数据迁移 |
| `changes` / `sync_states` / `sync_items` | EAS 增量同步的基础；改语义等于强制所有设备全量 |
| `settings` 的 key（`mail.domains`、`smtp.*`、`oof.<id>`） | 改名等于配置丢失 |
| `aliases` | 地址统一小写存储；主地址唯一性由部分索引保证 |

## 3. 构建与检查

```bash
make verify       # 收尾时跑一次：gofmt + vet + svelte-check + go test ./...
make build        # 前端构建 + 静态编译（只在需要产物时跑）
```

**检查只跑一遍。** 改动完成后跑一次 `make verify`，通过就直接提交；
不要为了“保险”在提交前再跑一次。只有在这之后又改了文件
（包括编辑器保存触发的格式化）才需要重跑。

迭代过程中不必每次全量，先跑受影响的包：

```bash
go test ./internal/store/     # 单个包
cd web && npm run check       # 只做前端类型检查
```

## 4. 协议层

- 修改 `internal/eas` 前先读 `docs/eas-notes.md`，那里记录了真机踩坑（MS-ASDTIME 紧凑格式、
  Exceptions 的位置、MeetingStatus 取值、ItemOperations 附件命名空间等）。
- 这些行为有测试兜底，**先让现有测试保持绿，再改代码**；新增行为补对应测试。
- 不要声明未实现的 EAS 命令：`MS-ASProtocolCommands` 里列出的命令必须真的在 `dispatch` 里。

## 5. 前端

- SvelteKit **3**：`#lib` 而不是 `$lib`；`tsconfig.json` 必须 `extends: "$app/tsconfig"`；
  `$app/paths` 只导出 `resolve` / `asset` / `base`；`$app/state` 提供 `page`。
- 类型检查用 `cd web && npm run check`，不要靠 `vite build` 代替。
- 页面全部预渲染（`ssr = false`），数据通过 `/api/admin/*` 在客户端拉取。
- UI 文案用中文，新增组件放 `web/src/lib/components/`，不要回到单文件大页面。

## 6. 提交

- 提交信息用 conventional commits（`feat:` / `fix:` / `chore:` / `docs:`）。
- 不要提交 `.env`、`lifemail` 二进制、`web/build/*`、`web/node_modules`、`data/` 内的运行时文件。
