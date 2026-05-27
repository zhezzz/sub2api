# sub2api 内置生图任务中心方案

## 背景

当前有两个相关项目：

- `sub2api`：主网关服务，已经具备 API Key 鉴权、用户/分组、账号调度、余额/订阅计费、usage log、图片并发控制等能力。
- `chatgpt2api`：可将 ChatGPT 网页端相关能力包装成 OpenAI 兼容接口，并且已有自己的异步生图任务接口。

最初考虑过几种方案：

1. 单独新建一个生图服务，用户输入 prompt 和 API Key，由该服务调用 `sub2api /v1/images/*`，再提供任务列表。
2. 在 `sub2api` 中深度改造 `/v1/images/*`，让原生接口直接支持异步。
3. 在 `sub2api` 中新增一个轻量任务模块，不改原生 `/v1/images/*` 行为，由任务 worker 调用原生图片接口。

最终推荐第 3 种。

## 关键认知

`sub2api` 原生已经支持 `/v1/images/generations` 和 `/v1/images/edits`。

它的上游可以是：

- OpenAI OAuth 账号：`sub2api` 会桥接到 `/v1/responses + image_generation`，再重组成 OpenAI Images 响应。
- OpenAI API Key 账号：直接调用 OpenAI 官方 `/v1/images/*`。
- API Key + `base_url=chatgpt2api`：调用 `chatgpt2api /v1/images/*`。

因此，生图任务中心不应该直接绑定 `chatgpt2api`。更合理的是：

```text
image-tasks -> sub2api 原生 /v1/images/* -> sub2api 自己调度上游账号
```

这样可以复用 `sub2api` 已有能力，`chatgpt2api` 只是可选上游之一。

## 目标

在 `sub2api` 内置一个轻量生图任务中心，支持：

- 文生图
- 图生图
- 后台等待
- 任务列表
- 任务详情
- 图片预览
- 图片下载
- 24 小时任务清理

同时保持方案简洁：

- 不改原生 `/v1/images/*` 响应格式
- 不自定义计费
- 不引入对象存储
- 不长期保存图片文件
- 不做复杂重试、取消、分布式 worker
- 适当 fast-fail

## 总体架构

```text
用户前端
  -> POST /api/image-tasks/generations 或 /api/image-tasks/edits
  -> 写入 image_tasks 任务
  -> 内置 worker 后台执行
  -> worker 调用本机 /v1/images/generations 或 /v1/images/edits
  -> sub2api 原生链路完成鉴权、调度、上游调用、计费
  -> worker 保存 response_json
  -> 前端轮询任务列表并展示结果
```

worker 第一版建议直接 HTTP 调用本机接口：

```text
POST http://127.0.0.1:{port}/v1/images/generations
POST http://127.0.0.1:{port}/v1/images/edits
Authorization: Bearer 用户选择的 API Key
```

这么做虽然绕了一层 HTTP，但能完整复用原生链路，避免抽取 handler/service 时引入复杂度。

## 为什么不直接调用内部函数

理论上可以抽出 `OpenAIImagesExecutor`，让原生 handler 和 image-task worker 共用。

但现有 `/v1/images/*` 链路依赖较多：

- gin context
- 响应写入
- 鉴权上下文
- 分组权限
- 图片并发限制
- 用户并发限制
- 账号调度
- failover
- usage 计费
- 错误响应

第一版为了简洁和低侵入，先通过 HTTP 调本机原生接口。等功能稳定后，再考虑抽执行器。

## 接口设计

第一版只需要：

```text
POST /api/image-tasks/generations
POST /api/image-tasks/edits
GET  /api/image-tasks
GET  /api/image-tasks/:id
```

暂不做：

```text
POST /api/image-tasks/:id/retry
POST /api/image-tasks/:id/cancel
```

任务状态：

```text
pending
running
succeeded
failed
```

## 请求参数

文生图：

```json
{
  "api_key_id": 123,
  "model": "gpt-image-2",
  "prompt": "A cat drinking coffee in a cyberpunk city",
  "size": "1024x1024",
  "n": 1,
  "quality": "auto"
}
```

图生图使用 multipart：

```text
api_key_id
model
prompt
size
n
quality
image
```

第一版建议 worker 调 `/v1/images/*` 时固定：

```text
response_format=b64_json
```

原因：

- `sub2api` OAuth 原生路径会稳定返回 `b64_json`。
- `chatgpt2api` 路径通常会返回 `b64_json + url`。
- 前端可以统一兼容 `url` 和 `b64_json`。

后续如果要降低数据库体积，可以再允许 `response_format=url`。

