# 05.05：结算日终对账

卖家余额更新成功，不代表整条资金链就正确。日终任务还要核对托管账户支出、卖家入账、渠道、卖家身份和余额快照。线上一天可能有百万张清算单，逐单扫描全部流水会退化为 O(n×m)。

请实现 `Reconcile` 和 `radixSortIssues`。每个订单只报告优先级最高的问题；输出必须按订单号升序。索引及核对阶段要求 O(n+m)，排序不得使用比较排序。

```bash
go test -tags exercise ./exercises/05-payment-settlement/05.05-settlement-reconciliation/problem
```
