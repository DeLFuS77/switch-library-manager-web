# Changelog

## Unreleased

### New features

- **Game page**: click a game anywhere (library, missing games, updates, DLC) to see its details: cover, publisher,
  release date, region, description and screenshots, your files with size and download button, update status and
  every DLC (owned, missing, ignored, update available). Works for games that are not in your library too.
- **Ignore with one click**: ignore missing DLC from the DLC page or the game page, and missing updates from the
  Updates page or the game page; ignored items can be restored from the game page. The lists in Settings still work.
- **Automatic synchronization**: synchronize every 6 or 12 hours, daily or weekly (Settings, disabled by default). The
  Settings page shows the last and the next synchronization.
- **Spanish interface**: the interface follows the browser language (English or Spanish) or the language chosen in
  Settings. Game names and descriptions come from the titles database and stay in English.

### Fixes

- Missing DLC are listed in a stable order.

## 1.1.1

### Fixes

- Library cards show the version of games without updates.
- Titles that need keys missing from prod.keys (dumped from an older firmware) are reported in Issues with the name of
  the missing key and how to fix it.
- Synchronize reloads prod.keys, so a replaced keys file is used without restarting the app.
- A library cache without recognized titles no longer scans every file twice on start.
- Log warnings no longer include stack traces unless debug is enabled.

### Other

- `*.keys` files are ignored by git, so console keys cannot be committed by mistake. Keys are never included in this
  repository or its releases: every user must provide their own `prod.keys`.

## 1.1.0

First release of this fork after [dtrunk90/switch-library-manager-web](https://github.com/dtrunk90/switch-library-manager-web)
1.0.11. Many parsing, organizing and title data fixes are ported from
[trembon/switch-library-manager](https://github.com/trembon/switch-library-manager).

> **Upgrading:** the local library cache is rebuilt automatically on the first start, because titles are now grouped
> differently. The default titles database URL changed (tinfoil.io no longer answers); `settings.json` files from
> older versions are updated automatically.

### New features

- **Organize page**: create a folder per game, rename files with templates, put updates and DLC in subfolders.
  Every action shows the exact list of changes first and only runs after you confirm it.
- **Delete old updates**: remove update and DLC files replaced by a newer version, optionally duplicates and empty
  folders, with the same preview.
- **Ignore options**: ignore missing updates for specific title IDs or for all DLC, ignore file types in Issues, hide
  demos in Missing Games.
- **Synchronization progress**: the current step and a progress bar are shown while synchronizing.
- **Optional password protection** with `SLM_AUTH_USERNAME` and `SLM_AUTH_PASSWORD`.
- The titles database is downloaded automatically on the first start.
- Configurable `titles_json_url` and `versions_json_url`, with fallback mirrors.
- prod.keys can be a folder or a `.keys` file, and is also found in the data folder and in `~/.switch`.
- Search is kept when changing pages; the search field shows the current search.

### Fixes

- Titles for recent firmware are read correctly (key revisions 0x10 and higher, compressed NACP titles, newer RomFS
  layouts).
- Neighbouring base games (for example `...6000` and `...8000`) and their DLC are no longer mixed up.
- No more crashes while synchronizing, after saving settings on a new installation, with invalid page parameters
  (`page=0`, `per_page=0`) or with split files in the API.
- Synchronization status polling no longer runs forever; pages reload when synchronization finishes.
- Only one synchronization runs at a time; saving settings no longer blocks until the library is rescanned.
- DLC downloads through the API work.
- PNG images can be resized.
- Images are cached instead of being downloaded again on every scan; failed downloads no longer leave empty files.
- Network timeouts and file handle leaks fixed; a broken cache or settings file is rebuilt.
- Games of which only updates or DLC are present now appear in Missing Games.
- Files identified by file name after their metadata could not be read are labelled as such in Issues.

### Security

- Requests that change data (synchronize, settings, organize) are rejected when sent by another web site.
- Image requests cannot read files outside the image folder.

### Other

- Moved from the archived boltdb to bbolt; Go 1.23.
- `titles.json` and `versions.json` are generated every 6 hours by a workflow of this repository.
- Test suite and CI for every push and pull request.
