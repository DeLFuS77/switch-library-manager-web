package switchfs

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestConvertXciToNsp(t *testing.T) {
	initNszKeys(t, "")
	source := writeTestXci(t, t.TempDir())
	target := ConvertedPath(source)
	if !strings.HasSuffix(target, ".nsp") || ConvertedPath("a.XCZ") != "a.nsz" {
		t.Fatalf("names: %s", target)
	}
	if err := ConvertXciToNsp(context.Background(), source, target, nil); err != nil {
		t.Fatal(err)
	}
	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	// the NSP holds the files of the secure partition, unchanged
	got, err := HashNcas(context.Background(), target, nil)
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("the NSP must hold the same NCA files: %v %v", got, err)
	}
	if err := ConvertXciToNsp(context.Background(), target, target+".2", nil); err == nil {
		t.Fatal("an NSP is no game card")
	}

	// an XCZ becomes an NSZ with the same compressed files
	xcz := CompressedPath(source)
	if _, err := CompressGame(context.Background(), source, xcz, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	nsz := ConvertedPath(xcz)
	if err := ConvertXciToNsp(context.Background(), xcz, nsz, nil); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompressed(context.Background(), nsz, expected, nil); err != nil {
		t.Fatalf("the NSZ must decompress to the same files: %v", err)
	}
}
