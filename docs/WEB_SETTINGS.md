# 网页配置

网页内嵌在 Memogram 的 Go 程序中，使用原生 HTML、CSS、JavaScript，不需要 Node、数据库或单独的前端服务。仅在 AI 笔记模式启用。

## 开启访问

`.env` 中配置：

```dotenv
WEB_ADDR=127.0.0.1:8080
WEB_URL=https://memogram.example.com
```

使用现有公网 HTTPS 反向代理，将这个域名转发到 `127.0.0.1:8080`。网页使用安全 Cookie，需要通过 HTTPS 登录。推荐独立域名或子域名，并将整个站点转发到 Memogram，不要挂在子路径下。

Caddy 示例：

```caddyfile
memogram.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

已有 Nginx HTTPS 站点可在对应 `server` 中添加：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

首次打开页面会提示设置独立管理密码，之后使用此密码登录。设置 `WEB_URL` 后，也可在 Telegram 私聊发送 `/settings`，点击按钮打开网页。

这个网页使用独立密码，不依赖 Telegram 登录身份。新部署后先设置密码，再分享访问地址。

## Docker Compose

`compose.notebook.yaml` 已将容器内 `8080` 映射到宿主机的 `127.0.0.1:8080`，配置文件仍只读挂载，数据写入持久卷。启动方式：

```sh
sh scripts/build.sh compose -f compose.notebook.yaml up -d --build
```

若反向代理也运行在容器中，可让两者加入同一 Docker 网络，通过 `memogram:8080` 连接，不需要向公网暴露 HTTP 端口。

## 可以修改什么

- **AI 供应商**：地址、API Key、超时、专用代理、额外请求头。
- **模型**：供应商、实际模型 ID、Responses / Chat Completions、读图能力和生成参数。
- **整理方案**：默认模型、长提示词、读图和标签开关、方案参数。
- **通用设置**：系统提示词、标签提示词、常用标签、素材限制、数字入口。
- **管理密码**：修改独立网页密码，其他已登录会话需要重新登录。

编辑后点击一次“保存更改”。配置校验失败时，保留当前正在使用的设置，并显示原因。正在编辑但尚未保存的内容不会写入文件。

API Key 不会回显。留空保留原有密钥；“改用环境变量密钥”会清除网页保存的密钥，使用该供应商的 `key_env`。无需认证的供应商可以不配置密钥和 `key_env`。

机器人 Token、Memos 地址与令牌、访问白名单、网页监听地址仍由启动环境配置。此页面主要用于经常调整的 AI 和整理参数。

## 数据保存

网页设置保存在 `DATA` 同目录的 `web-settings.json`。默认二进制路径为工作目录下的 `web-settings.json`，容器中为 `/data/web-settings.json`。

文件包含密码哈希和网页设置的供应商密钥，权限为 `0600`，请与绑定数据一起保留。管理密码不以明文保存，供应商密钥需要供程序调用，因此保存在这个私有文件中。

`notebook.yaml` 提供初始设置；存在网页保存的配置后，优先使用网页设置。重启后设置保留，但需重新登录网页。`WEB_ADDR=off` 关闭网页入口，已保存的 AI 设置仍然加载。

如果忘记管理密码，可停止服务，备份 `web-settings.json`，只删除其中的 `password_hash` 和 `auth_id` 字段，再启动服务重新设置密码；保留 `notebook` 与 `api_keys` 字段即可保留配置。
