# Switch Library Manager Web

Easily manage your Switch game backups from the browser.

## Features

- Cross platform, works on Windows / Mac / Linux and in Docker
- Web interface
- Scan your local Switch backup library (NSP/NSZ/XCI/XCZ and split files)
- Read title ID / version by decrypting NSP/XCI/NSZ, including titles for recent firmware (requires prod.keys)
- Without prod.keys, fall back to the file name (example: `Super Mario Odyssey [0100000000010000][v0].nsp`)
- List missing games, missing updates (for games and DLC) and missing DLC
- List issues: unsupported, duplicate, old or unreadable files and updates/DLC without base game
- Organize games in folders and rename files, with a preview before anything changes
- Delete old update files and, optionally, duplicates and empty folders
- Ignore lists for DLC, updates and file types; hide demos
- JSON API to list and download the library (`/api/titles`)
- Optional password protection
- Zero dependencies, all crypto operations implemented in Go

## Usage

### Docker

```
$ docker run -d \
	--name switch-library-manager-web \
	-v /home/johndoe/switch-library-manager-web:/usr/local/share/switch-library-manager-web:Z \
	-v /home/johndoe/Backups/Switch:/mnt/roms:Z \
	-p 3000:3000 \
	ghcr.io/DeLFuS77/switch-library-manager-web
```

Volumes inside the container:
- `/usr/local/share/switch-library-manager-web`: data folder (settings, caches, images, prod.keys)
- `/mnt/roms`: your library

Then open http://localhost:3000. On the first start the titles database is downloaded automatically.

### Binary

Build the binary for your platform (see [Building](#building)) and run it. The data folder is the folder of the binary, or the
folder set in the `SLM_DATA_DIR` environment variable. Set the folders to scan in the Settings page.

## Keys (optional)

A prod.keys file lets the app read the metadata of your files, so they are classified correctly even when the
file names are wrong. Only `header_key` and the `key_area_key_application_XX` keys are required.

The keys are looked up in this order:
1. The path set in the Settings page: a folder containing `prod.keys`, or the path to a `.keys` file
2. `prod.keys` in the data folder
3. `~/.switch/prod.keys`

## Organize

The Organize page moves and renames files according to its options. Every action shows the exact list of changes
first; nothing happens until you apply them. Existing files are never overwritten and files that changed since the
last scan are not deleted. Split files are left untouched.

Templates for folder and file names support:
- `{TITLE_NAME}` - game name
- `{TITLE_ID}` - title id
- `{VERSION}` - version id (only applicable to files)
- `{VERSION_TXT}` - version number, like 1.0.0 (only applicable to files)
- `{REGION}` - region
- `{TYPE}` - `BASE`, `UPD` or `DLC`
- `{DLC_NAME}` - DLC name (only applicable to DLC)

Templates must contain `{TITLE_NAME}` or `{TITLE_ID}`.

## Password protection

Set both environment variables to require a user name and password (HTTP basic authentication) for every page and
API call:

```
SLM_AUTH_USERNAME=admin
SLM_AUTH_PASSWORD=choose-a-password
```

With Docker, add `-e SLM_AUTH_USERNAME=... -e SLM_AUTH_PASSWORD=...`. Use HTTPS (for example a reverse proxy) when
the app is reachable from outside your network, since basic authentication sends the password with every request.

Requests that change data (synchronize, settings, organize) are rejected when they come from another web site.

## Settings

Most settings are available in the web interface. `settings.json` in the data folder also contains:

| Setting | Description |
|---|---|
| `port` | HTTP port, default `3000` |
| `debug` | Verbose logging, useful when reporting issues |
| `titles_json_url` | Titles database. Default: the `data` release of this repository |
| `versions_json_url` | Versions database. Default: [blawar/titledb](https://github.com/blawar/titledb) |

If the configured database cannot be downloaded, a mirror is used.

### Titles database

`titles.json` and `versions.json` are generated every 6 hours from [blawar/titledb](https://github.com/blawar/titledb)
by the [Update title data](.github/workflows/update-title-data.yml) workflow and published in the `data` release.

## Reporting issues

Please set `debug` to `true` in settings.json and attach the log to allow for quicker resolution.

## Building

Requirements: [Go](https://go.dev) 1.23+ and [Node.js](https://nodejs.org) (for the web assets).

```
$ git clone https://github.com/DeLFuS77/switch-library-manager-web.git
$ cd switch-library-manager-web
$ npm ci
$ npx gulp
$ make build          # Linux
$ make build-windows  # Windows
$ make build-mac      # macOS (Apple Silicon)
$ make test
```

The binaries are written to `build`. The web assets are embedded in the binary, so `npm ci` and `gulp` must run
before building. Without `make` (e.g. on Windows), run `go build -o build/switch-library-manager-web.exe .`

#### Thanks

- This program is based on [giwty's switch-library-manager](https://github.com/giwty/switch-library-manager) and
  [dtrunk90's switch-library-manager-web](https://github.com/dtrunk90/switch-library-manager-web)
- Parsing, organizing and title data fixes from [trembon's switch-library-manager](https://github.com/trembon/switch-library-manager)
- Title data from [blawar's titledb](https://github.com/blawar/titledb)