## 数据库表

建议新增 `image_tasks`：

```text
id
user_id
api_key_id
mode              generation / edit
status            pending / running / succeeded / failed
model
prompt
size
n
request_json
response_json
error_message
created_at
started_at
finished_at
updated_at
```

说明：

- `response_json` 保存 `/v1/images/*` 的完整响应，可以包含 `b64_json`。
- 不把图片二进制拆出来存其他表。
- 不写入 usage log。
- 任务 24 小时后清理，因此数据库体积可控。

## 图生图输入图片

图生图的输入图片第一版不入库、不落盘。

流程：

```text
1. /api/image-tasks/edits 接收 multipart
2. 后端读取上传图片 bytes
3. 创建任务并把图片 bytes 放入内存 job
4. worker 构造 multipart 调 /v1/images/edits
5. 执行结束后内存释放
```

如果服务重启，内存 job 丢失，未完成任务标记为 `failed`。

这是有意的 fast-fail 取舍，避免第一版引入临时文件、加密存储、任务恢复等复杂设计。

## 图片预览与下载

不同上游返回格式可能不同：

- `sub2api` OAuth 原生路径：常见为 `data[].b64_json`，如果 `response_format=url` 则可能是 `data:image/png;base64,...`。
- `chatgpt2api` 路径：`response_format=b64_json` 时可能返回 `b64_json + url`，`response_format=url` 时返回 `url`。

任务中心不改原生响应，只在前端或任务 DTO 做归一化：

```text
优先 data[].url
否则 data[].b64_json
```

预览：

```text
url 是 http/data URL -> 直接作为 img src
b64_json -> 拼成 data:image/png;base64,{b64_json}
```

下载：

```text
http URL -> 直接打开或浏览器下载
data URL / b64_json -> 前端转 Blob 下载
```

不需要额外前端库。

## 列表与详情

因为 `response_json` 可能包含很大的 base64 图片，列表接口不要默认返回完整响应。

推荐：

```text
GET /api/image-tasks
```

只返回：

```text
id
mode
status
model
prompt 摘要
size
n
error_message
created_at
updated_at
finished_at
```

详情接口：

```text
GET /api/image-tasks/:id
```

返回完整 `response_json`，用于预览和下载。

## 计费

image-tasks 模块不做任何自定义计费。

worker 调用 `sub2api /v1/images/*` 后，原生链路会负责：

- API Key 鉴权
- 分组是否允许生图
- 账号调度
- 图片并发
- 图片数量解析
- 图片尺寸档位
- 图片价格
- 图片倍率
- 余额/订阅/API Key 限额扣费
- usage log

这样不会出现两套价格体系。

## 清理策略

配置建议：

```yaml
image_tasks:
  enabled: true
  worker_count: 2
  queue_size: 50
  task_timeout_seconds: 1200
  retention_hours: 24
  cleanup_interval_minutes: 60
```

规则：

```text
finished_at 超过 retention_hours -> 删除任务记录
pending/running 超过 task_timeout_seconds -> 标记 failed
服务启动时 pending/running -> 标记 failed
```

第一版不做任务恢复。

## 前端页面

新增一个用户侧页面，例如：

```text
/image-center
```

页面结构：

```text
[文生图] [图生图]

公共表单：
- API Key 选择
- model
- size
- n
- quality
- prompt
- 提交按钮

图生图额外：
- 上传图片

任务列表：
- 状态
- prompt 摘要
- 模型/尺寸
- 创建时间
- 成功后查看详情/预览
- 下载按钮
- 失败原因
```

轮询：

```text
仅当存在 pending/running 任务时，每 2-3 秒刷新一次。
```

## 不做的事情

第一版明确不做：

- 不直接接 chatgpt2api 专用接口
- 不改 `/v1/images/*` 原生响应
- 不新增独立计费逻辑
- 不保存输出图片文件
- 不引入对象存储
- 不做任务取消
- 不做任务重试
- 不做分布式 worker
- 不做复杂任务恢复
- 不做浏览器指纹隔离

## 最终结论

推荐实现：

```text
sub2api 内置 image-tasks 模块
worker 调用 sub2api 原生 /v1/images/*
输出 response_json 存数据库 24 小时
前端兼容 url / b64_json 预览下载
不转存图片，不自定义计费，不深度改原生 images 行为
```

这个方案的优点是：

- 简洁
- 低侵入
- 能复用 sub2api 原生计费和调度
- 支持 OAuth 原生生图和 chatgpt2api 上游
- 方便继续同步官方上游代码
- 第一版失败路径清晰，符合 fast-fail 取向
