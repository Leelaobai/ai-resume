# TadpolesAI — 设计已知问题 & 待细化项

> 最后更新：2026-04-10
> 状态说明：🔴 重大风险 / 🟠 开发前需明确 / 🟡 后续迭代可补 / ✅ 已修复

---

## ✅ 已修复（本轮）

| # | 问题 | 涉及文档 | 修复内容 |
|---|------|---------|---------|
| 1 | Billing gRPC 端口混乱（9091/9094/50051） | llm-gateway.md | `BILLING_GRPC_ADDR` 统一为 `billing-service:9094` |
| 2 | LLM Gateway URL 写错端口 8080 | resume-agent.md | 改为 `http://llm-gateway:8083` |
| 3 | 积分不足 HTTP 状态码 429 vs 402 | resume-agent.md | 统一改为 `402 insufficient_credits` |
| 4 | SSE `error` 事件格式未定义 | resume-agent.md | 补充了 error 事件 JSON 格式和语义 |
| 5 | 并发锁 TTL=60s < LLM 超时 120s | resume-agent.md | TTL 改为 300s；改用 CAS 解锁（Lua + value=X-Request-Id） |
| 6 | PreAuth TTL=10min 无缓冲 | billing-service.md | 改为 15min（覆盖 5轮×120s+余量） |
| 7 | `modify_template` 失败无 Rollback | resume-agent.md | 补充了 LLM失败/校验失败/DB失败三条路径的 Rollback 流程 |
| 8 | X-User-Group 流向在各服务文档中含糊 | llm-gateway.md, resume-agent.md | 明确：APISIX注入→Resume Agent读取→透传Gateway→仅做日志 |
| 9 | X-Service-Name 未在 Resume Agent 中标注为必填 | llm-gateway.md, resume-agent.md | llm-gateway 改为「强烈建议」；resume-agent §12.1 标为⚠️必填 |
| 10 | `credit_transactions` 无 `request_id` 字段 | billing-service.md | 添加 `request_id VARCHAR(128)` 字段、索引、GORM model、SettleRequest proto |
| 11 | 多轮 Agent 调用计费机制（is_continuation）未在 Gateway 文档中定义 | llm-gateway.md | 新增 §9.5 说明：同一 X-Request-Id 多次 PreAuth 幂等复用 auth_id |

---

## ✅ 已修复（第二轮）

| # | 问题 | 决策 |
|---|------|------|
| A | `X-Session-Id` 的生成方式 | Resume Agent 调用 Gateway 时传 `resume_id` 作为 `X-Session-Id` |
| B | Billing ERR_INSUFFICIENT_CREDITS 状态码 | 统一改为 `402`，billing-service.md §11 已更新 |
| C | `modify_template` 的 PreAuth 时机 | 保持当前设计：工具调用时独立 PreAuth，失败由 LLM 向用户解释 |
| D | `usage_logs.billing_tx_id` 来源 | 从 Gateway SSE 最后一帧 `x_gateway.billing_transaction_id` 取 |
| 12 | 数据库技术栈 | 确认 MySQL 8.0（DDL 已是 MySQL 语法，PROJECT_STATUS 已更正） |
| 13 | User Service gRPC 端口 | 确认 9092（billing-service.md 已从 50051 更正） |

---

## ✅ 已修复（第三轮 — 设计补充）

| # | 问题 | 补充内容 | 涉及文档 |
|---|------|---------|---------|
| 14 | Gateway ZSET 池启动初始化未定义 | 新增 §5.9：从 DB 全量重建 ZSET + 熔断器 + model_configs 缓存 | llm-gateway.md |
| 15 | Settle gRPC 失败重试机制仅一句话 | 新增 §9.7：Redis ZSET 重试队列 + 指数退避 + 5 次上限告警 | llm-gateway.md |
| 16 | 赠送积分过期与 PreAuth 冻结竞态 | §8.4.2 增加 `balance - remaining >= frozen` 前置检查 | billing-service.md |
| 17 | 两个服务均无优雅关闭设计 | Gateway §6.3 + Billing §13：SIGTERM 处理、in-flight 等待、超时清理 | 两个服务 |
| 18 | 流式中断 Token 计数 fallback 缺失 | §6.2 补充 StreamTokenCounter 本地累加估算机制 | llm-gateway.md |

---

## 🟡 后续迭代可补

| # | 问题 | 建议 |
|---|------|------|
| E | billing-service.md 的 gRPC 错误码表不够细（区分积分不足 vs 钱包不存在 vs wallet 冻结） | 补充细化 gRPC Status 说明（FAILED_PRECONDITION 含三种原因） |
| F | billing-service.md 定时任务在多实例下的 leader election 未说明 | 当前用条件 UPDATE 抢占保证幂等，无需 leader election；但文档应说明这一点 |
| G | 全局降级策略缺失（Redis 不可用时 Resume Agent 如何行为） | 架构文档中补充：Redis 不可用→跳过并发锁（降级为允许并发）；Gateway 不可用→直接 503 不重试 |
| H | 用户充值后 group 升级最长 1 小时延迟对模型路由的影响 | 已知风险，设计上接受；可在前端充值成功后提示"模型升级最长 1 小时内生效" |
| I | `pre_auths` 表的 `expires_at` 应基于 `PREAUTH_TTL_MINUTES` 动态配置，但 SQL DDL 里是固定注释 | 代码实现时注意从环境变量读取，不要硬编码 10min |
| J | `resume-agent.md` §13 billing-stats 响应格式与 billing-service.md §5.2 的标准契约对比未校验 | 检查 `service_label`、`item_count`、`breakdown` 字段是否一致 |

---

## 🔴 重大风险备忘（已修复但需实现时特别注意）

### ⚠️ 风险 1：并发锁的 CAS 解锁必须用 Lua 原子脚本

```go
// 错误实现（非原子，有竞态）：
val := redis.Get(lockKey)
if val == lockID {
    redis.Del(lockKey)  // 这两行之间可能被其他 goroutine 抢占
}

// 正确实现（Lua 原子脚本）：
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end`

// 在 defer 中执行，保证 panic 也能解锁
defer func() {
    redis.Eval(ctx, unlockScript, []string{lockKey}, lockID)
}()
```

---

### ⚠️ 风险 2：`modify_template` 必须在所有失败路径调用 Rollback

见 resume-agent.md §4.3 失败处理说明。如果遗漏任何一条失败路径的 Rollback，对应的 sub-request-id `{request_id}-tpl` 的 PreAuth 积分会永久冻结，直到 15min 后自动过期清理。

---

### ⚠️ 风险 3：X-Service-Name 不传会导致 credit_transactions.service_name = NULL

账单对账公式依赖 `service_name='resume-agent'` 过滤，为 NULL 的记录对账会出现差异，需人工排查。Resume Agent 的所有 LLM Gateway 调用**必须**携带 `X-Service-Name: resume-agent`。
