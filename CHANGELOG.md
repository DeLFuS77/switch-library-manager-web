# Changelog

## 1.19.0

### New features

- **SD card planner**: choose the size of a card and what you like (genres, favorites, games for several players,
  a collection) and the app proposes the games that fit, with why each one was chosen. Change the list by hand,
  download it or copy the games to the card or a USB drive, a folder per game; nothing is ever deleted.
- **More data of each game**: genres, number of players, languages and age rating from the titles database. The
  library filters by genre, players and language of the game, the game page shows them, and the statistics show
  the games by genre, publisher and year of release. The genres of every store are joined into one list.
- **The same game from several regions**: the Space page groups the copies of a game from different stores and
  suggests the one to keep (in your language, more languages, newer update, more DLC).
- **Convert game cards**: XCI files become NSP files and XCZ files NSZ files, each new file checked before the
  original is deleted, if asked.
- **Forgiving search**: accents, case, signs and the order of the words do not matter, and small typing mistakes
  are accepted ("zelda brth" finds "Breath of the Wild").
- **Five more languages**: Dutch, Russian, Japanese, Korean and Chinese, with dates in their format and the game
  names from their eShop.

## 1.18.1

### Fixes

- The progress bars of running tasks, such as a file verification, move on smoothly instead of starting again
  at every update.
- A test that depended on the speed of the machine kept the 1.18.0 release from being built on GitHub; the Docker
  image of 1.18.0 was published. This version has everything of 1.18.0.

## 1.18.0

### New features

- **Favorites**: mark the games you like most with the star, on their card or their page, and see only them
  ("Favorites only") or first (sort by favorites). A collection named "Favorites" becomes the stars.
- **The missing games in the library**: "Show the missing games too", in the filters, shows the games of the
  eShop you do not have greyed out with yours, with their heart for the wishlist. Searching a name then shows
  which games of it you have and which ones you are missing.
- **From the same series**: the game page shows the other games of its series, the ones you have and the ones
  you are missing, found by their names.
- **Filters in one panel**: the library filters besides the status open from one "Filters" button, with a section
  for each; the active ones show as chips that can be removed. A legend explains the star, the heart and the grey.
- **Faster file verification**:
  - several files are checked at once, with a speed setting (gentle, normal or fast) that shows how many files
    each one checks at once on your server;
  - the results are saved as it goes, so a stopped verification goes on where it was;
  - the progress shows the speed and the time left;
  - the scheduled verification also checks again the files not checked for a week or a month, a part every
    night within the background hours, so the whole library is checked over time.

## 1.17.1

### Fixes

- The app could stop when the wishlist or a collection changed while the pages were prepared after a scan.
- The Settings page shows the current settings: saving it no longer undoes titles ignored from other pages.
- Folders that did not change are not scanned again after every start.
- The wishlist, history, collections, activity and notification state are written safely, so a crash while
  saving does not empty them; the activity is read again after a backup is restored.
- The activity of the Users page is shown in the language of the interface.

### Improvements

- Searching big lists is about 15 times faster and uses much less memory.
- Settings that change no list (such as the time of the last synchronization) no longer make every page compute
  its lists again.
- Phones: the library filters scroll in one row and the overview cards are shorter.
- The selection boxes of the library show on hover and while selecting.
- Names that start with quotes or signs are sorted by their first letter.

## 1.17.0

### New features

- **Collections**: group your games in your own collections ("Favorites", "Playing", "For the kids"...) from the
  game page, and filter the library by collection.
- **Select several games in the library** to add them to a collection, remove them from one or ignore their
  updates at once.
- **Library history**: the Statistics page lists the games, updates and DLC added to or removed from your folders,
  with a chart of the number of games over time.
- **Notifications of new games**: a new option reports the games and DLC that appear in your folders, also after
  the automatic folder checks.
- **Sort the library by size or by date added**, and a filter for the games added in the last 30 days.
- **The lists remember their filters**: coming back to the library, missing updates, DLC or games from another
  page shows them as you left them.
- **"Hide demos" works everywhere**: in the library (where the filter can still show them), missing updates, DLC
  and games, statistics and notifications.
- **Messages appear as notifications** in a corner of the screen and go away by themselves, instead of piling up
  in the page.

