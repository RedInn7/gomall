# 怎么运行作业测试

以下命令均在仓库根目录运行。

只检查代码能不能编译：

```bash
./scripts/grade-exercises.sh compile
```

做完全部题目以后运行：

```bash
./scripts/grade-exercises.sh student
```

维护参考实现时运行：

```bash
./scripts/grade-exercises.sh solution
```

只跑一题时，使用题目 README 里的 `go test` 命令。不要修改 `*_test.go`、build tag、函数签名和已经给出的错误变量。
