# 真实 Claude Code CLI 2.1.198 请求 — 完整 + 逐项作用标注

> 抓取方式:dump-server(`ANTHROPIC_BASE_URL` 指向本地,`claude -p hi`),不发真实 API。
> 端点:`POST https://api.anthropic.com/v1/messages?beta=true`

## 一、请求头(HEADERS)

| 头 | 值(示例) | 作用 | 伪装关键? |
|---|---|---|---|
| `authorization` | `Bearer sk-ant-oat01-…` | Max 订阅的 OAuth 访问令牌(鉴权) | ✅ 每号真令牌 |
| `content-type` | `application/json` | 请求体格式 | 固定 |
| `accept` | `application/json` | 期望响应格式 | 固定 |
| `user-agent` | `claude-cli/2.1.198 (external, sdk-cli)` | **CLI 身份+版本**(风控看这个) | ✅ 必须真版本 |
| `x-app` | `cli` | 应用标识 | 固定 |
| `anthropic-version` | `2023-06-01` | Anthropic API 版本 | 固定 |
| `anthropic-beta` | `claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-…,fine-grained-tool-streaming-2025-05-14,…` | **开启的 beta 特性开关**(Claude Code 专属能力) | ✅ 决定可用功能 |
| `anthropic-dangerous-direct-browser-access` | `true` | 允许非浏览器直连 | 固定 |
| `x-claude-code-session-id` | `<uuid>` | **本次对话的会话 ID**(每对话轮换,== body.metadata.session_id) | ✅ header==metadata |
| `x-stainless-arch` | `x64` | Stainless(Anthropic官方SDK生成器)遥测:CPU架构 | 跟出口机器 |
| `x-stainless-os` | `Linux` | 操作系统 | 跟出口机器 |
| `x-stainless-lang` | `js` | SDK 语言 | 固定 |
| `x-stainless-runtime` | `node` | 运行时 | 固定 |
| `x-stainless-runtime-version` | `v26.3.0` | Node 版本 | 跟真实CLI |
| `x-stainless-package-version` | `0.94.0` | `@anthropic-ai/sdk` 版本 | 跟真实CLI |
| `x-stainless-retry-count` | `0` | 当前重试次数(首次=0) | ✅ |
| `x-stainless-timeout` | `600` | 请求超时(秒) | 固定 |
| `accept-encoding` | `gzip, deflate, br, zstd` | 支持的响应压缩 | ✅ 我们Go侧解压 |
| `connection` | `keep-alive` | 连接复用 | 固定 |
| `host` / `content-length` | — | HTTP 标准 | 自动 |

> **注意:真实 CC 2.1.198 不发 `x-client-request-id`**(我们早期自己加了,已删)。

## 二、请求体(BODY)顶层键序

```
model, messages, system, tools, metadata, max_tokens, thinking, context_management, output_config, stream
```
(键顺序本身是指纹,我们逐字节对齐)

| 字段 | 值 | 作用 |
|---|---|---|
| `model` | `claude-sonnet-5` | 目标模型 |
| `max_tokens` | `64000` | 最大输出 token(haiku=32000) |
| `thinking` | `{"type":"adaptive","display":"omitted"}` | **扩展思考**:adaptive=模型自决是否思考;display:omitted=思考内容不回传给用户端(haiku 用 `{budget_tokens:31999,type:enabled,display:omitted}`) |
| `output_config` | `{"effort":"high"}` | 推理投入档位(haiku 无此字段) |
| `context_management` | `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}` | **上下文自管理**:自动清理旧 thinking 块省上下文,keep:all=保留全部非思考内容 |
| `stream` | `true` | SSE 流式响应 |
| `metadata.user_id` | JSON字符串 | **身份三元组**(见下) |

### metadata.user_id(身份,是个 JSON 字符串)
```json
{"device_id":"<每机器稳定sha256>","account_uuid":"<真实账户UUID>","session_id":"<本次对话uuid>"}
```
- `device_id`:每台机器稳定、**独立于** account_uuid(不能和账户一样,也不能全队一样)
- `account_uuid`:该 Max 账户的**真实** UUID
- `session_id`:本次对话,**等于** header `x-claude-code-session-id`

### system(3 块,身份+主提示词)
| # | 键序 | cache_control | 内容 |
|---|---|---|---|
| [0] | `type,text` | 无缓存 | `x-anthropic-billing-header: cc_version=2.1.198.542; cc_entrypoint=sdk-cli;` ← **计费头兼版本标记** |
| [1] | `type,text,cache_control` | `{type:ephemeral,ttl:1h}` | `You are a Claude agent, built on Anthropic's Claude Agent SDK.` ← **身份声明** |
| [2] | `type,text,cache_control` | `{type:ephemeral,ttl:1h}` | `You are an interactive agent that helps users with software engineering tasks…` ← **主系统提示词**(缓存1小时省钱) |

### tools(28 个基础工具,固定集)
```
Agent, Bash, CronCreate, CronDelete, CronList, DesignSync, Edit, EnterWorktree,
ExitWorktree, Monitor, NotebookEdit, PushNotification, Read, RemoteTrigger,
ReportFindings, ScheduleWakeup, SendMessage, Skill, TaskCreate, TaskGet, TaskList,
TaskOutput, TaskStop, TaskUpdate, WebFetch, WebSearch, Workflow, Write
```
- 每个 tool 键序:`name, description, input_schema`
- 用户/MCP 工具(`mcp__*`)追加在这 28 个之后;非 CC 认识的工具会被删

### messages(对话内容)
- `[0]` role=**user**:`<system-reminder>`(注入的 userEmail/currentDate 上下文)+ 实际输入(如 "hi"),末块带 `cache_control` 缓存
- `[1]` role=**system**(对话中系统消息):可用 agent 类型列表、可用 skills 列表等运行时上下文
- 后续 assistant/user 轮次交替(每条 `role,content` 键序,role 优先)

## 三、我们的伪装如何对齐(meridian-mirror)
- **头**:replay 真实抓来的头(删 x-client-request-id、accept-encoding 完整、真令牌、header session==metadata session)
- **body**:①system/tools/context_management splice 真实抓来的**原始字节** ②thinking/output_config/max_tokens 按模型强制、按真实键序输出 ③metadata 用真实 account_uuid + 独立 device_id ④用户 messages 保留原始字节但 wrapper 规范成 role 优先 ⑤顶层+嵌套键序逐字节对齐
- **排除**:`cc_prev_req`(确认封号风险,绝不加)