### Fixes

- The Docker Hub page shows the screenshots and the notice about keys.
- A development dependency used to build the interface was updated (not part of the app or the image).

## 1.16.1

### New features

- **More library filters**: games or demos, region, games without a cover and games the titles database does not
  know, each with its count. They combine with the search and the other filters. Demos have a "Demo" label on
  their cards.

### Fixes

- Saving the settings no longer changes them while other parts of the app are reading them.

## 1.16.0

### New features

- **Wishlist**: mark missing games with the heart, on the Missing Games page or on the game page. The "Wishlist"
  filter shows them, the menu counts them, and the notifications tell you when a wished game is released or gets
  new DLC. Games leave the list by themselves once they are in the library.
- **Export the library as a web page**: Export > "Web page to share (ZIP)" makes a page that opens in any browser,
  without the app, with search, filters and covers. Only names, versions and covers are included; no game files and
  no keys.
- **Background hours**: a new setting keeps heavy work (cover downloads, thumbnails, automatic compression and
  scheduled checks) for the night, so the server and the disks are free during the day.
- **Update guide**: the update notice links to a page with the steps for each installation. For containers created
  with `docker run` it shows the commands with the ports, folders and variables of this container.
- **Language per user**: each user can choose the language of the interface on the Account page.
- **Activity log**: the Users page lists who deleted, organized or compressed files, restored backups, saved the
  settings or changed users.

### Covers

- Games without a cover get the icon from their own files (also NSZ and XCZ), so games missing from the titles
  database have a cover too.
- Covers missing in the US store are looked for in the stores of other regions.
- New button "Search covers again" on the Settings page, for covers that could not be downloaded.

### Fixes

- Quieter starts: covers that failed recently no longer start a cover task on each start.
- Release dates with only a year are read correctly.

## 1.15.0

### Faster starts, less memory

- The titles database (a JSON file of over 100 MB) is processed once into `titles.db` in the data folder. Later
  starts load it in about 0.1 seconds instead of 2, and descriptions and screenshots stay on disk until a game
  page shows them: the titles take about a third of the memory. The copy is made again when the titles change.

### Folder watching for big libraries

- The folder watcher remembers the folders of the library and only asks each folder for its modification time;
  only folders that changed are read again. Recently changed folders are still read file by file, so games being
  copied are not scanned half written. Empty folders no longer cause a scan.
- New setting "Check the folders every" (2, 10, 30 or 60 minutes): a longer interval lets the disks of a NAS sleep.

## 1.14.0

Made for big libraries (tens of thousands of files).

### New features

- **The library shows up right away**: covers are downloaded after the scan, in the background, and appear as they
  arrive. Before, a first scan of a big library waited for thousands of cover downloads.
- **Slow first scans show their games while they run**, every few seconds, and what was read is saved as it goes,
  so an interrupted scan does not start over.
- **Background work is visible**: cover downloads and thumbnails are tasks with their progress, and the Tasks icon
  turns while something runs.
- **Safer duplicates**: a duplicate can only be deleted once the copy that is kept was verified and is sound; the
  Space page checks only those copies.

### Faster pages

- Thumbnails are made in the background with a faster method, so pages do not wait for them the first time.
- Covers of games that are not in the library are fetched once by the server and shown as small thumbnails.
- The main lists are prepared after each scan, before anyone opens them.
- Space and Compress list the 300 biggest files of each group and offer the rest as one row; the Decompress list is
  loaded when it is opened and searched on the server. With a big library these pages went from 11-18 MB to about
  200 KB.
- Pages, styles and scripts are sent compressed (about 7 times smaller).

### Fixes

- Updates and DLC of titles without a name in the titles database take the name from the files; the kind "BASE" is
  translated.
- Space read kept files with parentheses in their names wrongly ("2).nsz"), and now deletes the copy
  ("Game(2).nsz") instead of the file with the clean name.
- Compress no longer lists files that already have a compressed copy.
- The library tile says "games with a missing update", which matches its number.

## 1.13.0

### New features

- **Space page**: old updates, duplicates and NSP/XCI originals that already have a compressed copy, with their
  sizes and the file that is kept, and how much of the library can be freed. Originals can only be deleted once
  their compressed copy is verified, files that changed since the last scan are refused, and every deletion is
  confirmed first and recorded in Tasks.
