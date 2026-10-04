# M1 对话 UI：SSE 穿中转层治理设计（一页，动手前评审）

> 依据：roadmap §五"技术设计先行"硬性要求；前科：日志 follow 死锁（审核留档 P1 项）。
> 范围：中转层流式（CompleteStream）→ 平台 SSE 下发 → 前端消费，全链路生命周期。

## 1. 链路与职责

```
前端 fetch(POST /ai/chat/conversations/:id/messages, stream 读)
   │  断连 = abort → req ctx cancel
平台 SSE handler（gin：c.Stream + Flush；delta channel）
   │  channel 有界（64）；写超时 30s
中转层 CompleteStream（OpenAI 兼容 stream:true）
   │  上游 SSE 解析（data: {...delta} / [DONE]）
   │  ctx 传导到上游 HTTP 请求——客户端断开即取消上游
收尾（WithoutCancel）：聚合回复落库（含中断 partial）+ 用量留痕
```

## 2. 超时与保活

- **整体 deadline 5min**（ctx WithTimeout）：单轮回复上限，防 goroutine 常驻；
- **空闲超时 60s**：`time.Ticker` 检查距上次 delta 的时间，超时判上游死亡（区别于整体超时），错误语义"上游空闲超时";
- **心跳**：平台→前端每 15s 一条 SSE comment（`: ping\n\n`），防中间层（nginx） idle 断连——nginx 默认 60s 读超时是已知坑。

## 3. 背压与丢弃策略

- delta channel 容量 64：正常打字速度远低于此，不丢字；
- **客户端读慢**（channel 满）：handler 写前端等待 30s 仍满 → 断开 SSE（客户端收 `error: 客户端消费过慢`），但**上游继续跑完**？——否：断开后 req ctx 已 cancel，上游同步取消，已聚合部分照常落库。对话是"人盯着看"的场景，读慢 30s 即人已离开，取消是最省语义。

## 4. goroutine 生命周期（死锁前科的针对性设计）

- **单一发送 goroutine**：handler 内 `c.Stream(func(w) { <-ch })`，上游读取在独立 goroutine，两者只经 channel 通信，无共享锁——从结构上排除"日志 follow 式互等死锁"；
- **退出路径穷举**（每条都关 channel 一次，close 由发送方独占）：
  1. 上游正常 [DONE] → 发 `done` 事件 → close；
  2. 上游错误/空闲超时 → 发 `error` 事件 → close；
  3. 客户端断开 → `c.Request.Context().Done()` → 上游 goroutine 经 ctx cancel 返回 → close；
  4. 整体 deadline 到 → 同 2；
- **落库永不阻塞退出**：消息落库与用量留痕用 `context.WithoutCancel` + 限时 5s，失败仅记日志（best-effort，与 P5 深审修复同纪律）。

## 5. 并发上限（单实例）

- 信号量 `chan struct{}` 容量 **8**：同时活跃流超出即 429"对话并发已达上限"；
- 会话级互斥：同一会话同时只允许一个进行中的流（重复发消息返回 409），防历史错乱。

## 6. 中断语义与恢复

- 前端"停止生成" = abort fetch = 断连路径 3：已生成内容落库（status=aborted），会话历史完整可续；
- 前端刷新/重开：拉 `GET messages` 重放——不做断点续传（M1 不做，出现真实需求再议）。

## 7. 验收口径（并入 M1 验收）

- 长回答（>2000 tokens）不断流；中途断开/停止后 `pprof goroutine` 无泄漏（退出路径穷举的直接验证）；
- nginx 反代下 60s+ 思考时间不断连（心跳生效）；
- 9 并发流第 9 个被 429；同会话并发第二条被 409。
