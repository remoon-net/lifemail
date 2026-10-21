# EAS 互操作笔记

这份文档记录的是**用真机打出来的协议行为**，不是推测。每一条都对应过具体的故障或客户端怪癖。
改动 `internal/eas` 前先读这里，避免把已经踩过的坑再踩一遍。

客户端基准：MIUI 邮件（实为 WPS / 金山 EAS 栈），另以 iOS 邮件、Outlook for iOS 作为对照。

---

## 1. 真实故障与根因

| 现象 | 根因 | 规则 |
|---|---|---|
| 日历永远不同步 | 日历时间必须是 MS-ASDTIME 紧凑格式 `20261021T093000Z`；发 ISO 会让客户端抛 `NumberFormatException` 并卡死同步锁 | 所有日历时间用 `formatEasCalendarTime`，不要用 `time.RFC3339` |
| Ping 风暴（每秒上百次） | 客户端 folder id 全部失效时 `targets` 为空，旧代码立即回 `status=1` | 空 targets 不能立刻回 1，要等长轮询窗口或返回可用状态 |
| 会议响应状态反了 | MS-ASCAL：3=接受、7=拒绝、9=暂定 | 不要按 1/2/3 映射 |
| 会议回复永远记成「已接受」 | 客户端对所有结果都上报 `MeetingStatus=3` | 解析它自己发出的 iMIP `METHOD:REPLY`，以邮件为准 |
| 附件显示 0 B / 存成 0 字节 | 附件 fetch 的 `<Properties>` 必须用 **itemoperations 命名空间**的 `Range`/`Total`/`Data`（base64 文本），不是 AirSyncBase 的 OPAQUE；缺 `Total` 客户端就按 0 处理 | 见 `internal/eas/extended.go` |
| 服务器搜索永远 0 结果 | 查询树里的日期条件（`LessThan/DateReceived/Value`）被当成关键词拼进全文搜索 | 先按节点类型分流，日期条件单独处理 |
| 正文内联图片不显示 | 两层：`mailparse` 丢弃非文本 inline part；`appdata` 没下发 `<ContentId>` | 两处都要发 |
| 存储的 MIME 里发件人为空 | 只修了内存解析结果，没改写 `raw` | 入库前把解析出的 From 写回 MIME |
| 改单次事件无效 | `<Exceptions>` 位置放错，且写入时被旧值覆盖 | 见第 2 节 |

---

## 2. 日历重复事件（Recurrence / Exceptions）

### 2.1 协议位置

- `<Recurrence>` 是 `<ApplicationData>` 的**直接子元素**
- `<Exceptions>` 也是 `<ApplicationData>` 的直接子元素，**不是** `<Recurrence>` 的子元素。
  MS-ASCAL 的 `Recurrence` 元素表里没有 Exceptions，放错层级会永远解析不到。

### 2.2 字段

```
<Recurrence>
  <RecurrenceType>1</RecurrenceType>            0=日 1=周 2=月(日) 3=月(第N个周X) 4=年(月+日) 5=年(第N个周X)
  <RecurrenceInterval>1</RecurrenceInterval>    间隔，默认 1
  <RecurrenceDayOfWeek>4</RecurrenceDayOfWeek>  位掩码：1=日 2=一 4=二 8=三 16=四 32=五 64=六
  <RecurrenceDayOfMonth>15</RecurrenceDayOfMonth>
  <RecurrenceWeekOfMonth>2</RecurrenceWeekOfMonth>   1..4，5=最后一个
  <RecurrenceMonthOfYear>9</RecurrenceMonthOfYear>   0..11
  <RecurrenceUntil>20261231T000000Z</RecurrenceUntil>
  <RecurrenceOccurrences>10</RecurrenceOccurrences>
</Recurrence>
```

- `Until` 是紧凑格式（与 2.1 的日历时间一致）
- Until 为 0 且 Occurrences 为 0 → 无限重复
- 存储用 `Interval == 0` 作为「不重复」的哨兵，**不要用 `Type == -1`**：
  `Type=0` 是合法的「每天重复」，零值必须是「无规则」

### 2.3 写入时的两个陷阱

1. `Change` 请求**没带** `<Recurrence>` 时必须保留旧规则，否则客户端只改主题就会把重复事件变成单次
2. 带了 `<Recurrence>` 时**不能**用数据库里的旧例外覆盖客户端发来的例外（写反过一次，3 条例外「成功」写入后表里是空的）

### 2.4 服务器不展开实例

EAS 只保存「规则 + 第一次时间」，展开由客户端完成。这一点与 CalDAV/IMAP 不同，也是改动面小的关键。
唯一例外：`MergedFreeBusy` 只统计已存储的实例，不展开重复规则（需要完整 RRULE 求值器，收益不值）。

---

## 3. 会议回复 / 外出回复 / 忙闲

### 3.1 两类客户端行为完全不同

- **标准客户端（iOS / Windows Outlook）**：发 `MeetingResponse`，自己**不写信**。服务端必须代发通知。
  收件人取 `event.OrganizerEmail`；为空或等于自己则跳过。主题 `Accepted/Tentative/Declined: <标题>`，
  加 `Auto-Submitted: auto-replied`，不写「已发送」副本。
- **MIUI / WPS（实测）**：从不发 `MeetingResponse`，自己拼 iMIP `METHOD:REPLY` 邮件，本地直接改状态，
  且无论选什么都上报 `MeetingStatus=3`。服务端必须解析它发来的回复邮件。