- **A new game page**: the facts span the whole header and never cut a value (the title ID can be copied), files
  and DLC are lists that work on phones, and the DLC share is a bar.

### Other changes

- Every page was reviewed on computers and phones:
  - The menu fits in every language: it is horizontal from 1400 pixels wide and opens from the menu button below.
  - Library cards keep their rows aligned, with a "No DLC" row for games without DLC.
  - Updates cards show the kind translated, a selection box on the cover and a wider ignore button.
  - Issues, Compress and Space use lists instead of tables; issues show file names instead of full paths, Compress
    has a filter and the Decompress section is folded.
  - Clearer texts on the account page when login is disabled and on the Issues and Tasks pages.
- The GitHub workflows run on Ubuntu 24.04, so the change of ubuntu-latest does not break them.

## 1.12.0

### New features

- **Verify the library**: the Issues page checks every file, NSP and XCI against the content IDs of their NCA files,
  NSZ and XCZ by decompressing them, without keys. Damaged files are listed as issues. Only new and changed files
  are checked again, and the check can run by itself every week or month.
- **Automatic compression**: new NSP and XCI files can be compressed as soon as they appear or every night at 3:00,
  with the same checks as on the Compress page. It needs your own prod.keys.
- **Required firmware**: game pages and cards show the system version a game or update needs, and warn when it is
  newer than the firmware of your console, set in the settings.
- **New version notice**: administrators see when a new version is released (asked to GitHub once a day, nothing
  about your library is sent; it can be turned off).
- **Backups**: the settings page downloads a ZIP with the settings, users, verification results and notified
  versions, and restores it. Keys and the session key are never included.
- **Install as an app**: the interface can be installed on phones and computers, with a maskable icon, shortcuts and
  a notice when the server cannot be reached. Only the app's own files are cached.
- **Four more languages**: French, German, Italian and Portuguese, including game names. Only the game names of the
  languages in use are downloaded.
- **Demo mode**: `SLM_DEMO=true` shows a made-up library to try the app without games; the README and the Unraid
  template show screenshots of it.

### Other changes

- A Content Security Policy and other security headers: the browser only runs the app's own scripts and refuses
  being framed by other sites.
- NSZ files, including update patches, were checked with the official nsz tool: it gives back the original NSP
  files byte for byte.

## 1.11.0

### New features

- **Motion**: smooth transitions between pages, gauges and charts that fill up, bars that grow, numbers that count
  up, covers that shimmer while loading and fade in, a subtle tilt of the game cards towards the pointer, and small
  animations on buttons, menus and alerts. Everything stays still when the system asks for reduced motion.

### Other changes

- Clearer texts about keys: they are always your own, dumped from your console, and never included in the app,
  downloaded or uploaded. Without keys games are recognized by their file names and compression is off.
- The README is rewritten: legal notice, quick start for Docker, Unraid and other systems, a section about keys,
  guides and troubleshooting.

## 1.10.0

### New features

- **A more visual interface**:
  - The Library page opens with an overview: games, updates, missing DLC and total size in colored tiles that link
    to their pages, and a bar with the share of games up to date.
  - Game cards get a gradient ring when hovered, an update badge on the cover and a bar with the DLC you have.
  - The Statistics page shows the space by content as a donut chart and the games up to date as a gauge; the game
    page shows the share of its DLC you have.
  - The Inter font, embedded in the app so it also works offline, soft brand colored light behind the pages, and
    cards that fade in one after the other (off when the system asks for reduced motion).

## 1.9.0

### New features

- **XCI to XCZ**: the Compress page compresses XCI files too, in the XCZ format of nsz (only the secure partition is
  kept, the one installers use), verified like NSZ files.
- **Update patches are compressed**: their patch sections are read with the counters of their subsection table, so
  updates save space as well (they were stored without compression).
- **Decompress NSZ to NSP**: a new section of the Compress page turns NSZ files back into NSP files, byte for byte the
  original, checked before the NSZ is deleted, for tools that do not read NSZ.
- **Unraid**: the app is listed in Community Applications. Search for Switch Library Manager in the Apps tab: the
  port, folders and user (99:100) are filled in and can be changed later.

