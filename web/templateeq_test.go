package web
import "testing"
func TestTemplateEqComparesNumbers(t *testing.T) {
	if !templateEq(int64(0), 0) || templateEq(int64(1), 0) || !templateEq("a", "a") || templateEq("1", 1) {
		t.Fatal("numbers of different types with the same value are equal; texts are not numbers")
	}
}