- **单次会议响应**：`MeetingResponse` 带 `InstanceId` 时，写进该事件的例外记录（`meeting_status`）。

### 3.2 外出自动回复（Oof）

- 存储：`settings` 表，键 `oof.<userID>`，值 `{"state":0|1|2,"start":unix,"end":unix,"message":"..."}`
- `state`：0 关闭、1 开启、2 定时
- 触发点必须在**投递层**（`deliver.OnInboxDelivered`），EAS、IMAP 提交、`/api/inbound` 三条路径都要走到。
  曾只挂在 `deliver.ToUser` 上，而 inbound 自己插库，导致自动回复静默失效。
- 防循环：不回复 `Auto-Submitted:`、`Precedence: bulk|list|junk`、`List-Id:`、自己、含 `text/calendar` 的邮件；
  同一发件人 24 小时只回一次（进程内 map，重启会重置）

### 3.3 MergedFreeBusy

- 每 30 分钟一个字符：`0` 空闲、`1` 暂定、`2` 忙、`3` 外出
- 事件结束时间正好落在槽边界时不计入下一槽（用 `end-1` 归位）
- 槽位上限 2000（约 41 天），超出截断

---

## 4. 客户端实际行为（别误判成服务端 bug）

- **不用 `MeetingResponse`**：自己拼 iMIP 回复邮件，本地直接改状态
- **「清空垃圾箱」走 `Sync` 的 `<Delete>`**，不是 `ItemOperations/EmptyFolderContents`
- **搜索默认本地**：必须点「继续从服务器搜索」才发 `Search`
- **添加参会人不发 `ResolveRecipients`**：忙闲条在这套客户端上没有入口
- **没有「外出回复」UI**：`Settings/Oof` 只对其他客户端有意义
- **转发带附件前必须先下载附件**：没下载过的附件，转发时只留文件名占位甚至完全不带
- **从不发送**：`GetItemEstimate`、`GetAttachment`、`FolderCreate/FolderUpdate/FolderDelete`

---

## 5. 排查手法

1. 先看 `cmd=X status=… bytes=N`：
   **N 很小** → 服务端没返回内容；**N 正常但客户端不认** → 结构问题
2. 设置 `LIFEMAIL_EAS_DUMP`，请求体（WBXML）会落盘；响应需要临时解码测试还原
3. 判断问题在哪一层：**先查数据库**。表里没有 → 解析层就丢了；表里有 → 协议层
4. 声明的能力必须真的实现：`MS-ASProtocolCommands` 里列了但 `dispatch` 没有的命令，客户端调用会直接 500

---

## 6. 只读日历订阅（iCalendar feed）

EAS 没有「分享日历给外部」的通道，可行方案是一个固定 URL 的 `.ics`。

| 项 | 决定 | 原因 |
|---|---|---|
| 地址 | `GET /calendar/<uuid>.ics` | uuid 挂在日历上，一个用户可有多个日历 |
| 凭据 | uuid 本身；未公开与不存在返回同一个 404 | 不泄漏 uuid 是否存在 |
| 时间 | 全部 UTC（`...Z`） | 免掉 VTIMEZONE 与 Windows 时区名映射 |
| 例外 | 删除 → `EXDATE`；改期 → 独立 `VEVENT` + `RECURRENCE-ID` | |
| `MonthOfYear` | +1 后写 `BYMONTH` | EAS 0 基，iCal 1 基 |
| `WeekOfMonth=5` | `BYSETPOS=-1` | 「最后一个」不是「第 5 个」 |
| `DayOfWeek` | 位掩码 → `SU,MO,...` | EAS 与 iCal 顺序不同 |
| 类型 3/5 | `BYDAY` + `BYSETPOS` | |
| `UNTIL` 与 `COUNT` 同时存在 | 取 `COUNT` | 过期的 `UNTIL` 会静默截断序列 |
| 长行 | 74 字节折行，续行以空格开头 | 不切断 UTF-8 序列 |
| 缓存 | `max-age=300` + `X-PUBLISHED-TTL:PT1H` | |
| 增量 | `SEQUENCE` 每次编辑 +1，`LAST-MODIFIED` 同步写入 | 很多客户端只认 `SEQUENCE` |

`STATUS` 恒为 `CONFIRMED`：库里没有「整个会议已取消」的可靠信号，`HandleIMipReply` 只处理 `METHOD:REPLY`。

---

## 7. 明确不做

远程擦除 `RemoteWipe`（只回空节点）、S/MIME（`ValidateCert`）、会话视图、收件箱规则、
Tasks/Notes、文档库、日历附件（依赖缺失的 `ItemOperations/Store` 上传通道）。

### 联系人头像

不做，原因是流量而不是工作量：`<Picture>` 是 base64 内嵌在联系人属性里，不能分块下载，
线上体积再涨 33%。当前 `contactAppData` 是非空字段全发、`ContactsByFolder` 全量遍历，
100 个联系人 × 50 KB × 1.33 ≈ **6.6 MB / 次全量同步**。

将来要做必须同时满足：

1. 解析请求里的 `<Options><Properties>`，只有列了 `Picture` 才带图，否则只发 `HasPicture`
2. 超过阈值（建议 100 KB）不下发

第 1 条要求「按属性列表裁剪下发」，这套机制目前没做（所有命令都是全量返回）。