### Other changes

- Removed an obsolete Windows tile configuration of the web interface.

## 1.8.0

### New features

- **Compress NSP to NSZ**: the new Compress page turns NSP files into NSZ files, usually 10 to 60% smaller (a test
  game went from 214 MB to 100 MB), installed directly by Tinfoil, DBI and other installers.
  - Every file is checked before and after: damaged NSP files are not compressed, and each NSZ is decompressed again
    and must give back every NCA byte for byte (same SHA-256) before the original is deleted.
  - Choose the level (fast, balanced, maximum) and whether the originals are deleted. Runs in the background with
    live progress in Tasks, can be cancelled, and leaves half of the processors free.
  - Written in Go, compatible with the NSZ format of nsz: nothing else needs to be installed, on Windows, Docker or a
    Raspberry Pi.
  - Games whose NSP has no ticket use the title key of a `title.keys` file next to `prod.keys`.

### Fixes

- An NSP without content metadata (for example a damaged one) is listed in Issues instead of disappearing from the
  library.

### Other changes

- The project moved to the DeLFuS77 account. Saved settings that point to the previous address are updated
  automatically, and the Docker Hub description follows the README.

### Performance

Big libraries and the full titles database are handled much faster, with less memory:

- **Scans** read up to 4 files at the same time (`scan_workers` in `settings.json`), bounded so disks and network
  shares are not saturated. The metadata of new files is cached in one transaction instead of one per file: caching
  500 new files went from 4.9 s to 0.09 s.
- **Covers** are downloaded after the scan, in parallel. Covers that failed are not retried for a day, and when the
  server cannot be reached the scan stops trying instead of waiting for every cover.
- **Pages** reuse their results until the library or the settings change. With 5,000 games and 20,000 titles, the
  Library page went from 17 ms to 0.1 ms and the navigation counts from 9 ms to almost nothing.
- **Cover thumbnails**: cards and lists load 360 px thumbnails, made once and cached on disk (about 20 times smaller),
  and every cover can be cached by the browser. Covers were decoded and encoded again on every request before.
- **Startup**: the web interface answers right away while the titles database loads in the background, and loading
  it needs about a third of the memory.
- The Issues page has a search and pages, so thousands of issues do not make one huge page.
- Watching the folders no longer keeps or sorts the list of every file.

## 1.7.0

### New features

- **Users and roles**: create accounts in the new Users page.
  - Without users the app stays open as before. Creating the first administrator requires a login from then on.
  - **Administrators** can do everything. **Read-only** users can browse the library and download files; the
    controls that change something are hidden and the server refuses those actions.
  - New login page and a My account page where every user changes their own password.
  - Passwords are stored as bcrypt hashes. Logins last 30 days and end when the password changes.
  - After 10 failed logins an address is blocked for 15 minutes.
  - API clients keep using HTTP basic authentication, now with any user. The administrator set with
    `SLM_AUTH_USERNAME` / `SLM_AUTH_PASSWORD` keeps working.
- **Tasks page**: synchronizations, scans and organize runs, with what started them (you, the schedule, a change in
  the folders...), their live progress, duration and result.
  - Warnings, such as a titles database that could not be downloaded or a notification that could not be sent, and
    errors are shown with their reason.
  - Failed tasks stay until they are dismissed, and the navigation shows how many there are.
  - New API endpoints `/api/tasks` and `/api/tasks/events` (server-sent events).

### Other changes

- The project now has a license: the changes made in this repository are MIT licensed. The projects this fork is
  based on did not publish a license; see NOTICE.md.

## 1.6.0

### New features

- **Automatic scan**: games copied to, removed from or replaced in the library folders show up by themselves.
  - Local folders report changes right away.
  - Every folder is also checked every two minutes, which covers network shares and Docker volumes that do not
    report changes, and changes made while the app was stopped.
  - A folder is only scanned once it stopped changing, so files still being copied are not read half written.
  - Can be turned off in Settings.
- **Library filters**: status filters with counts (all, update available, DLC missing, up to date), a file format
  menu, and a switch between large and small covers that the browser remembers.
- **Setup checklist**: while the library is empty, the Library page lists the steps to set it up (titles database,
  prod.keys, game folders, scan), shows which are done and links to where each one is done. Folders that cannot be
  read are named.

