package fake

import "testing"

func TestDeterministic(t *testing.T) {
	a, b := New(42).Dummies(3), New(42).Dummies(3)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed produced different data: %v vs %v", a[i], b[i])
		}
		if a[i].ID != int64(i+1) || a[i].Name == "" || a[i].CreatedAt.IsZero() {
			t.Fatalf("incomplete dummy: %+v", a[i])
		}
	}
}
