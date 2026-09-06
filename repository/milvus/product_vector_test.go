package milvus

import "testing"

func TestConfigureProductVectorCollectionRejectsUnsafeNames(t *testing.T) {
	previous := ProductVectorCollection
	t.Cleanup(func() { ProductVectorCollection = previous })
	if err := ConfigureProductVectorCollection("other_collection"); err == nil {
		t.Fatal("collection outside product_vector namespace must be rejected")
	}
	if err := ConfigureProductVectorCollection("product_vector_bad-name"); err == nil {
		t.Fatal("unsafe collection characters must be rejected")
	}
	if err := ConfigureProductVectorCollection("product_vector_0123abcd"); err != nil {
		t.Fatal(err)
	}
}
