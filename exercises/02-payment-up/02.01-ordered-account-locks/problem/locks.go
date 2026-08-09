//go:build exercise

package orderedlocks

func OrderedAccountIDs(buyerID, sellerID uint) []uint {
	// TODO: 返回本次支付需要加锁的账户 ID：
	// buyerID 与 sellerID 相同时只返回一个 ID；不同时返回两个 ID，并按升序排列。
	// 加锁顺序不能受买卖方向影响。
	return nil
}
