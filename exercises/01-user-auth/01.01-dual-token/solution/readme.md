# 01.01 参考实现

先判断 access 是否仍有效；只有 access 过期后才需要看 refresh。用 `Before` 可以自然表达“等于到期时刻即失效”。
