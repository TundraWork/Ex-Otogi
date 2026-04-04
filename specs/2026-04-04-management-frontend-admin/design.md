# Design: Management Frontend Admin

## Overview

本规格定义 `web/` 管理后台的前端方案。该后台直接消费 `docs/management.yaml` 描述的只读 Management API，并严格基于当前 API 能力规划页面，不假设任何额外后端接口。

当前前端基座已经具备：

- Vue 3
- Vue Router
- Pinia
- PrimeVue
- Tailwind CSS 4
- `openapi-typescript`
- `openapi-fetch`

因此本次前端设计的重点不是重新搭骨架，而是明确：

- 如何从 OpenAPI 生成稳定的类型与 SDK
- 如何实现 token 登录与鉴权注入
- 如何围绕现有 API 组织导航与页面信息架构
- 如何处理轮询、详情查看、错误反馈与空状态

## Goals

- 使用 `docs/management.yaml` 生成类型安全的前端 API 接入层
- 提供一个 token 登录页，允许用户输入 bearer token 进入后台
- 登录后提供统一导航和受保护页面
- 覆盖当前 API 能支撑的主要查询工作流：
  - overview 总览
  - events 事件流浏览
  - event 详情查看
  - trace 追踪查看
  - artifact 内容查看
  - snapshots 当前状态查看
- 保持后续扩展空间，方便继续接更多 management API

## Non-Goals

- 不在本规格中实现后端接口
- 不引入 WebSocket 或 SSE，沿用轮询模型
- 不设计用户账号体系；“登录”仅指保存 bearer token
- 不设计写操作、控制台命令或配置修改能力

## API Capability Mapping

根据 `docs/management.yaml`，当前后端暴露的页面能力如下：

- `GET /panel/overview`
  - 适合做总览首页，展示窗口范围、总事件数、分类计数、错误数、活跃组件、近期 trace 数
- `GET /panel/events`
  - 适合做事件列表页，支持轮询与筛选
- `GET /panel/events/{id}`
  - 适合做事件详情页或右侧详情面板
- `GET /panel/traces/{trace_id}`
  - 适合做 trace 详情页，但不适合做 trace 列表页，因为 API 没有“枚举 trace”接口
- `GET /panel/artifacts/{id}`
  - 适合从事件详情中跳转查看 artifact 内容，但不适合做 artifact 列表页，因为 API 没有列表接口
- `GET /panel/snapshots`
  - 适合做 snapshots 列表页，支持按 namespace/key/module 过滤

因此，前端后台应该包含“页面 + 详情流”两类能力：

- 一级导航页面：
  - 登录页
  - Overview 总览页
  - Events 事件页
  - Snapshots 快照页
  - Trace Lookup 追踪页
- 从上下文打开的详情视图：
  - Event Detail
  - Artifact Detail

## Frontend Architecture

推荐将 `web/src` 划分为以下模块：

- `app/`
  - 应用级初始化、provider 注册、全局错误提示
- `router/`
  - 路由定义、导航元信息、鉴权守卫
- `stores/`
  - Pinia 状态管理
- `api/`
  - OpenAPI 生成物、fetch client、请求封装
- `layouts/`
  - 登录外壳、后台外壳
- `views/`
  - 各页面路由组件
- `components/`
  - 共享 UI 组件，如导航栏、统计卡片、筛选栏、JSON 查看器、状态标签
- `features/`
  - 按业务域组织的组件与数据逻辑，例如 `events/`, `snapshots/`, `overview/`
- `utils/`
  - 时间格式化、query 同步、错误解析

这个结构保持页面与数据能力解耦，后续接更多 management API 时不需要重做根目录。

## OpenAPI SDK Strategy

### Type Generation

使用 `openapi-typescript` 从 `docs/management.yaml` 生成类型：

- 输入：`../docs/management.yaml`
- 输出建议：`src/api/generated/management.ts`

推荐新增脚本：

- `pnpm gen:api`

示例职责：

- 生成 `paths`、`components` 等 TypeScript 类型
- 不手写重复 DTO
- 将 API 变更集中在生成文件中

### Runtime Client

运行时请求层使用 `openapi-fetch`：

- 基于生成的 `paths` 创建 client
- 统一设置 `baseUrl`
- 统一注入 `Authorization: Bearer <token>`
- 统一处理 401、网络错误、problem+json 错误体

