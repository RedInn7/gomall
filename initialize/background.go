package initialize

import "sync"

// background 记录启动流程拉起、进程退出前要等它收尾的后台 goroutine（库存预热、Xcash 对账）。
var background sync.WaitGroup

// WaitBackground 等后台 goroutine 和 outbox 发布器收尾。先取消传给各 Init 的 ctx 再调用。
func WaitBackground() {
	background.Wait()
	if globalPublisher != nil {
		<-globalPublisher.Done()
	}
}
