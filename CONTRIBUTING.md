# Contributing

Thanks for wanting to help. Switch Library Manager Web is free software under the
[GNU AGPL-3.0](LICENSE); contributions are accepted under the same license.

## Reporting bugs and asking for features

- Open an issue on <https://github.com/DeLFuS77/switch-library-manager-web/issues> with what you did, what you
  expected and what happened, the version (bottom of every page, or Diagnostics) and how you run the app (Docker,
  Unraid, Windows, Linux...). The report of Diagnostics can be copied and pasted: it holds no keys nor passwords.
- Security problems are reported privately: see [SECURITY.md](SECURITY.md).
- Never attach `prod.keys`, `title.keys` or game files.

## Contributing code

1. Fork the repository and make a branch for your change.
2. Build and test it locally:

   ```bash
   npm ci && npm run build
   go vet ./...
   go test ./...
   ```

   The pages are Go templates in `resources/pages`, `resources/partials` and `resources/layout.html`. Their styles
   are in `resources/web.scss` and its partials in `resources/scss`, and their script in the files of `resources/js`,
   one per subject, joined in the order of their names into one script by `scripts/build-js.mjs`. `npm run build`
   also leaves out of the style sheet the classes the app never uses (`scripts/purge-css.mjs`).

3. Open a pull request against `master`, saying what it changes and why. The checks of the pull request (tests with
   the race detector, `go vet`, CodeQL, govulncheck and npm audit) must pass before it is merged.

## Requirements for a change

- **Tests:** new functionality and fixed bugs come with automated tests in the `_test.go` files next to the code, so
  the behaviour is kept by later changes. A pull request without them is asked for them.
- **Style:** Go code is formatted with `gofmt` and passes `go vet` without warnings; JavaScript and SCSS follow the
  style of the files around them.
- **Interface texts:** every text of the pages is translated in all the languages of `web/translations*.go`; a test
  fails when one is missing.
- **Changelog:** user-facing changes get a line under `## Unreleased` in [CHANGELOG.md](CHANGELOG.md).
- **No keys nor game data** in commits, tests or issues.

## Releases

Versions follow [Semantic Versioning](https://semver.org/) and are tagged `MAJOR.MINOR.PATCH` (without a "v"). A tag
builds the programs and the Docker images, with their checksums and a signed provenance.
