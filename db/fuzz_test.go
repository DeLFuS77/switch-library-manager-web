package db

import "testing"

// A file name of any kind gives a name, and never panics.
func FuzzParseTitleNameFromFileName(f *testing.F) {
	f.Add("Game [0100000000010000][v0].nsp")
	f.Add("ゲーム [0100000000010800][v65536].nsz")
	f.Add("[[]]")
	f.Fuzz(func(t *testing.T, name string) {
		ParseTitleNameFromFileName(name)
	})
}
