//go:build exercise

package orderedlocks

func OrderedAccountIDs(buyerID, sellerID uint) []uint {
	if buyerID == sellerID {
		return []uint{buyerID}
	}
	if buyerID < sellerID {
		return []uint{buyerID, sellerID}
	}
	return []uint{sellerID, buyerID}
}
