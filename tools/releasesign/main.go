// Command releasesign signs the checksums of a release for the update of the app, with the
// private key of the secret SLM_SIGNING_KEY. The workflow of the releases runs it:
//
//	go run ./tools/releasesign -version 2.0.3 dist/SHA256SUMS.txt
//
// It writes the signature beside the file (SHA256SUMS.txt.minisig, as minisign does, with the
// version in its trusted comment), and checks it with the public keys the app is built with.
// Without the key, or with one that does not match them, it signs nothing and says so: the
// release goes out unsigned, and the app only tells of it. The key is never printed.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dtrunk90/switch-library-manager-web/internal/selfupdate"
)

func main() {
	version := flag.String("version", "", "the version of the release, as its tag")
	flag.Parse()
	if *version == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: releasesign -version 1.2.3 SHA256SUMS.txt")
		os.Exit(2)
	}
	if note := sign(os.Getenv("SLM_SIGNING_KEY"), *version, flag.Arg(0), selfupdate.Keys()); note != "" {
		// a warning of the workflow, and the release goes on unsigned
		fmt.Printf("::warning::%s\n", note)
	}
}

// sign writes the signature of the checksums, checked with the keys the app is built with, and
// tells why when it does not.
func sign(secret string, version string, sums string, keys []selfupdate.PublicKey) string {
	if secret == "" {
		return "no SLM_SIGNING_KEY: the release is not signed, the app only tells of it"
	}
	private, id, err := selfupdate.ParsePrivateKey(secret)
	if err != nil {
		return "SLM_SIGNING_KEY is not a key of the app: the release is not signed"
	}
	data, err := os.ReadFile(sums)
	if err != nil {
		return fmt.Sprintf("%s cannot be read: the release is not signed", sums)
	}
	signature := selfupdate.Sign(private, id, data, selfupdate.Comment(version))
	if _, err := selfupdate.Verify(data, signature, keys); err != nil {
		return "the key of SLM_SIGNING_KEY is not one of internal/selfupdate/release.pub: the release is not signed"
	}
	if err := os.WriteFile(sums+".minisig", signature, 0o644); err != nil {
		return fmt.Sprintf("the signature cannot be written: %v", err)
	}
	fmt.Printf("%s signed for version %s\n", sums, version)
	return ""
}
