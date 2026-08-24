# 01 用户鉴权 Labs

配套 `docs/lecture/01-user-auth.md`。三题按一次受保护请求的链路展开：

第一次做实验请先阅读[代码实验使用说明](../README.md)，里面写了作答目录、运行命令和提交要求。

1. `01.01-dual-token`：access / refresh 到期后的 `PASS / REFRESH / RELOGIN`。
2. `01.02-rbac-cache`：带 TTL 和显式失效的角色缓存。
3. `01.03-token-version`：用用户版本号让旧 JWT 立即失效，并完成认证与授权判定。

运行学生版本：

```bash
go test -tags exercise ./exercises/01-user-auth/01.01-dual-token/problem
go test -tags exercise ./exercises/01-user-auth/01.02-rbac-cache/problem
go test -tags exercise ./exercises/01-user-auth/01.03-token-version/problem
```

实现还需正确处理到期边界、缓存穿透和版本号回退。
