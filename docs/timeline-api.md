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
- `cursor` 为上一页的 `next_cursor`，不可解读或改写；下一页必须保持相同 `start` 和 `end`。
- 始终返回 JSON；`format=json` 可省略。没有 `offset`、联系人筛选或逆序参数。

```sh
curl --fail --get 'http://127.0.0.1:5030/api/v1/timeline' \
  --data-urlencode 'start=2026-11-01T09:00:00' \
  --data-urlencode 'end=2026-11-01T12:30:00' \
  --data-urlencode 'limit=500'
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

当 `has_more=true` 时，将 `next_cursor` 作为下一次请求的 `cursor`，其余范围保持不变，直到 `has_more=false`。游标支持同秒跨表分页，保留同秒重复发送的消息，不按正文去重。全局只保留 `limit+1` 条候选消息，防止大范围请求把全部历史载入内存；但每页仍需查询所有消息表。

所有源连接为 SQLite 只读，读事务按数据库建立。任何消息库或表读取失败都返回错误，不把缺失源静默当成空表；身份查询异常仅影响名称映射，记录在 `identity_warnings` 中。该 API 不提供跨数据库、跨 HTTP 页的事务快照：分页过程中导入/修改的历史可能位于已读游标之前，增量轮询应固定区间上限，并重叠回查、按 `(source, localId)` 合并。

## 测试

当前仓库保留了模块名和旧 import 路径的本地替换，内部包测试使用已有 import 路径：

```sh
GOTOOLCHAIN=go1.24.0 CGO_ENABLED=1 go test \
  github.com/sjzar/chatlog/internal/wechatdb/timeline \
  github.com/sjzar/chatlog/internal/chatlog/http \
  ./cmd/chatlog-readonly
```
