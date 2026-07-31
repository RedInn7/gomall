# 01.01 双 token 续期

实现 `NextAction`：

- access 尚未过期：`PASS`；
- access 已过期、refresh 尚未过期：`REFRESH`；
- refresh 也已过期：`RELOGIN`。

注意边界语义：`now == expiresAt` 时 token 已经过期。不要依赖真实时钟，测试会显式传入 `now`。
