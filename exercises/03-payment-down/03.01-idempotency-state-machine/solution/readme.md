# 支付票根状态机：参考答案

`Begin` 只负责取得处理权或观察当前状态；`CompleteSuccess` 只接受 `processing`，确保失败请求不会被缓存成成功。