建议封装：

- `src/api/client.ts`
- `src/api/management.ts`

封装后页面不直接拼 URL，而是调用语义化函数：

- `getOverview()`
- `getEvents(params)`
- `getEventById(id)`
- `getTrace(traceId, params)`
- `getArtifact(id)`
- `getSnapshots(params)`

## Auth And Session Model

当前 API 没有登录接口，因此“登录页”本质是本地会话初始化：

1. 用户输入 token
2. 前端将 token 保存到本地存储
3. 跳转到后台首页
4. 所有请求自动附带 `Authorization: Bearer <token>`

建议：

- 使用 Pinia 维护 `auth` store
- 使用 `localStorage` 持久化 token，保证刷新后仍可用
- 提供 `logout`，清空 token 并跳回登录页
- 路由守卫校验 token 是否存在
- 若接口返回 401：
  - 清空本地 token
  - 弹出“鉴权失效”提示
  - 重定向回登录页

由于没有 token 校验专用接口，登录页提交后不必额外走“验证 token”请求；可以直接跳转并由后续页面请求自然验证。为了更好的体验，也可以在提交后先请求一次 `/panel/overview`，成功再进入后台，失败则停留在登录页并展示错误信息。

推荐采用“登录时用 `/panel/overview` 做一次轻量校验”的方案，因为它更符合用户心智，也能及早暴露 token 错误。

## Routing And Navigation

推荐路由结构：

- `/login`
- `/`
  - 重定向到 `/overview`
- `/overview`
- `/events`
- `/snapshots`
- `/traces`
- `/traces/:traceId`
- `/artifacts/:artifactId`

说明：

- `Event Detail` 更适合先做成 Events 页内的 Drawer/Dialog
- `Artifact Detail` 适合单独路由，便于从 event 或 trace 中直接跳转
- `Trace` 同时支持：
  - `/traces` 手动输入 trace ID 查询
  - `/traces/:traceId` 直接深链打开

导航栏建议包含：

- Overview
- Events
- Snapshots
- Trace Lookup
- 当前 token 会话状态
- Logout

## Page Design

## 1. Login

职责：

- 输入 token
- 调用 `overview` 做有效性验证
- 登录成功后跳转到 `/overview`

关键状态：

- idle
- submitting
- invalid token / unauthorized
- network error

UI 元素：

- Token 输入框
- 登录按钮
- 错误消息区域
- API 地址说明或环境说明

## 2. Overview

职责：

- 展示管理面板核心摘要
- 作为后台首页
- 提供跳转入口到 Events / Snapshots / Trace 查询

展示内容：

- 当前保留窗口范围
- 当前窗口总事件数
- 最近错误数
- 最近 trace 数
- 按 category 统计的事件数
- 按 component 统计的 inflight 数

交互：

- 支持手动刷新
- 可选自动轮询
- 从统计卡片快速跳转到 Events 并带入筛选条件

## 3. Events

职责：

- 作为事件时间线与调试主工作台
- 支持轮询增量更新和筛选

筛选项直接映射 API：

- `after_id`
- `limit`
- `category`
- `kind`
- `trace_id`
- `conversation_id`
- `module`
- `level`

推荐交互：

- 顶部筛选栏
- 事件表格或时间线列表
- 自动轮询开关
- “跳到最新”操作
- 当 `cursor_reset_required=true` 时提示用户重置游标并重新加载
- 点击事件打开详情面板

事件列表字段建议至少展示：

- ID
- OccurredAt
- Category
- Kind
- Level
- Module
- Component
- TraceID
- Summary

## 4. Event Detail

职责：

- 深入查看单条 event
- 展示 envelope 基础字段与 payload
- 提供关联跳转

内容：

- 基础元数据
- `payload_type`
- 格式化后的 `payload` JSON
- `artifact_ids`
- 跳转到 trace 详情
- 跳转到 artifact 详情

若事件已被窗口淘汰导致 404，需要明确显示“事件已不在当前保留窗口”。

## 5. Trace Lookup / Trace Detail

由于 API 没有 trace 列表，所以 trace 能力设计为“查找 + 详情”模式，而不是“全量列表”模式。

职责：

- 在 `/traces` 输入 trace ID 后查询
- 在 `/traces/:traceId` 展示该 trace 下事件序列

展示内容：

