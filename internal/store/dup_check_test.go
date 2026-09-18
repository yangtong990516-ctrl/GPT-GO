package store

import (
	"context"
	"testing"
)

func TestDuplicateCreateRejected(t *testing.T) {
	m := NewMockAccountStore()
	ok1, _ := m.Create(context.Background(), AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com"})
	ok2, _ := m.Create(context.Background(), AccountDocument{ID: "2", Email: "a@x.com", EmailNormalized: "a@x.com"})
	if !ok1 || ok2 {
		t.Fatalf("第一次应成功, 第二次应拒绝: ok1=%v ok2=%v", ok1, ok2)
	}
}