### Improvements

- **Titles database downloads**:
  - Downloads are written straight to disk instead of being kept in memory, which helps small devices such as a
    Raspberry Pi.
  - Truncated downloads are rejected, and server errors or dropped connections are retried before the next mirror
    is tried.
  - When the database did not change, it is not processed again, so most synchronizations are much faster.

## 1.5.0

### New features

- **Redesigned interface**: a new look for every page, in light and dark themes.
  - Game cards show the status of each game: update available, missing DLC or up to date.
  - The navigation shows how many updates, DLC and issues are pending.
  - Every page has a header that explains it, and empty pages say what to do next.
  - The game page has a large header with the cover and the key facts.
  - Settings are split into sections with a save bar that is always visible.
  - The Synchronize button spins while a synchronization runs, and its progress floats in a corner instead of moving
    the page.
  - Two columns of games on phones, a skip link, visible keyboard focus and reduced motion when the system asks
    for it.

- **Download all**: the game page downloads the base game, its updates and DLC as one ZIP archive. Files are stored
  without compressing them again, so large games download at full speed without temporary files.
- **Bulk actions**: select several missing DLC or games with missing updates and ignore them at once.
- **Documented API**: `/api/openapi.json` describes every endpoint, and `/api/statistics` returns the numbers of the
  Statistics page, e.g. for a Home Assistant REST sensor (example in the README).

### Fixes

- Covers of your games come from the local image cache on every page, so they are shown without access to the
  Nintendo servers; covers that cannot be loaded show the placeholder instead of a broken image.

## 1.4.0

### New features

- **Statistics page**: number of games, updates and DLC, total size, space by content and by file format, games up to
  date, missing updates, DLC and games, issues and the largest games.
- **Notifications** after every synchronization when an update or a DLC of one of your games becomes available: Discord
  webhook, Telegram bot or a generic JSON webhook (ntfy, Home Assistant, n8n, ...). Only new items are reported; the
  settings page can send a test notification.
- **Light theme**: choose light, dark or automatic (follows the system) from the menu. Dark stays the default and the
  choice is remembered by the browser.

### Fixes

- Docker: the container no longer stops when files inside the data folder are mounted read-only (e.g.
  `-v /path/prod.keys:/usr/local/share/switch-library-manager-web/prod.keys:ro`); it explains when the data folder is
  not writable by `PUID`/`PGID`.
- Adding, removing or updating prod.keys rescans the library on the next start, so games that could not be read
  before appear without a manual synchronization.
- The web interface answers right after start; the initial library scan runs in the background with its progress.
- Running without prod.keys is reported as a warning with a hint where to put the file, instead of an error.

## 1.3.0

### New features

- **Docker Hub**: the image is published as `delfus77/switch-library-manager-web`
  (also still on `ghcr.io/delfus77/switch-library-manager-web`), for **amd64 and arm64**, so it runs on Raspberry
  Pi and most NAS as well.
- **Export the library** from the Library page: a spreadsheet (CSV, opens with accents in Excel) or a full inventory
  with files and DLC (JSON).
- **Game names and descriptions in Spanish** when the interface is in Spanish and the Nintendo eShop has them. The
  search finds games by their Spanish and their original name. The export keeps the original names.

### Fixes

- Dates are shown in the format of the interface language ("Oct 5, 2026" / "5 oct 2026").
- Issues are translated to Spanish as well; file paths and technical details are kept.

## 1.2.0

> **Docker users:** the container now runs as an unprivileged user (`PUID`/`PGID`, default `1000`). The data folder
> is given to that user automatically; your library folder must be writable by that user to organize files.

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

### Security

- The Docker container no longer runs as root.
- The build tooling no longer uses Gulp and its unmaintained plugins (32 known vulnerabilities, 1 critical); the web
  assets are built with sass and esbuild and `npm audit` reports no vulnerabilities.
- Dependabot keeps Go modules, npm packages, GitHub Actions and the Docker base image up to date.

### Other

- Docker health check on `/healthz` (works with password protection) and a `docker-compose.yml` example.
- CI builds the Docker image and checks that it starts healthy and unprivileged.

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
