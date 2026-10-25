# Memogram 笔记模式 MVP

基于 `usememos/telegram-integration` 的 `6c896f0` 开发；保留上游历史、客户端、附件处理、原有功能和测试。根目录原始 `REQUIREMENTS.md`、`START_PROMPT.md` 未修改，仓库 `docs/` 中保留副本。

一个 Go 服务；素材、草稿和修改要求只保存在内存。未选处理方式时不会调用 AI 或写入 Memos；AI 草稿确认前不会上传 Memos 附件。没有网页正文抓取、QQ、钉钉、数据库、向量检索或自动模型路由。

## 本地启动

需要 Go 1.27.2 或以上。本次开发使用 Go 1.27.3、官方 `openai-go/v3 v3.86.0`，复用上游 `go-telegram/bot v1.27.0` 和 YAML 库。

```sh
cd /home/newmemogram/memogram
cp .env.notebook.example .env
cp notebook.example.yaml notebook.yaml
# 编辑 .env：Bot Token、数字 Telegram 用户 ID、Memos 地址/令牌及 AI Key。
# 编辑 notebook.yaml：服务地址、实际模型 ID、协议和能力。
chmod 600 .env
sh scripts/build.sh
./build/memogram --check-config notebook.yaml
./build/memogram
```

开发环境临时 Go 位于 `/tmp/newmemogram-toolchain/go/bin`，可加入 PATH 后运行；缓存位于 `/tmp/newmemogram-*`。服务器请正常安装 Go，或使用下述 Compose 构建。

`.env` 中的 `NOTEBOOK_CONFIG=notebook.yaml` 开启笔记模式。进程环境优先于 `.env`。不设置此项时仍是上游的即时保存模式。`--check-config` 仅验证 YAML，不要求凭据，也不访问网络。运行时需要 Telegram/Memos 凭据；AI Key 可暂缺，原样保存仍然可用，选 AI 时会提示缺少哪个变量。

只接受 `TELEGRAM_USER_ID` 对应用户的私聊和按钮；忽略群组和其他用户。不在 Telegram 中发送 Memos Token，直接用环境配置。原有绑定文件仍保留，但笔记模式使用 `MEMOS_TOKEN`。Memos 启动时通过上游只读接口识别 API 版本，并在日志中记录实际版本；链接优先使用实例配置的公开 URL。

## Docker Compose

```sh
cd /home/newmemogram/memogram
# 先按上面的步骤准备 .env 和 notebook.yaml。
sh scripts/build.sh compose -f compose.notebook.yaml config --quiet
sh scripts/build.sh compose -f compose.notebook.yaml up -d --build
docker compose -f compose.notebook.yaml logs -f
```

Compose 只启动 Memogram，连接已经部署的 Memos。`localhost` 指容器本身；宿主机服务使用 `host.docker.internal`，其他 Compose 服务使用同网络的服务名。`notebook.yaml` 以只读方式挂载，密钥放 `.env`。上游构建脚本自动传递版本和提交元数据。配置修改后重启；仅修改 `.env` 时用 `up -d --force-recreate` 重新创建容器。

## 网络代理

在 `.env` 中设置统一代理和 Telegram 专用代理：

```dotenv
# Telegram 和所有未单独配置的 AI 供应商使用此代理。
PROXY_URL=http://host.docker.internal:7890
# 可选；留空继承统一代理，也可设置为 direct 强制直连。
TELEGRAM_PROXY_URL=socks5://host.docker.internal:1080
# 可选；供下方供应商的 proxy_env 引用，可包含代理认证信息。
COMPATIBLE_PROXY_URL=socks5://user:password@host.docker.internal:1080
```

各 AI 供应商可在已有的 `providers` 配置下分别覆盖：

```yaml
providers:
  cli_proxy:
    # 保留原有 base_url、key_env、timeout 等设置。
    proxy_url: direct
  compatible:
    # proxy_url 和 proxy_env 二选一。
    proxy_env: COMPATIBLE_PROXY_URL
    # 无需认证时也可直接配置：
    # proxy_url: http://host.docker.internal:7890
```

