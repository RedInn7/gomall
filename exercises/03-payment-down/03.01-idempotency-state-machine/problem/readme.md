# 支付票根状态机

## 背景

用户在电梯里点击“支付”，服务端已经开始处理，但手机网络断了。客户端没有拿到响应，于是立刻使用同一个 `Idempotency-Key` 重试。第二个请求不能再扣一次钱，也不能把尚未完成的请求误报为成功。

GoMall 为每个支付意图保存三种状态：

- 没有记录：第一个请求取得处理权，写入 `processing`；
- `processing`：另一个请求正在处理，本次返回 `WAIT`；
- `done`：支付已经成功，本次直接 `REPLAY` 已保存的响应。

## 需要实现

补全 `Begin` 和 `CompleteSuccess`。状态转换必须是单向的：不存在 → `processing` → `done`。

## 约束

- 空 key 返回 `ErrEmptyKey`；
- 只有取得处理权的请求可以继续执行业务；
- 只有成功响应可以进入 `done`；
- `CompleteSuccess` 不能凭空创建记录，也不能覆盖已有成功响应；
- 不要为测试样例写死 key 或响应内容。

## 样例

```text
Begin("pay-42")                 -> ACQUIRED
Begin("pay-42")                 -> WAIT
CompleteSuccess("pay-42", "ok") -> nil
Begin("pay-42")                 -> REPLAY, "ok"
```

## 运行测试

```bash
go test -tags exercise ./exercises/03-payment-down/03.01-idempotency-state-machine/problem
```
