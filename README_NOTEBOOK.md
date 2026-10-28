# AI 笔记模式使用与配置

本页说明 v0.3.2 的素材收集、图片理解、草稿修改和模型配置。安装与允许用户规则见[主文档](README.md)。

当前源码新增[网页配置](docs/WEB_SETTINGS.md)：通过独立密码登录，编辑供应商、模型和提示词等常用设置。网页保存的配置保存在数据目录并优先加载，`notebook.yaml` 用作初始配置。网页配置已包含在 v0.3.0 发布镜像中。

## 开启模式

复制 `.env.notebook.example` 为 `.env`、`notebook.example.yaml` 为 `notebook.yaml`，填写 Memos、Telegram、模型服务和允许用户。设置 `NOTEBOOK_CONFIG=notebook.yaml` 后启动程序。

允许用户统一由 `ALLOWED_USERNAMES`、`ALLOWED_USER_IDS` 和兼容字段 `TELEGRAM_USER_ID` 控制，至少填写一项。只有其中一项单独设为 `none` 才取消用户限制。所有获准用户使用 `MEMOS_TOKEN` 对应的 Memos 账号；每个 Telegram 用户有独立的私聊素材、草稿和确认按钮。

旧版 YAML 的 `user_id_env` 仍可作为额外 ID 环境变量的名称使用，省略即可使用通用访问配置。该环境变量设置了 ID 时会加入名单；显式 `none` 仍要求其他名单字段留空。不要将显示名称、群聊 ID 或 Bot Token 当作用户 ID。

## 收集和保存

发送文字、转发、网址、照片、相册及文件会加入当前素材组。只有自己发送且完整匹配的数字入口才会执行操作；转发内容、图片说明中的数字按素材处理。

| 入口 | 默认行为 |
| --- | --- |
| `1` | 原样合并文字和来源，直接保存，不调用 AI |
| `2` | 简单整理文字与说明，保留有效信息，默认不压缩成摘要 |
| `3` | 结合图片整理教程，缺失步骤标记“待确认” |
| `4 本次提示词` | 自定义整理，默认读取图片 |
| `5` | 图片理解，提取图片文字、数据与可见细节 |

生成前不会写入 Memos。AI 返回 Markdown 草稿后，可直接发送修改要求；继续修改时会带上当前草稿和按顺序保存的要求。通过“补充素材”回到收集状态，已有草稿保留。切换模型后，下一次生成生效。

确认按钮绑定具体草稿版本，旧版本或不完整草稿不能保存。生成期间仍可接收素材，新素材不会混入正在生成的快照；保存较早快照后，未纳入的素材留为下一组。其他用户的按钮不能操作自己的素材组。

正文保存后上传附件。若部分附件失败，会保留笔记标识，只补传失败附件。保存或待补传期间，当前用户需要先完成操作再发新素材。四秒间隔仅用于减少状态消息，不会自动分组、生成或保存。

`/status` 显示当前组，`/model` 列出配置名称，`/model primary` 切换模型，`/retry` 重试上次整理。“放弃本组”清空当前用户的内存内容；若正文已经保存，该笔记仍保留在 Memos。

## 图片理解

1. 发送图片或相册，可分别附上说明。
2. 发送 `5` 或点击“图片理解”。
3. 检查草稿，按需要求补充、删改或调整标签。
4. 点击最新版本的确认保存按钮，正文与原图保存到同一篇 Memos。

`3`、`4` 也支持图片，`2` 只读取说明。只有模型声明 `vision: true`，且方案设置 `images: true`，才会发送图片像素。每张图片会标注素材及附件索引，与对应的文字说明关联。重要图片信息应进入正文或简短图注，便于日后搜索；看不清的内容标记“待确认”。

照片和带图片 MIME 类型的文件会被识别为图片；常见图片扩展名可补充识别缺少 MIME 类型的文件。发送 AI 前按文件内容检测格式，支持 PNG、JPEG、WebP、GIF。其他格式需转换后重发，或选择不读图的方案。图片在本地按需下载后作为数据传给 AI，不发送带 Bot Token 的 Telegram 下载地址。

每个文件下载最多 20 MiB，`max_images` 默认八张。模型不支持、数量超限、下载失败或格式不支持时会提示并保留素材与已有草稿；可重试、换模型或改用简单整理。不发送图片像素时，会明确要求模型不能猜测画面。

已有 YAML 不会自动更新。新增入口时，在原有 `profiles` 与 `entries` 下分别合并：

```yaml
profiles:
  vision:
    name: 图片理解
    model: primary
    images: true
    tags: true
    prompt: |
      结合图片和说明整理笔记，提取重要文字、数据与可见细节。
      区分事实和推断，无法确认的信息标记“待确认”。
entries:
  "5": {label: 图片理解, action: generate, profile: vision}
```

