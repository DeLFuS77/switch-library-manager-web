package diskfree

import "testing"

// A folder that exists has a volume with a size and room no larger than it.
func TestSpaceOfAFolder(t *testing.T) {
	folder := t.TempDir()
	free, size, err := Space(folder)
	if err != nil || size == 0 || free > size {
		t.Fatalf("free %d, size %d: %v", free, size, err)
	}
	if available, err := Free(folder); err != nil || available == 0 {
		t.Fatalf("free %d: %v", available, err)
	}
	if Volume(folder) == "" {
		t.Fatal("the folder has a volume")
	}
	if _, _, err := Space(folder + "/not there"); err == nil {
		t.Fatal("a folder that is not there has no space")
	}
}
