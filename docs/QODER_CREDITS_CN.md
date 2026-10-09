# Qoder Credits 计费与额度查询

核对日期：2026 年 10 月 9 日。面板当前支持原生 Qoder 国内版 OAuth 账号。

## 计费单位与付款方式

不能把所有费用都概括为按 Credits 收钱。订阅购买与模型消耗是两个层面：

| 项目 | 付款方式与计量 |
| --- | --- |
| 个人订阅 | 按月或按年预付，方案按周期提供 Credits |
| 团队与企业订阅 | 按席位、时长预付，成员获得或共享 Credits |
| AI 模型调用 | 按真实服务消耗扣 Credits，与模型、上下文、思考和任务过程有关 |
| 资源包 | 一次性购买 Credits，有独立有效期 |
| 服务账号 SA | 单独开通后按实际 Credits 用量后付费；公开说明当前为 0.046 元/Credit，以阿里云控制台为准 |
| QoderWake 云电脑 | 另按台、月购买；官网当前展示限时 59 元/台·月，原价 99 元 |
| 基础能力、限免与存量方案 | 存在不扣 Credits 或沿用旧机制的情况，不能按所有请求统一扣费 |

当前国内版公开基础方案：Pro 59 元/月、2,000 Credits；Pro+ 169 元/月、6,000 Credits；Ultra 559 元/月、20,000 Credits。Teams 99 元/席位·月、3,000 Credits；Enterprise 149 元/席位·月、3,000 Credits；VPC 199 元/席位·月、3,000 Credits。活动赠送不等于基础订阅额度。

模型选择器中的倍率是场景测算参考，官方明确其不是实际抵扣依据。不能假定一个 Credit 固定等于一次请求、某个 Token 数或某个人民币金额。Agent、工具执行和专家团可能触发多次模型调用；重试成功的调用也独立产生消耗。

## 额度来源与有效期

- Plan Credits 在当前额度周期有效，不结转；年付服务期不等于一次性发放全年 Credits。
- Add-on Credits 有独立有效期，个人资源包通常一个月，企业共享资源包通常三个月。
- 优先消耗先到期额度；到期相同时先扣 Plan，再扣 Add-on。
- 组织资源包可能有成员可用限额，不能把同一个共享池在多个账号上重复累计为总余额。
- 专属赠送额度可能限制模型。例如目前个人新订阅及续费活动中的 Qwen Credits 仅适用于 Qwen 系列。
- 当前 Qwen3.8-Flash 限免活动适用于个人与会员卡用户，明确不适用于 Teams、Enterprise 和 VPC。
- 模型调用失败，官方说明不扣 Credits；网关响应失败未必等于上游模型没有成功执行，因此不根据本地 HTTP 状态推算官方余额。
- 国内版与国际版 Credits 不等价且不互通；原灵码与全家桶订单也有产品线限制。

当前面板不领取奖励、购买资源包或调整组织上限。

## 已验证接口

| 地址 | 方法 | 验证结果 |
| --- | --- | --- |
| `https://openapi.qoder.com.cn/api/v2/quota/usage` | GET | 实际账号返回 Credits 计量、订阅额度、团队共享资源包状态与额度周期时间 |
| `https://openapi.qoder.com.cn/api/v2/user/plan` | GET | 实际账号返回套餐类型、付费状态、订阅起止时间等 |

认证使用后端保存的 OAuth 访问令牌，放在 `Authorization: Bearer ...` 头中。请求没有模型调用正文，不需要聊天接口的 COSY 编码或签名。令牌不进入网址、网页响应或面板日志。

额度接口中：

- `usageType` 必须确认是 `credits`，不能把次数或 Tokens 当 Credits 显示。
- `userQuota.total / used / remaining` 是订阅总额、已用和剩余。
- `addOnQuota` 若存在，独立显示资源包；不根据名字假定所有资源包都适用于全部模型。
- `orgResourcePackage.used / remaining / cap / available` 表示共享资源包的用量、可用数值、限额和可用状态；共享池不并入订阅余额。
- 顶层 `expiresAt` 作为额度周期时间；不把套餐 `end_date` 或顶层时间误贴成每个资源包的到期日。
- 仅保存明确返回的数值与日期。缺失或无法识别的余额显示查询失败，不补零。

当前真实账号没有返回个人资源包或模型专属奖励池。面板不会臆造这些余额；未返回的模型专属来源仍应在官方用量页核对。

## 面板与管理员接口

页面：`/admin/subscription-quota`，侧栏入口为「订阅额度」。目前只提供 Qoder。

- `GET /api/v1/admin/qoder/credits/accounts`：返回原生 Qoder 账号的必要信息及同一授权版本的已有查询结果。
- `GET /api/v1/admin/qoder/credits/accounts/:id`：查询对应账号额度。
- 查询参数 `refresh=1`：手动更新；短间隔重复查询受五秒保护。

均使用现有管理员认证。仅接入原生 Qoder OAuth 账号，旧 OpenAI 类型桥接账号不作为 Qoder 额度来源。

正常查询使用两分钟缓存，并合并同一授权版本的并发请求。授权或代理变化会隔离缓存；查询过程中发现账号身份变更时丢弃旧结果。刷新失败保留上次成功结果并明确标注；首次失败显示未知值。页面批量查询最多同时处理两个账号。

显示的是上游额度汇总值，不通过本地美元费用、请求数或 Token 用量倒算。官方聚合或结算可能有延迟，页面显示最近成功查询时间。

## 官方来源

- [国内版价格页](https://qoder.com.cn/pricing)
- [国内版计费说明](https://docs.qoder.cn/product-overview/billing-description)
- [Credits 定义、共享与扣减规则](https://docs.qoder.cn/product-overview/credits)
- [服务账号后付费](https://docs.qoder.cn/enterprise/service-account)
- [Qwen3.8-Flash 限免范围](https://docs.qoder.cn/events/flashoffer)
- [官方用量页](https://qoder.com.cn/account/usage)

官网部分旧文档与新产品说明存在版本差异，价格与活动也会变化。页面展示以实际账号接口结果为准，不硬编码套餐额度、优惠倍率或未来重置日期。

## 部署验收

2026 年 10 月 10 日完成服务器与网页验收：

- 额度与套餐接口使用真实账号查询成功，网页自动显示并能手动刷新。
- 匿名请求返回 401；旧桥接账号不进入原生额度查询，单账号访问返回 404。
- 八项针对额度的小数、缺失字段、凭证隔离、刷新失败及卡片显示测试通过；语言键检查、前端类型检查和生产构建通过。
- 375 像素与桌面布局无横向溢出，临时视口已恢复。
- 服务替换保留运行文件备份；已有账号、密钥、数据库及原生模型转发继续使用原配置。