优先级：Telegram 使用 `TELEGRAM_PROXY_URL` > `PROXY_URL`；每个 AI 供应商使用自己的 `proxy_url` 或 `proxy_env` > `PROXY_URL`。专用项留空时继承统一代理；任一层的 `direct` 表示强制直连。显式配置了 `proxy_env` 却未设置对应环境变量时，会报错并保留素材，不会静默改走其他连接。`--check-config` 校验 YAML 中的地址和字段冲突，但不要求环境变量已设置。

支持 HTTP/HTTPS 代理及 SOCKS5，均可使用 `协议://用户名:密码@主机:端口`；用户名或密码里的 `@`、`:` 等特殊字符需按 URL 规则编码。SOCKS5 和 `socks5h` 都由代理解析目标域名。实现复用 [Go 标准 HTTP Transport](https://pkg.go.dev/net/http#Transport)，保留 TLS 证书验证，无额外依赖。

Telegram 的初始化、长轮询、发送消息、文件查询及附件下载使用同一代理；图片送给 AI 前的下载仍使用 Telegram 的设置，AI 生成请求使用所选供应商的设置。`PROXY_URL` 不改变 Memos 的连接配置。未设置任何专用或统一代理时，保留原有 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 行为；显式代理和 `direct` 优先于这些系统变量，包含系统 `NO_PROXY` 中的地址。

`BOT_PROXY_ADDR` 仍表示 Telegram API 服务地址，通常留空；实际 HTTP/SOCKS5 网络代理使用上述新字段。Docker 内的 `127.0.0.1` 指容器自身，宿主机代理请使用 `host.docker.internal`，并确认代理监听的地址允许容器连接。二进制直接运行时，可按实际情况改用 `127.0.0.1`。修改配置后重启；代理地址及密码不会写入 AI 输入或主动打印到日志。

## 交互和默认值

连续发送或转发文字、网址、图片、相册及上游支持的文件，自动进入同一组。4 秒只是状态提示节流，不会自动分组、生成或保存；`/status` 随时显示准确计数和当前草稿。

| 入口 | 默认行为 |
| --- | --- |
| `1` / 原样保存 | 按接收顺序合并原文和来源，直接保存，不调用 AI |
| `2` / 简单整理 | 文字和图片说明参与整理，不下载或发送图片给 AI |
| `3` / 教程整理 | 读取图片；缺失的步骤标为待确认 |
| `4 本次提示词` | 自定义整理，默认读图 |
| `/model`、`/model primary` | 查看模型配置名称，切换下一次生成使用的模型 |
| `/retry` | 使用保留的素材、草稿和编辑要求重试 AI |
| 确认保存 vN | 保存这个完整版本；过期版本按钮被拒绝 |
| 补充素材 | 回到收集状态，允许自己输入文字作为素材；已有草稿保留 |
| 放弃本组 | 清空内存；部分成功时已在 Memos 创建的笔记保留 |

收集状态只识别本人发送且完整匹配的入口，例如 `2 notes` 不触发数字指令。转发内容、图片说明中的数字作为素材。草稿状态普通文字是编辑要求，转发和图片继续作为素材。生成时普通文字也可补充为素材；不会混入正在生成的快照。保存较早快照后，未纳入的新素材保留为下一组。

草稿按纯文本分段展示，保存的是原始 Markdown。修改、切换模型和补充素材时，继续把当前草稿和按顺序保留的编辑要求交给模型。用户应在确认前检查模型是否遵守了删改要求。

附件只保留 Telegram 标识，按需下载。读图时发送图片数据，不发送含 Bot Token 的下载链接。每个下载最多 20 MiB，方案最多读 8 张图片；支持 PNG/JPEG/WebP/GIF，其他图片应换格式或关闭读图。附件和说明按素材序号关联；保存时附件归属同一笔记，不做复杂正文插图排版。

保存正文后若部分附件失败，会保留笔记标识和失败列表；按钮只补传失败附件。保存进行中或等待补传时不接收新素材，会明确提示稍后重发。生成/保存时不允许重复启动或放弃，完成后再操作。重启后不恢复草稿和补传进度。

## 配置规则

数字映射、提示词、标签词表、模型、供应商和限制集中在 `notebook.yaml`。默认标签目标 2～5 个，可更少；标签和正文同次生成并一起确认。原样保存只保留已有标签。`primary` 默认使用示例 CLIProxyAPI 地址与 **待替换** 模型 ID、Responses、声明支持图片；`alternate` 演示第二个供应商与 `deepseek-chat`、Chat Completions、无图片能力。这些是可调整示例，不代表已验证你的服务提供这些模型。三个 AI 方案默认使用 `primary`，输出上限 8192；不默认发送思考等级或采样参数。

参数优先级：模型默认值 < 方案覆盖 < 本次临时参数（应用接口已支持，未开发 Telegram 参数菜单）。每一层先转换普通参数，再合并本层 `extra`，同层同名扩展叶子优先；三层转换结果依次递归合并，高层普通参数也能覆盖低层扩展参数。扩展不能替换输入、模型、工具、认证或服务端会话状态。未配置的可选参数不会发送。

固定配置和本次临时参数使用相同的保留字段校验，包含旧版函数调用字段。AI 请求只使用选定供应商的地址、Key 引用和显式请求头，不继承 SDK 自动读取的全局 `OPENAI_*` 设置；如需组织、项目或额外请求头，请在对应供应商的 `headers` 中配置。

Responses 将上限映射到 `max_output_tokens`，思考等级到 `reasoning.effort`，详细程度到 `text.verbosity`。Chat Completions 默认 `max_completion_tokens`，可配置 `token_field: max_tokens`；思考等级和详细程度使用 `reasoning_effort` / `verbosity`。原生思考预算需同时配置模型的 `reasoning_budget_field`（例如 `thinking.budget_tokens`）和参数 `reasoning_budget`；这取决于实际中转支持，默认关闭。模型名带思考后缀时不能重复配置请求体思考参数。请求失败不自动重试或切换供应商。

输出上限可能包括思考 Token。中断输出会标为不完整，不能确认保存；可以修改配置、切换模型或继续要求补全后重新生成。正文自动补上尚未出现的来源链接，不在末尾附全部原文。

业务边界：`internal/bot/notebook.go` 转换 Telegram 输入并展示；`internal/workflow` 管理内存状态、快照、草稿版本和保存恢复；`internal/ai` 用官方 SDK 适配两种协议；Memos 仍复用 `internal/memos` 与上游附件函数。未来平台接入不需要引入通用插件框架。

## 验证与限制

```sh
go test -race ./...
go vet ./...
sh scripts/build.sh
./build/memogram --version
./build/memogram --check-config notebook.example.yaml
python3 scripts/test-notebook.py ./build/memogram
```

测试覆盖多条素材/相册、转发数字、访问限制、保存前零写入、原文顺序、两供应商两协议的请求、参数覆盖/省略、图片数据、截断输出、保留编辑上下文、生成快照、新素材保留、旧按钮、并发重复确认、正文失败和附件补传。已有上游测试保留。Python 冒烟脚本启动真正的本地二进制，通过三个本地模拟服务完成长轮询收集、SDK 生成、重复确认后仅保存一篇笔记以及 SIGTERM 退出；不使用真实凭据。

代理测试使用本地 HTTP/SOCKS5 服务验证认证、远端域名解析、HTTPS CONNECT 与证书验证、请求取消、统一设置与专用覆盖、直连，以及 Telegram API/文件下载和两种 AI 协议的实际路由。

本次没有真实 Telegram、AI 中转、Memos 凭据，也没有 Docker 守护进程。模拟测试和本地构建不能代表真实服务或容器启动通过。尚需在实际服务器验证：Telegram 长轮询与相册接收；中转模型/图片/扩展参数的实际支持；实际 Memos 版本、公开链接、标签提取和文件存储；Compose 构建启动及重启行为。首次联调请记录 Memos 的确切版本。

同一进程内已知成功的正文不会因重复确认或附件补传重复创建。若 POST 请求已在服务器成功但返回前断网，客户端无法确定写入结果；Memos 未提供此处可用的幂等键，重试前应检查笔记。跨重启不承诺去重。进程关闭时尚未完成的保存可能需要在 Memos 中手工检查。

实现参考：[Memogram 上游](https://github.com/usememos/telegram-integration)、[官方 Go SDK](https://github.com/openai/openai-go)、[Responses 与 Chat Completions](https://developers.openai.com/api/docs/guides/migrate-to-responses)、[CLIProxyAPI 思考后缀](https://help.router-for.me/configuration/thinking)。
