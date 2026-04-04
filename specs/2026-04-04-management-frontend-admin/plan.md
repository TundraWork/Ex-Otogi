# Plan: Management Frontend Admin

## Milestone 1: OpenAPI SDK And API Foundation

**Success criterion:** `web/` 可以通过脚本从 `docs/management.yaml` 生成类型文件，并存在可复用的 API client 与 management 查询封装。

Tasks:

1. 在 `web/package.json` 中新增 API 生成脚本，例如 `gen:api`。
2. 建立 `src/api/generated/` 目录并接入 `openapi-typescript` 输出。
3. 实现基于 `openapi-fetch` 的 client 工厂，支持：
   - `baseUrl`
   - bearer token 注入
   - 统一错误处理
4. 实现 management API 语义化封装：
   - `getOverview`
   - `getEvents`
   - `getEventById`
   - `getTrace`
   - `getArtifact`
   - `getSnapshots`
5. 约定生成代码与手写代码的边界，避免后续手改生成文件。

Verification:

1. 运行生成脚本，确认类型文件成功产出且可被 TypeScript 引用。
2. 执行前端构建或类型检查，确认 API 封装层通过编译。
3. 手工检查 `Authorization` 注入路径只存在一个统一入口。

## Milestone 2: Auth Session And Route Guard

**Success criterion:** 后台存在独立登录页，用户可通过输入 token 建立本地会话，未登录状态无法访问受保护路由。

Tasks:

1. 建立 `authStore`，支持：
   - 保存 token
   - 从 `localStorage` 恢复 token
   - logout 清理
2. 实现 `/login` 页面：
   - token 输入
   - 提交状态
   - 错误提示
3. 登录流程通过请求 `/panel/overview` 验证 token。
4. 配置路由守卫：
   - 未登录时跳转 `/login`
   - 已登录访问 `/login` 时跳到 `/overview`
5. 统一处理 401：
   - 清理会话
   - 返回登录页

Verification:

1. 无 token 进入任一后台路由会被重定向到登录页。
2. 输入有效 token 后可进入后台首页。
3. 输入无效 token 时停留在登录页并显示明确错误。
4. 刷新页面后会话可恢复。

## Milestone 3: App Shell And Navigation

**Success criterion:** 登录后进入统一后台壳，用户可以通过导航在核心页面间切换，并能执行退出登录。

Tasks:

1. 设计后台主布局：
   - 顶部或侧边导航
   - 页面内容区域
   - 全局反馈区域
2. 建立路由信息与导航元数据。
3. 将 Overview、Events、Snapshots、Trace Lookup 挂入导航。
4. 在布局中展示当前会话状态与 logout 操作。
5. 处理移动端与窄屏下的导航折叠。

Verification:

1. 登录后所有后台页面共享同一套布局。
2. 导航切换不丢失会话。
3. logout 后立即回到登录页并失去受保护页面访问权。

## Milestone 4: Overview Dashboard

**Success criterion:** `/overview` 能展示 overview API 的关键摘要信息，并支持刷新与基本跳转。

Tasks:

1. 对接 `GET /panel/overview`。
2. 实现摘要卡片：
   - WindowStartID
   - WindowEndID
   - TotalEvents
   - RecentErrorCount
   - RecentTraceCount
3. 实现两组结构化展示：
   - EventCountsByCategory
   - ActiveInflightByComponent
4. 增加加载、空态、错误态与手动刷新。
5. 预留从统计项跳转到 Events 页的入口。

Verification:

1. 页面正确渲染 overview 返回的全部核心字段。
2. 手动刷新后数据可以重新拉取。
3. 接口错误时页面有可重试反馈，不会直接白屏。

## Milestone 5: Events Explorer

**Success criterion:** `/events` 支持筛选、轮询、游标管理和事件详情查看，能够成为主要调试工作台。

Tasks:

1. 对接 `GET /panel/events` 并建立页面查询模型。
2. 实现筛选栏，覆盖 API 全部查询参数。
3. 实现事件列表，至少展示：
   - ID
   - OccurredAt
   - Category
   - Kind
   - Level
   - Module
   - Component
   - TraceID
   - Summary
4. 实现自动轮询开关与轮询间隔策略。
5. 基于 `LastID` 实现增量加载。
6. 正确处理：
   - `CursorResetRequired`
   - `HasMore`
7. 点击事件打开详情视图，并对接 `GET /panel/events/{id}`。
8. 在事件详情中支持跳转：
   - trace
   - artifact

Verification:

1. 更改筛选条件后能够重新加载对应结果。
2. 开启轮询后新事件可以追加进入列表。
3. `CursorResetRequired=true` 时有清晰提示且可重置。
4. 事件详情能显示基础字段、payload 与关联资源入口。

## Milestone 6: Trace And Artifact Workflows

**Success criterion:** 用户可以通过 trace ID 查看 trace 时间线，并可以从事件上下文进入 artifact 原文详情。

Tasks:

1. 实现 `/traces` 查询页：
   - 输入 trace ID
   - 可选输入 limit
2. 实现 `/traces/:traceId` 详情页，对接 `GET /panel/traces/{trace_id}`。
3. 将 trace 结果按事件序列展示，并支持打开 event detail。
4. 实现 `/artifacts/:artifactId` 页面，对接 `GET /panel/artifacts/{id}`。
5. Artifact 页面完整展示：
   - ID
   - EventID
   - Kind
   - CreatedAt
   - Content
6. 处理 trace/artifact 的空态与 404。

Verification:

1. 通过手工输入 trace ID 能查询并渲染时间线。
2. 从 event detail 可以跳转到对应 trace 或 artifact。
3. artifact 内容不截断、可滚动、可复制。

## Milestone 7: Snapshots Explorer

**Success criterion:** `/snapshots` 支持按条件过滤当前快照，并可查看每条 snapshot 的完整 payload。

Tasks:

1. 对接 `GET /panel/snapshots`。
2. 实现筛选栏：
   - namespace
   - key
   - module
3. 实现 snapshot 列表展示：
   - Namespace
   - Key
   - Module
   - UpdatedAt
   - Summary
   - PayloadType
4. 提供 payload 查看能力，使用 JSON 展示兜底。
5. 支持加载、空态、错误态。

Verification:

1. 三个筛选条件都能正确参与查询。
2. 列表能展示 summary 与基础元信息。
3. payload 在复杂结构下仍可读。

## Milestone 8: Hardening, Build, And UX Polish

**Success criterion:** 后台具备稳定的错误反馈、基础响应式体验、清晰的空态/加载态，并通过构建校验。

Tasks:

1. 收敛统一错误展示策略：
   - 页面内错误
   - toast
   - 401 全局跳转
2. 统一时间格式化、JSON 展示、状态标签视觉。
3. 补充响应式布局处理，确保窄屏下仍可导航与查看详情。
4. 清理重复请求与明显的状态竞争问题。
5. 运行前端构建检查。

Verification:

1. 所有核心页面都具备 loading / empty / error 状态。
2. 受保护路由与 401 处理路径一致。
3. `pnpm build` 成功。

## Suggested Implementation Order

按以下顺序执行可降低返工：

1. Milestone 1
2. Milestone 2
3. Milestone 3
4. Milestone 4
5. Milestone 5
6. Milestone 6
7. Milestone 7
8. Milestone 8

## Scope Notes

本计划刻意不包含以下能力，因为当前 API 尚不支持或收益不足：

- trace 全量列表
- artifact 全量列表
- 写操作或控制命令
- websocket / SSE 实时推送
- 基于后端枚举的固定筛选下拉

这些能力如需支持，应先扩展 management API，再更新前端规格。
