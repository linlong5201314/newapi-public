# 反代网关部署清单（自建 + 平台托管）

本文件记录平台已托管的反代网关、入口 URL、端口与接入方式。所有网关都是 OpenAI 兼容的
`/v1/chat/completions`，由 new-api 侧按「渠道」方式接入或直接给用户/客户端使用。

## 0. 统一入口 `gw.ailong.love`（DNS-only 直连 Railway）

| 项 | 值 |
| --- | --- |
| 入口 | **https://gw.ailong.love** |
| 解析 | `CNAME gw → z06s5r7j.up.railway.app`，Cloudflare **DNS-only（灰云，proxied=false）**，TTL 60 |
| 后端 | Railway node1（`newapi-public-production`，project `27fb5007…` / env `a0bbf032…` / service `21e6779e-ed4f-485c-9578-fb95151c8bb1`） |
| 证书 | Railway 自动签发；归属验证 TXT `_railway-verify.gw = railway-verify=<token>` |
| 上线 | 2026-11-14，`certificateStatus = CERTIFICATE_STATUS_TYPE_VALID` |

> **为什么不用 Cloudflare 代理（关键教训）**
> 国内家宽实测：CF 边缘向客户端传大文件被限速到 **≈2.2 KB/s**——5.3 MB 的前端主包 90 秒只下了
> 199,799 B（直连同文件 2.73 s / 1.9 MB/s）。所以凡是「前端资源经 CF 下发」的方案
> （Worker 透传、Worker 静态 302、Workers Static Assets 托管 `web/dist` 65 MB）都会让浏览器白屏。
> 结论：**CF 只做 DNS，静态资源与 API 全部由浏览器直连 Railway**。

实测（`gw.ailong.love` 直连）：

```
GET /                                   -> 200   1.71s   1,707 B
GET /login                              -> 200   0.71s   1,707 B（SPA 路由）
GET /api/status                         -> 200   0.77s
GET /static/js/index.be5eecfb28.js       -> 200   2.78s   5,374,616 B  (1.9 MB/s)
GET /static/js/async/79926.2951…js      -> 200   3.03s   6,827,321 B
真实浏览器（Chromium 1280×800）          -> DCL 2.0s / load 2.0s，首屏完整渲染
```

六台节点（同一个 Supabase 库、同一 rootadmin）：

| 节点 | 域名 | 说明 |
| --- | --- | --- |
| node1 railway438 | `newapi-public-production.up.railway.app` | **gw.ailong.love 指向它**，权重最高 |
| node2 railway842 | `newapi-public-production-49b4dfb0.up.railway.app` | 备用 |
| node4 railway196 | `newapi-public-production-91f2142c.up.railway.app` | 备用 |
| node5 railway850 | `newapi-public-production-1c18a29a.up.railway.app` | 备用 |
| node6 railway733 | `newapi-public-production-782d8c37.up.railway.app` | 备用 |
| node7 railway964 | `newapi-public-production-2113ed4c.up.railway.app` | 备用 |
| 875 railway875 | — | workspace 被 Railway 风控（`Suspicious activity`），项目+账号已删 |

Cloudflare Worker `newapi-router` 仍部署在 `https://newapi-router.m13136064359.workers.dev`
（加权分流 + 5xx 顺延 + `/__router/nodes`），但**不再绑定 gw.ailong.love**：CF 代理会拖慢大文件，
只作为 workers.dev 上的实验/备用入口。`wrangler.toml` 里不要再加 `routes`/`[assets]`。

## 1. DS2API（DeepSeek 网页端 → OpenAI 兼容）

| 项 | 值 |
| --- | --- |
| 仓库 | `CJackHwang/ds2api`（Dockerfile 多阶段，Node 构建 WebUI + Go 编译） |
| Railway 服务 | project `27fb5007-e70b-480f-ad9d-4adc241c8af5` / env `a0bbf032-f38d-4902-9098-76f50142f6f0` / service `c4b61855-2aa7-4a19-a920-6a40ea3bfd82` |
| 服务名 | `ds2api` |
| 域名 | **https://ds2api-production-24fcbc11.up.railway.app** |
| 容器端口 | 应用实际监听 **8080**（Dockerfile 里 `EXPOSE 5001` 是旧值，域名 targetPort 必须填 8080） |
| 管理后台 | `https://ds2api-production-24fcbc11.up.railway.app/admin` |
| 配置注入 | 环境变量 `DS2API_CONFIG_JSON`（整份 config JSON）+ `DS2API_ENV_WRITEBACK=1` |
| 平台 API Key | 存在 `DS2API_CONFIG_JSON` 的 `keys[]` / `api_keys[]`，可在后台改 |
| new-api 模板 | `ds2api`（`extraFields:["base"]`，`defaultExtra.base` 已指向上面的 `/v1`） |

