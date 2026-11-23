package diskfree

import (
	"errors"
	"fmt"
	"testing"
)

func TestFull(t *testing.T) {
	if Full(nil) || Full(errors.New("disk full")) {
		t.Fatal("an error that is not of the volume counts as full")
	}
	if !Full(fmt.Errorf("writing: %w", fullError)) {
		t.Fatal("a full volume is not told")
	}
}
