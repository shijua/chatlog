# 全账号时间段 API（北京时间）

```text
GET /api/v1/timeline?start=2026-10-31&end=2026-11-02&limit=500
```

不需要 `talker`。当前支持本机 **macOS 微信 3.x** 的已解密消息库，直接枚举所有 `msg_*.db` 中的 `Chat_<32位哈希>` 消息表，而非只查询会话列表。隐藏会话、没有联系人映射的表以及不同库内同名表均纳入查询。辅助删除表、尚未同步/解密的源数据不在范围内。其他平台或版本返回 501。

## 时间和分页

- `start`、`end` 必填，采用 **包含开始、不包含结束** 的 `[start, end)` 区间。
- 日期 `YYYY-MM-DD` 解释为北京时间当日 00:00:00。查询 10 月 31 日整天应使用 `start=2026-10-31&end=2026-11-01`。
- 支持任意精确到秒的时间段：`2026-11-01T09:00:00` 到 `2026-11-01T12:30:00`。不带时区时按北京时间；RFC3339 显式偏移会换算为北京时间。返回时间含 `+08:00`。不支持小数秒。
- `limit` 默认 500，允许 1–5000。
- `cursor` 为上一页的 `next_cursor`，不可解读或改写；下一页必须保持相同时间范围及排除条件，改变条件请从第一页重新查询。无排除条件的旧游标仍可使用。
- `exclude_talker`：用逗号分隔的聊天对象 ID，排除整个私聊或群聊。例如 `wxid_a,123456@chatroom`。直接按 ID 的表哈希匹配，即使对象未能从元数据解析也能排除；不同库中同一对象的表都会排除。
- `exclude_sender`：用逗号分隔的发送者 ID，仅排除该人发送的消息，包括能识别发送者的群消息；不会同时排除你发给这个人的私聊回复。若需要排除整个私聊，请用 `exclude_talker`。发送者未知的记录不会被猜测过滤。
- 排除参数只接受精确 ID，不按昵称或备注模糊匹配；每类最多 100 个不同 ID。空白会去掉、重复 ID 会合并，列表顺序不影响游标。使用一个逗号分隔参数，不要重复传同名参数。
- 始终返回 JSON；`format=json` 可省略。没有 `offset` 或逆序参数。过滤发生在分页之前，不会因为被排除的记录占满一页而提前停止。

```sh
curl --fail --get 'http://127.0.0.1:5030/api/v1/timeline' \
  --data-urlencode 'start=2026-11-01T09:00:00' \
  --data-urlencode 'end=2026-11-01T12:30:00' \
  --data-urlencode 'limit=500'
```

排除整段私聊和某个群成员的发言：

```sh
curl --fail --get 'http://127.0.0.1:5030/api/v1/timeline' \
  --data-urlencode 'start=2026-11-01' \
  --data-urlencode 'end=2026-11-02' \
  --data-urlencode 'exclude_talker=wxid_a,wxid_b' \
  --data-urlencode 'exclude_sender=wxid_c'
```

远程将地址替换为 Mac 的 Tailscale 地址。原后端保持回环监听，通过只读代理访问；代理的指定 VM 来源限制仍然适用。

## 返回结果

```json
{
  "items": [],
  "start": "2026-11-01T09:00:00+08:00",
  "end": "2026-11-01T12:30:00+08:00",
  "timezone": "Asia/Shanghai",
  "has_more": false,
  "next_cursor": "",
  "tables_scanned": 935,
  "tables_excluded": 0,
  "unresolved_items": 0,
  "identity_warnings": 0
}
```

`items` 按 `(time, source, localId)` 从旧到新稳定排序。每条含现有消息字段（`time`、`talker`、`talkerName`、`sender`、`senderName`、`isSelf`、`type`、`content` 等），以及：

- `source`：数据库相对路径和表名，用于区分不同库。
- `localId`：该表的 `mesLocalID`；`(source, localId)` 是去重标识，不使用微信 3.x 中可能为 0 的 `seq`。
- `serverId`：`mesSvrID` 字符串，避免大整数在 JavaScript 中丢精度。
- `tableHash`：表名中的哈希。
- `identityResolved`：是否能从联系人、群成员、群或会话元数据映射聊天对象。未识别时 `talker` 为空，记录仍返回；不能把它误认为文件传输助手或普通私聊。
- `rawContent`：未经消息类型解析的原始正文，供解析失败或对象未知时使用。不会下载附件或转写语音。

当 `has_more=true` 时，将 `next_cursor` 作为下一次请求的 `cursor`，保持时间范围及排除条件不变，直到 `has_more=false`。游标支持同秒跨表分页，保留同秒重复发送的消息，不按正文去重。全局只保留 `limit+1` 条候选消息，防止大范围请求把全部历史载入内存；每页仍需枚举所有消息表。`tables_excluded` 是按聊天对象排除的表数，`tables_scanned` 是其余实际查询的表数。使用发送者过滤时，单表可能需要读取更多行才能找齐有效候选消息。

所有源连接为 SQLite 只读，读事务按数据库建立。任何消息库或表读取失败都返回错误，不把缺失源静默当成空表；身份查询异常仅影响名称映射，记录在 `identity_warnings` 中。该 API 不提供跨数据库、跨 HTTP 页的事务快照：分页过程中导入/修改的历史可能位于已读游标之前，增量轮询应固定区间上限，并重叠回查、按 `(source, localId)` 合并。

## 测试

当前仓库保留了模块名和旧 import 路径的本地替换，内部包测试使用已有 import 路径：

```sh
GOTOOLCHAIN=go1.24.0 CGO_ENABLED=1 go test \
  github.com/sjzar/chatlog/internal/wechatdb/timeline \
  github.com/sjzar/chatlog/internal/chatlog/http \
  ./cmd/chatlog-readonly
```
