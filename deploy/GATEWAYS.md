# 反代网关部署清单（自建 + 平台托管）

本文件记录平台已托管的反代网关、入口 URL、端口与接入方式。所有网关都是 OpenAI 兼容的
`/v1/chat/completions`，由 new-api 侧按「渠道」方式接入或直接给用户/客户端使用。

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
