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

这样可以复用 `sub2api` 已有能力，`chatgpt2api` 只是可选上游之一。任务中心不需要理解 `chatgpt2api` 的专用任务接口，也不应该根据 `chatgpt2api` 的实现来设计自己的返回格式。

## 目标

在 `sub2api` 内置一个轻量生图任务中心，支持：

- 文生图
- 图生图
- 后台等待
- 任务列表
- 任务详情
- 图片预览
- 图片下载
- 输入图短期保存
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
  -> POST /api/v1/image-tasks/generations 或 /api/v1/image-tasks/edits
  -> 写入 image_tasks 任务
  -> 将 task_id 放入内置队列
  -> 内置 worker 后台执行
  -> worker 从数据库读取任务和输入图
  -> worker 调用本机 /v1/images/generations 或 /v1/images/edits
  -> sub2api 原生链路完成鉴权、调度、上游调用、计费
  -> worker 保存 response_json
  -> 前端轮询任务列表并展示结果
```

worker 第一版直接 HTTP 调用本机接口：

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

后端注册在 `/api/v1` 下，前端 `apiClient` 仍按现有习惯调用相对路径。

第一版只需要：

```text
POST /api/v1/image-tasks/generations
POST /api/v1/image-tasks/edits
GET  /api/v1/image-tasks
GET  /api/v1/image-tasks/:id
```

前端调用路径：

```text
POST /image-tasks/generations
POST /image-tasks/edits
GET  /image-tasks
GET  /image-tasks/:id
```

暂不做：

```text
POST /api/v1/image-tasks/:id/retry
POST /api/v1/image-tasks/:id/cancel
```

任务状态：

```text
pending
running
succeeded
failed
```

## 请求参数

文生图使用 JSON：

```json
{
  "api_key": "sk-xxx",
  "model": "gpt-image-2",
  "prompt": "A cat drinking coffee in a cyberpunk city",
  "size": "1024x1024",
  "n": 1,
  "quality": "auto"
}
```

图生图使用 multipart：

```text
api_key
model
prompt
size
n
quality
image            可重复
image[0]         可选格式
image[1]         可选格式
mask             可选，最多 1 张
```

第一版图生图范围：

```text
支持多张 image
支持可选 mask
不支持 stream
```

`mask` 是图片编辑里的遮罩图，用来标记哪些区域允许被改、哪些区域保持不变。第一版支持可选 mask，但只允许 1 张。

多图与 mask 上传约定：

```text
前端可以重复提交 image 字段
也可以提交 image[0]、image[1] 这类字段
mask 使用独立的 mask 字段
worker 按上传顺序重新构造 image multipart
如果存在 mask，worker 额外写入 mask multipart 字段
```

## Worker 请求固定参数

任务中心第一版强制非流式：

```text
stream=false
response_format=b64_json
```

原因：

- 任务中心通过轮询展示结果，不需要实时 SSE。
- worker 需要一次性拿到完整 JSON 并保存到 `response_json`。
- `b64_json` 不依赖远程 URL 生命周期，详情预览和下载更稳定。
- 对 `sub2api` 当前图片链路来说，`response_format=b64_json` 是最适合任务中心的兼容格式。

前端仍然兼容 `url` 和 `b64_json`，但这是对原生响应的容错，不是任务中心绑定某个上游实现。

## API Key 语义

任务提交时直接传当前用户选择的明文 `api_key`，后端只把它放进内存队列 job，用于本次 worker 调用本机 `/v1/images/*`。

`image_tasks` 不存 `api_key`，也不存 `api_key_id`。服务重启后内存 job 丢失，启动时会将遗留的 `pending/running` 任务标记为失败；后续 retry 时由外部重新传入 `api_key`。

执行结果仍由原生 `/v1/images/*` 链路决定：

- Key 额度或余额不足：任务失败
- Key 所属分组不允许生图：任务失败

这样可以保持任务表干净，不持久化任何 API Key 凭据。

## IP 白名单语义

第一版 worker 通过 `127.0.0.1` 调用本机 `/v1/images/*`，因此 API Key 的 IP 白名单仍然会生效。

约定：

```text
任务中心严格尊重 API Key IP 白名单。
如果某个 Key 配置了 IP 白名单，但没有允许本机地址，后台任务可能失败。
```

这是第一版的简单取舍。后续如果希望按用户提交任务时的真实 IP 判断，可以在 worker 请求里携带受信任的原始 IP，并在鉴权中增加专门逻辑。

TODO：

```text
后续评估是否支持 image-task worker 透传提交任务时的用户 IP，
让 API Key IP 白名单按用户真实来源判断，而不是按 127.0.0.1 判断。
```

## 数据库表

新增 `image_tasks`：

```text
id
user_id
mode                    generation / edit
status                  pending / running / succeeded / failed
model
prompt
size
n
quality
request_json
response_json
error_message

input_images_json
input_mask_json

created_at
started_at
finished_at
updated_at
```

说明：

- `response_json` 保存 `/v1/images/*` 的完整响应，可以包含 `b64_json`。
- 图生图输入图也存入任务表，保留 24 小时后随任务一起清理。
- 输入图不拆到独立文件或对象存储。
- `input_images_json` 保存输入图数组，每一项包含 field_name、b64、mime_type、filename、size_bytes。
- `input_mask_json` 保存可选 mask，结构包含 field_name、b64、mime_type、filename、size_bytes。
- 不写入独立 usage log，原生 `/v1/images/*` 链路会负责 usage log。
- 列表接口不返回 `response_json`、`input_images_json` 和 `input_mask_json`。
- 详情接口可以返回完整 `response_json`，并按需返回输入图预览数据。

## 图生图输入图片

图生图的输入图片第一版存入 `image_tasks` 表。

流程：

```text
1. /api/v1/image-tasks/edits 接收 multipart
2. 后端读取上传图片 bytes，支持多张 image 和可选 mask
3. 将图片转为 base64 数组，连同 field_name、mime type、filename、size_bytes 写入 image_tasks.input_images_json
4. 如果存在 mask，将 mask 转为 base64 对象写入 image_tasks.input_mask_json
5. 队列中只放 task_id
6. worker 从数据库读取任务
7. worker 将 input_images_json 还原为 multipart image
8. worker 将 input_mask_json 还原为 multipart mask
9. worker 调 /v1/images/edits
10. worker 保存 response_json 或 error_message
```

建议第一版限制：

```text
最多 10 张输入图
最多 1 张 mask
单张输入图最大 10MB
mask 最大 10MB
只允许 image/* MIME 类型
```

如果上传超过限制，提交接口直接返回错误，不创建任务。

## 队列与执行

第一版队列只保存内存中的 `task_id`，不做分布式队列。

固定代码常量即可，先不抽配置：

```text
worker_count = 4
queue_size = 50
task_timeout = 20m
retention = 24h
cleanup_interval = 1h
input_image_max_count = 10
input_image_max_size_each = 10MB
input_mask_max_size = 10MB
```

队列满时：

```text
直接返回 429
不创建任务
```

这样可以避免创建一个必然无法执行的任务。

任务执行：

```text
pending -> running -> succeeded
pending -> running -> failed
pending -> failed
```

worker 执行任务时使用 `context.WithTimeout(task_timeout)`。超时后标记为 `failed`。

## 意外终止处理

第一版不做任务恢复。

规则：

```text
服务启动时 pending/running -> 标记 failed
pending/running 超过 task_timeout -> 标记 failed
finished_at 超过 retention -> 删除任务记录
```

这样即使服务崩溃、进程重启、worker 意外退出，也不会让任务永久停留在 `running`。

虽然输入图已经存入数据库，理论上可以恢复执行，但第一版仍然不做恢复，保持实现简单。

## 图片预览与下载

不同上游返回格式可能不同，但任务中心不改原生响应，只在前端或任务 DTO 做归一化：

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

输入图预览：

```text
详情接口返回 input_images_json 和 input_mask_json
前端对每张图拼成 data:{mime};base64,{b64}
mask 也可以按同样方式预览
```

不需要额外前端库。

## 列表与详情

因为 `response_json`、`input_images_json` 和 `input_mask_json` 可能很大，列表接口不要默认返回完整响应。

列表接口：

```text
GET /api/v1/image-tasks?page=1&page_size=20
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
quality
error_message
created_at
updated_at
finished_at
input_image_count
has_input_mask
```

详情接口：

```text
GET /api/v1/image-tasks/:id
```

返回：

```text
列表字段
request_json
response_json
input_images_json
input_mask_json
```

详情用于预览和下载。前端应该按需打开详情，不要列表批量拉详情。

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
- 上传 mask（可选）

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

建议前端固定轮询间隔：

```text
poll_interval = 2500ms
```

## 不做的事情

第一版明确不做：

- 不直接接 `chatgpt2api` 专用接口
- 不改 `/v1/images/*` 原生响应
- 不新增独立计费逻辑
- 不保存输出图片文件
- 不引入对象存储
- 不做任务取消
- 不做任务重试
- 不做分布式 worker
- 不做复杂任务恢复
- 不做 stream/SSE 任务进度
- 不做浏览器指纹隔离
- 不把 image-tasks 配置抽到 YAML 或管理后台

## 最终结论

推荐实现：

```text
sub2api 内置 image-tasks 模块
worker 调用 sub2api 原生 /v1/images/*
固定 stream=false + response_format=b64_json
输入图、mask 和输出 response_json 存数据库 24 小时，图生图支持多张输入图和可选 mask
前端兼容 url / b64_json 预览下载
不转存输出图片，不自定义计费，不深度改原生 images 行为
```

这个方案的优点是：

- 简洁
- 低侵入
- 能复用 sub2api 原生计费和调度
- 支持 OAuth 原生生图和 chatgpt2api 上游
- 方便继续同步官方上游代码
- 第一版失败路径清晰，符合 fast-fail 取向
