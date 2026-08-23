# API Notes: Management Frontend Admin

## Source

前端 API 接入以 `docs/management.yaml` 为唯一生成源，运行时认证约定补充来自现有 management 后端规格与实现。

## Auth Contract

所有请求都必须带：

- `Authorization: Bearer <token>`

前端不依赖登录接口；token 由登录页采集并保存到本地会话。

## Base URL Contract

建议：

- 开发时通过 `VITE_MANAGEMENT_API_BASE_URL` 配置
- 默认回退到同源 `/`

这样生成类型与运行时配置解耦，不需要在生成代码中写死地址。

## Generated Types And Runtime Layer

建议生成：

- `src/api/generated/management.ts`

建议手写封装：

- `src/api/client.ts`
- `src/api/management.ts`

职责划分：

- 生成文件：仅保存 OpenAPI 类型
- `client.ts`：创建 `openapi-fetch` client，并注入 bearer token
- `management.ts`：输出业务语义化方法

## Endpoint To UI Mapping

### `GET /panel/overview`

UI 用途：

- Overview 首页
- 登录后 token 有效性探测

需要处理：

- `200` 成功
- `401` token 无效

### `GET /panel/events`

UI 用途：

- Events 主列表
- 自动轮询
- 筛选与增量游标

查询参数：

- `after_id`
- `limit`
- `category`
- `kind`
- `trace_id`
- `conversation_id`
- `module`
- `level`

关键响应字段：

- `Items`
- `LastID`
- `WindowStartID`
- `WindowEndID`
- `CursorResetRequired`
- `HasMore`

前端含义：

- `LastID` 用于后续增量轮询
- `CursorResetRequired=true` 时要求丢弃本地 cursor 并重载
- `HasMore=true` 时允许继续翻取

### `GET /panel/events/{id}`

UI 用途：

- Events 页详情抽屉
- Trace 页点击单条 event 后加载详情

注意：

- `404` 在本系统里很可能表示该 event 已被滑动窗口淘汰，而不是用户输错 ID

### `GET /panel/traces/{trace_id}`

UI 用途：

- Trace Lookup 页
- 深链接 trace 详情页

限制：

- 只能按 trace ID 查询
- 不能反查 trace 列表

### `GET /panel/artifacts/{id}`

UI 用途：

- Artifact 详情页

限制：

- 没有列表接口
- 必须从 `event.artifact_ids` 或手工输入 ID 进入

### `GET /panel/snapshots`

UI 用途：

- Snapshots 列表页

查询参数：

- `namespace`
- `key`
- `module`

## Derived UI Constraints

根据 API 形态，前端应该遵守以下边界：

- 不做“Trace 列表页”，而做“Trace 查询页”
- 不做“Artifact 列表页”，而做“Artifact 详情页”
- `payload` 与 `snapshot payload` 统一按未知 JSON 渲染
- 事件筛选项初期采用自由输入，避免错误假设后端枚举集合

## Suggested API Wrapper Surface

建议对页面暴露的最小接口：

- `validateToken()`
- `getOverview()`
- `getEvents(params)`
- `getEventById(id)`
- `getTrace(traceId, params?)`
- `getArtifact(id)`
- `getSnapshots(params?)`

其中：

- `validateToken()` 可直接复用 `getOverview()`
- 所有函数都返回规范化后的结果对象或抛出统一错误

## Error Handling Contract

前端统一解释：

- `401 Unauthorized`
  - 清空 token
  - 跳转登录页
- `404 Not Found`
  - 展示“资源不存在或已被淘汰”
- `400 Bad Request`
  - 保留用户输入并展示错误信息
- 其他错误
  - 统一 toast + 页面内可重试提示