探活结果：

```
GET  /v1/models              -> 200  {"data":[{"id":"deepseek-v4-flash",...},{"id":"deepseek-v4-pro",...}]}
POST /v1/chat/completions    -> 429  no accounts configured or all accounts are busy
GET  /admin                  -> 200  WebUI
```

`429` 是预期行为：网关本身通了、鉴权也过了，只是还没有灌 DeepSeek 网页账号。
在 `/admin` 里登录 DeepSeek 网页账号后即可出话。

## 2. WorkBuddy2API Panel（Qoder / MiniMax / Trae / WorkBuddy / 豆包 / CodeArts 合集网关）

| 项 | 值 |
| --- | --- |
| 仓库 | `xu20060420fan-cell/workbuddy2api-trae-qoder-minimax-panel`（Go + Dockerfile，内置 Web 面板） |
| Railway 服务 | 同 project/env，service `bfb7b4ea-6d12-4d58-89f6-f9c4a5fcacd6` |
| 服务名 | `wb2api` |
| 域名 | **https://wb2api-production.up.railway.app** |
| 面板 | **https://wb2api-production.up.railway.app/panel/** |
| 端口 | 7863（`WB2A_LISTEN=:7863`，域名 targetPort 7863） |
| API Key | 环境变量 `WB2A_API_KEY`（随机生成，见 Railway 变量；请求头 `Authorization: Bearer <key>`） |
| 模型命名 | `<realm>:<model>` — `cn:`（CodeBuddy 国服）、`global:`（国际版）、`trae:`、`minimax:` |

它一个部署就覆盖了 Qoder / MiniMax / Trae / WorkBuddy / 豆包 / 华为 CodeArts，面板里做
OAuth 设备码登录（Qoder、MiniMax 都是设备码 + PKCE，不需要本机客户端），并自带每日签到、
保活、账号池轮转、熔断冷却、会话粘性。

探活结果：

```
GET /healthz  -> 503 {"healthy":0,...,"total":0}     # 进程健康，但没有账号
GET /panel/   -> 200  管理面板
GET /v1/models -> 首次会懒加载上游模型目录，无账号时会一直悬挂到超时（预期）
```

> 要出话：打开 `/panel/` → 用 `WB2A_API_KEY` 进面板 → 添加 Qoder / MiniMax / Trae 账号
> （OAuth 或粘贴 token）→ 之后 `/v1/chat/completions` 用 `minimax:` / `trae:` / `cn:` 前缀即可。

## 3. 与 new-api 的接线方式

1. **用户自带网关**：`我的渠道 → 从模板创建 → ds2api / qoder / minimax`，`base` 留空即用平台
   托管实例（`defaultExtra` 已预填），也可以填自己的实例地址覆盖。
2. **平台托管渠道**：管理台 `渠道` 里新建 type=1（OpenAI）渠道，Base URL 填上面的 `/v1`，
   Key 填对应网关的 API Key；`OwnerId=0` 时按 `PlatformPublicFreeBilling` 对用户免计费。
3. **出口代理**：模板创建时可选「跟随出口池 / 直连 / 固定代理」，落库为渠道 `setting.proxy`。

## 4. 已知限制

- 两个网关都是**凭证网关**：没有登录上游账号就是空池（ds2api 429、wb2api healthy:0），
  这不是部署失败。
- 托管实例的账号池是**共享**的，多用户混用会互相抢额度；要隔离请各自 fork 部署一份。
- Qoder 的 COSY 签名、MiniMax 的 `yy` 签名会随客户端版本漂移，因此平台**不在 new-api 内复刻**，
  统一走这两个独立网关（它们自己跟进上游版本）。
