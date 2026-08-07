package balancer

import "testing"

func TestNewSingleIterator(t *testing.T) {
	iter := NewSingleIterator(42, "model/variant")
	if iter.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", iter.Len())
	}
	if iter.IsSticky() {
		t.Fatal("single iterator must not be sticky")
	}
	if !iter.Next() {
		t.Fatal("first Next() = false, want true")
	}
	item := iter.Item()
	if item.ChannelID != 42 || item.ModelName != "model/variant" {
		t.Fatalf("Item() = %+v", item)
	}
	if iter.Next() {
		t.Fatal("second Next() = true, want false")
	}
}