- Trace ID
- 按时间或 ID 排序的事件序列
- 每个事件的摘要与类型
- 可点击查看 event detail

支持：

- `limit` 输入
- 空结果提示
- 不存在 trace 的提示

## 6. Artifact Detail

由于 API 仅支持按 ID 读取 artifact，所以 artifact 设计为详情页面。

职责：

- 展示原始调试文本内容
- 显示 artifact 元数据

展示内容：

- Artifact ID
- Event ID
- Kind
- CreatedAt
- Content 原文

关键要求：

- 内容不能截断
- 大文本区域要可滚动
- 保持等宽字体与便于复制的展示方式

## 7. Snapshots

职责：

- 查看当前快照类状态
- 支持按 namespace、key、module 过滤

展示内容：

- Namespace
- Key
- Module
- UpdatedAt
- Summary
- PayloadType
- Payload

推荐交互：

- 表格列表
- 行展开或侧边详情
- 筛选条件与 URL query 同步

## State Management

建议使用以下 stores：

- `authStore`
  - token
  - login/logout
  - restore session
- `uiStore`
  - 全局 toast
  - 布局状态
  - 当前激活导航
- `eventsStore` 或页面内 composable
  - 当前筛选条件
  - 轮询状态
  - cursor / last ID
- `overviewStore`
  - overview 数据与刷新状态

如果某些数据只服务单个页面，优先使用 composable，而不是过度集中到 Pinia。

## Polling Model

轮询重点在 `overview` 和 `events`：

- `overview`
  - 低频轮询即可，例如 5 到 15 秒
- `events`
  - 支持用户可控轮询频率
  - 初始加载后保留 `last_id`
  - 后续使用 `after_id=last_id` 增量拉取
  - 如果筛选条件变化，重置 cursor 并全量重载当前过滤结果

需要特别处理：

- `cursor_reset_required=true`
  - 提示用户“本地游标已落后于保留窗口”
  - 提供一键重载
- `has_more=true`
  - 提供“继续加载更多”或内部循环拉取策略

## Error Handling

统一错误分层：

- 401：token 缺失或无效，回到登录页
- 404：资源已被窗口淘汰或不存在，显示资源级空状态
- 400：筛选参数非法，保留当前页并提示修正
- 网络错误：显示可重试提示，不清空当前数据

建议提供一个统一的 problem 解析函数，从 `ErrorModel` 中提取：

- `title`
- `detail`
- `errors`

## UI Direction

这是调试导向后台，不需要营销风格，但必须具备高信息密度与清晰的层次。

建议视觉方向：

- 浅色为主，保留当前 PrimeVue 主题
- 导航壳采用左侧侧边栏或顶部导航
- 统计卡片、筛选栏、详情面板层次清晰
- 事件级别使用显著标签区分 `debug / info / warn / error`
- JSON 与 artifact 内容区域使用等宽字体

重点是“读数据效率”，而不是装饰性视觉。

## Environment And Configuration

建议增加 Vite 环境变量：

- `VITE_MANAGEMENT_API_BASE_URL`

默认策略：

- 开发环境优先从环境变量读取
- 未配置时使用同源 `/`

这样本地联调、反向代理和部署场景都能覆盖。

## Third-Party Libraries

本方案继续沿用现有依赖，不额外引入重量级数据层。

直接使用：

- `openapi-typescript` 生成类型
- `openapi-fetch` 生成类型安全请求
- `pinia` 管理会话与少量全局状态
- `primevue` 构建数据表格、输入框、抽屉、标签、提示

不建议在当前阶段再引入：

- axios
- tanstack query
- monaco editor

理由是当前 API 面积小、交互以查询为主，使用已有栈足以完成。

## Risks And Gaps

当前 OpenAPI 未显式声明 security scheme，但已有后端规格与实现约束要求 `Authorization: Bearer <token>`。前端需按该约定实现。

当前 API 还存在几个直接影响页面设计的限制：

- 没有 trace 列表接口，因此 trace 页面只能做查询入口
- 没有 artifact 列表接口，因此 artifact 只能从事件上下文进入
- `Event.Payload` 和 `Snapshot.Payload` 是开放结构，前端需要以 JSON viewer 方式兜底展示
- API 未提供枚举元数据，筛选值初期应使用自由输入，而不是写死枚举选项

这些限制不会阻塞第一版后台，但需要在计划中明确，避免实现阶段发生页面范围漂移。