将 `primary` 改为实际模型的配置名称，并确认对应模型的 `vision: true`。

## 供应商、模型和方案

三层分别配置：`providers` 保存服务地址和密钥环境变量名称；`models` 保存实际模型 ID、协议、能力和参数；`profiles` 保存整理提示词、默认模型、读图和标签开关。`entries` 只把数字入口映射到方案。

示例配置中的 `replace-with-your-model-id` 必须替换。示例供应商或模型名称不表示已验证你的服务提供这些能力。密钥写入 `.env`，YAML 中通过 `key_env` 引用。

```yaml
providers:
  my_provider:
    base_url: https://ai.example.com/v1
    key_env: MY_AI_KEY
    timeout: 180s
models:
  my_vision_model:
    provider: my_provider
    id: 服务实际提供的模型ID
    protocol: chat_completions
    vision: true
    token_field: max_tokens
    parameters:
      max_tokens: 8192
```

协议必须显式选择 `responses` 或 `chat_completions`，不会自动降级或换供应商。模型参数、方案参数、本次临时参数依次覆盖；同一层先转换普通参数，再合并 `extra`，高层配置优先。Telegram 暂未提供临时参数菜单。

| 配置项 | Responses | Chat Completions |
| --- | --- | --- |
| `max_tokens` | `max_output_tokens` | 默认 `max_completion_tokens`，可用 `token_field: max_tokens` 调整 |
| `reasoning_effort` | `reasoning.effort` | `reasoning_effort` |
| `verbosity` | `text.verbosity` | `verbosity` |
| `temperature`、`top_p` | 按原名发送 | 按原名发送 |

未配置的可选参数不发送。原生思考预算还需设置模型的 `reasoning_budget_field`，例如 `thinking.budget_tokens`，并配置 `reasoning_budget`；是否支持取决于供应商。模型 ID 已带思考后缀时，不要重复配置请求体的思考参数。

`extra` 可放供应商扩展，但不能替换模型、素材、认证、工具调用或服务端会话状态。请求只使用当前供应商的密钥和显式请求头，不继承全局 `OPENAI_*` 请求头。输出 Token 上限可能包含思考；中断或截断的草稿会标为不完整，补全后才能保存。

## 网络代理

`.env` 中的 `PROXY_URL` 是统一代理，`TELEGRAM_PROXY_URL` 可单独覆盖 Telegram API 与图片下载。每个供应商可再独立设置：

```yaml
providers:
  my_provider:
    base_url: https://ai.example.com/v1
    key_env: MY_AI_KEY
    timeout: 180s
    proxy_env: MY_AI_PROXY
    # 或使用 proxy_url: direct；proxy_env 与 proxy_url 二选一。
```

在 `.env` 填写 `MY_AI_PROXY=socks5://user:password@host.docker.internal:1080`。专用代理优先于统一代理，留空时继承；`direct` 强制直连。显式设置 `proxy_env` 却未提供对应环境变量会报错，保留素材。SOCKS5 由代理解析目标域名。Memos 的连接不受统一代理字段控制。

代理地址中的特殊字符按 URL 规则编码。容器内的 `127.0.0.1` 指容器自身，宿主机服务应使用可访问的地址。修改环境变量后重建容器，修改 YAML 后重启。

## 标签和来源

默认提示词采用扁平结构：短内容以自然段为主，不自动添加标题或拆成很多条目；长内容确有需要时才使用少量小标题、列表或表格。只有名称与链接的素材保留简洁说明，不扩写未读到的网页内容。

标签与正文在同一次 AI 输出中生成、一起确认。保留用户原有的有效标签及写法，去重后统一放在正文末尾同一行，标签之间用空格分隔，`#` 紧贴标签名，例如 `#rk3506 #开发板`。简单内容通常一至三个标签即可，不凑数量、不默认补“笔记”等泛标签；`common_tags` 只作相关候选。原样保存只保留原有标签。

更新示例文件不会自动覆盖已有配置。已有部署需同步 `system_prompt`、`tag_prompt` 与所用方案的 `prompt`；启用网页配置时，优先修改网页中保存的这些提示词。

保存时补上正文中尚未出现的来源网址，不在末尾重复附整份原文。图片仅作为 Memos 附件保存，不承诺复杂的正文内插图排版。

## 数据与验证边界

素材组、草稿、修改要求和补传进度仅在内存中，重启后丢失；保存成功后清理该用户的已完成组。所有用户共用配置的模型与 Memos 令牌，权限名单决定谁能调用这些服务。

本地测试使用模拟 Telegram、AI 和 Memos 服务验证传输及流程，不能替代真实模型识图质量或真实服务连通性验证。真实 Memos 版本兼容测试通过独立的容器测试执行，详情见[开发文档](docs/DEVELOPMENT.md)。
