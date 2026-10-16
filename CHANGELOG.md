# Changelog

## 1.39.1

### Improvements

- The page of the start looks like the app: its mark and colours, the steps with a check once done and a striped bar,
  in the theme chosen in the app.

### Fixes

- The app could stay for minutes on the page of the start ("Preparing the pages...") on a large library on a NAS: it
  first walked every file of the folders to watch them. That walk is now done in the background, after the app opens;
  it was also the cause of the minute it took to answer after an update or a restart.
- Tasks: the list of the files a running compression has done no longer jumps back to the top every second while it
  is scrolled; it stays where it was each time the progress is drawn again.

## 1.39.0

### Improvements

- The app answers as soon as it starts: while it gets ready after an update or a restart, every page shows "Starting
  the app..." with what it is doing (opening the local database, loading the keys, preparing the pages) and opens by
  itself once it is ready, instead of failing to load. The health check says it is not ready until then.
- On the widest screens (1700 pixels and more), the search of the header is a small field with its shortcut instead of
  the magnifier alone, in the room the menu leaves.
- Tasks: each file being compressed, decompressed or converted says which format it becomes ("NSP → NSZ",
  "XCI → XCZ", "NSZ → NSP", "XCI → NSP").
- Statistics: the bars look like those of the tasks, thin and round, in the colours of the library one after the
  other and with stripes; the bars of the years of release too.
- Searches of the pages: emptying the field, with its cross or by deleting the text, shows everything again at once,
  so the separate "Clear" link is gone.

### Fixes

- Tune-up: the page could take more than ten seconds to open on a NAS, while it asked the disk whether every NSP and
  XCI had a compressed copy next to it; it now looks among the files of the last scan and the copies written since.
- Compress: the "All" chip counts every file to compress, also the smaller ones listed together in the last row (it
  said 300 while 1,787 were selected), and a note says the biggest files are listed one by one.
- "Hide demos" hides the demos whose name ends with the word ("… Demo", "… - Demo", "…-DEMO", "… Demo for KIOSK",
  "… DEMO EDITION", "… DEMO!"), says it in brackets ("【DEMO】", "(Demo!)") or is a special demo ("Special Demo
  2020"): the titles database does not flag many of them, and about a hundred showed in a large library. They are
  left out of the recommendations too. Names such as "100 Demon Fantasia" are not taken for demos.

## 1.38.0

### Improvements

- The cards of the favorites have a golden aura, and those of the games on the wishlist a red one: a light that goes
  round their edge and a glow that breathes, so they stand out at a glance. It stays still when the system asks for
  less motion, and comes and goes with the star or the heart.
- Game page: the title ID copies itself on a click, with the clipboard icon next to it (a check for a moment once
  copied), and so do the IDs of the DLC; the separate "Copy title ID" button is gone.
- Compress: the list of files is colored by kind (game, update, DLC), each file with its format and a bar of its size
  next to the biggest one, and chips at the top show only the files of a kind; "Select all" picks the files shown.
- The Issues icon of the header shows, under the mouse, a small panel with the kinds of issues left to solve and how
  many files each has, in the colors of the cards of the Issues page.
- Starting a check, a compression or decompression, a pack, a new search of covers or a copy to the SD card no longer
  takes you to Tasks: you stay on the page, a note says it is running with its progress and a link to Tasks, and the
  page updates by itself when it ends.

### Fixes

- Organize no longer shows the same changes after applying them: the folder of a game is created only when a file moves
  into it (a file of two games stays in the folder of the first one, and the folder of the second was created and
  deleted as empty on every run), so the tune-up marks Organize as done.
- A file that holds the game besides an old update (a game card with its update) is no longer listed as an old update
  in the cleanup, Issues and Space: it is never deleted, and it showed as skipped on every run.
- The deletion of empty folders shows in the previews as a row of its own, "Empty folders", not as a deletion of ".".
- A game read by an older version that was left without any name of its own (its Simplified Chinese name lost) is
  read again with the next scan also when the titles database knows it, e.g. with a placeholder name such as "-".
- Space: the legend and the bar count what can be freed of each group, with the whole group after it when part of it
  cannot go; "0 B can be freed" sat next to "Old updates: 9.0 GB".
- Issues: after deleting the damaged files or the files that cannot be read, the page showed them until it was
  reloaded by hand; it now updates once the scan after the deletion ends.

## 1.37.0

### Improvements

- Organize: the preview of the changes is a list colored by kind (move, rename, create folder, delete, skip, error),
  each file with its name first and its folder below, and the new name and folder after an arrow. A summary at the top
  counts each kind and shows only its changes with a click. A move that changes the title ID of the name is marked
  "Review", and a name whose version changes says so.
- Cleanup: of two copies of the same content, the compressed one (NSZ, XCZ) is kept when it was checked, is sound and
  holds the same contents, and the NSP or XCI goes, whichever the scan found first.
- Names: a game the titles database knows only from the Japanese, Korean or Chinese store, or only by a placeholder
  such as "-", shows the name it gives itself in the language of the interface or in English, when it has one
  (Japanese, Korean and Chinese keep the name of the database).
- Statistics: the share of the games in the space counts them as the library does, so both numbers match.
- Space: the bar under what can be freed shows how it splits between old updates, duplicates and compressed originals,
  in the colors of the legend; as a share of the whole library it was often too thin to be seen.
- Issues: the cards of the summary are sized to their content (they all stretched to the tallest one), with the count
  large, the icon in the color of the kind and the actions at the bottom. Damaged files and files that cannot be read
  can be deleted from their card, with their size and a confirmation; the library is scanned again after.

### Fixes

- Organize: a file holding several contents (a collection of several games, an update with its DLC) that already had
  the name of one of them was renamed after another, and back on the next run (Etrian Odyssey II HD became III HD).
- A game that names itself in Simplified Chinese only (games of the Chinese store) had no name and was not recognized
  as a demo: its name was lost under the empty Brazilian Portuguese one. The files the titles database does not know
  are read again once with the next scan.
- Organize: the note about files kept until the copy that stays is checked showed "0 files" when there were none.

## 1.36.0

### Improvements

- Statistics: the history of the library has three charts, Content (games, DLC and updates), Space (the library, the
  disk used and the size of the disk) and Saved (the bytes compressing saved), with the values of the day under the
  pointer or the finger. Below, how much the library grows a month and, when it keeps growing, around which date the
  disk would be full at this rate. The free space of the disk and the bytes saved are recorded from this version on.

## 1.35.0

### Improvements

- A warning of disk space: when a disk of the library has less than 10% free, every page says so at the top, with its
  free space and a link to the Space page, and a notification is sent (Discord, Telegram or webhook) once, until the
  disk has room again. Automatic compression waits until a file's disk has room for its compressed copy, and a file
  that failed for lack of space is tried again once there is room.
- Tune-up: Organize and Free space no longer stay on "Looking…" while the library changes (during a check of a large
  library they were worked out again with every file, never finishing). They show their last figures, marked
  "(updating…)", while new ones are worked out; Free space follows the results of a running check as they are saved,
  every two minutes. The Space page does the same, with a note, instead of waiting.
- Tune-up: "Review again" scans the library (only new or changed files are read) and works every step out again, for
  after changing files outside the app; the page says when it was last reviewed and updates itself while the figures
  of Organize and Space are worked out.
- Issues: the games without their base game have a heart to add the game to the wishlist at once, so it shows on
  Missing games and in the notifications.
- The Tools menu shows a dot while a step of the tune-up worth doing is waiting (files never checked, changes of
  Organize, space that can be freed), and so does the Tune-up in it.
- Issues: a summary at the top with a card for each kind of issue found (damaged files, keys too old, files that cannot
  be read, updates and DLC without their game, duplicates, old versions, files recognized by their name only, files
  that are not games), with how many files, what it means and what to do, a button that shows only its files and a
  link to where it is solved. The card of the updates and DLC without their game lists the games with the most of them,
  each with its name and the ID of its base game, which a click copies to look for it.

## 1.34.0

### Improvements

- Tasks on files (check, compression, decompression, conversion, pack): how many files are reached out of how many is
  shown in a place of its own, next to "Total", instead of after the name of the file, where it moved with each name.
- Pages of cards fill their last row: the cards per page are rounded up to whole rows of the columns your screen shows
  (24 become 25 on 5 columns, 28 on 7), remembered by the browser. The per page menu keeps the value you chose.
- More files that are never games are left out of the Issues page by themselves: downloads left halfway (part,
  partial, tmp, crdownload, download, aria2, !qb, !ut), backups (bak), documents (pdf, rtf, csv, diz), shortcuts (lnk),
  playlists (m3u), settings (cfg), more checksums (sha512, crc, par2) and svg images. Archives and loose parts of games
  are still listed, as they may hold games. The setting of the ignored types says which ones are left out already.
- Settings › Ignored items › "Count a game once when you have it from several regions": the library shows one card for
  such a game, the copy the Space page suggests to keep, with how many other regions you have, and the counters, the
  filters and the statistics count it once. Off by default; no file is changed.

## 1.33.0

### Improvements

- DLC: each missing DLC is a card with its own picture, its release date and its size, with its checkbox and its
  Ignore button, under the cover of its game.
- Tasks: the total bar of a running task goes from red to green as it advances, through orange and yellow, so it stands
  apart from the bars of the files; the progress of Organize too.
- Every check of a file is kept and used everywhere: a compression reads its original whole, and that is now its check
  (the original and the NSZ count as checked); a decompression, a conversion and a pack check the file they write whole,
  and that counts as its check too. The next file check does not read those files again.
- Organize names a game that has no English name, in the titles database or in itself, by the name it gives itself in
  another language (Latin letters first, then Japanese, Korean or Chinese) instead of by the name of its file; a
  placeholder name such as "-" of the titles database does not count.

### Fixes

- Game pages show their cover and banner from the server, like the cards, and a cover or picture that cannot be loaded
  (a network that blocks Nintendo, a cover gone) shows the placeholder instead of a broken image.
- Demos the titles database does not know (of the Chinese store, for example) are hidden with the others: the app reads
  the demo flag the game itself carries, whatever its name or store. Only the games the titles database does not know
  are read again once for it, not the whole library.
- A game whose own name in a language is only a placeholder such as "-" (Chinese games fill so the languages they do
  not have) shows its real name, in another language, instead of "-".
- Settings is at one click again: a gear next to the account menu of the main bar, for administrators (it is still in
  the account menu too).
- Tasks: the stripes of the bars move again, and smoothly: an old rule meant to remove their entrance animation stopped
  them, and they now slide on a layer the graphics card moves, instead of a background that stuttered while the page
  was redrawn.

## 1.32.0

### Improvements

- Tune-up: a page, first in the Tools menu, with the five steps to put a library in order in the order they depend on
  each other (synchronize, check the files, organize, free space, compress), each with what is left to do (files never
  checked, changes of Organize waiting, space that can be freed, files not compressed) and the first one waiting marked,
  with its button. Organize, Space and Compress say which step they are, with a link to it.
- The cover of a game card opens the game too, not only its name.
- Organize: when the cleanup keeps files because the copy that stays is not checked yet, it says how many and offers to
  check those copies at once.

### Fixes

- Game pages: the screenshots are fetched once by the server and shown from it, like the covers, instead of each
  browser loading them from Nintendo; when they cannot be loaded at all (networks that block Nintendo block its images
  too) a note says why instead of broken images.
- Organize: a file whose name has no title ID got a folder named after the file with its extension, which could not be
  made next to it ("not a directory"); the extension is left out now. A file whose copy of the same size is already where
  it would go is left where it is as a duplicate, instead of an error, and a different file with the same name gives a
  clearer message.

## 1.31.0

### Improvements

- The main bar is ordered by what it is for: the library (Library, Updates, DLC, Missing games), a Discover menu
  (Upcoming, Series, Statistics) and a Tools menu (Organize, Compress, Space, SD card planner, Save backups), each page
  with a line that says what it is for; on the right Issues and Tasks, then search, synchronize and the account menu at
  the end, with My account, Settings, Users, Diagnostics and the theme (a gear without login). The side menu of phones
  and tablets shows the same groups as sections.
- Settings: its sections are grouped as Your library, How it works, Extras and Maintenance.
- My account: each user chooses the colour of their icon among ten, or keeps the colours of the app; the icon shows it
  in the menu, on the account page and in Users. Read-only users can choose it too.
- The theme (light, dark or automatic) moved out of the main bar, which is emptier: it is in the menu of the user and
  in Settings › General.

### Fixes

- Docker: the container starts in seconds again. On every start it went through every file of the data folder (covers,
  thumbnails, caches: tens of thousands in a big library) to give them to PUID/PGID, which took about two minutes on an
  Unraid user share before the app even started. It is now done once for each owner, remembered in the data folder,
  and done again only when PUID or PGID change; the first start after this update still does it once.
- The tasks icon of the menu stays on the line of the other icons while it turns: only its glyph turns.

## 1.30.3

### Fixes

- File check: the results follow the files. A file moved or renamed by Organize keeps its result instead of being
  checked again as a new file, a file deleted by a cleanup loses it, and the NSZ, NSP or XCZ made by a compression,
  decompression or conversion gets the result of its original, as it was checked byte by byte against it. The files
  moved by older versions get their results back at the next check of the whole library, when exactly one file has the
  same size and modification time as a result left behind.

### Improvements

- Tasks: the bar of each file being worked on takes one of the colours of the library overview, always the same for
  the same file.

## 1.30.2

### Improvements

- File check: the task says why its files are checked: how many are new, changed, checked again once after an
  improvement of the check (the files found modified by a tool, since 1.30.0) or checked again as asked.
- Synchronization and scans: while the folders are walked the bar goes up to the number of files of the last scan
  instead of staying full, and its steps are translated ("Looking at the folders", "Reading the new file").
- Every progress bar of a running task looks the same, striped and moving like the one of the synchronization: the
  total, the line of each file and the progress of Organize.
- Disclaimer: the app is provided without warranty and its authors are not responsible for lost or deleted files. A
  page linked from the footer of every page and from the setup wizard, a section of the README and a line of the
  Unraid template say so; keep a backup of your games.

## 1.30.1

### Fixes

- Library: the numbers of the overview match the filters and the menu. The games card no longer counts the demos the
  library hides; "Games up to date" counts as the "Up to date" filter (every update and DLC); the updates card shows the
  same number as the menu, the updates of the Updates page, with how many are of games and of DLC. The Statistics page
  counts the same way.
- Demos of the Chinese (试玩版, 体验版) and Korean (체험판) stores are recognised and hidden with the others, also when only
  the game itself names them so.
- A game whose name in the titles database is a placeholder such as "-" shows the name it gives itself.
- The icon of the Synchronize button turns around its centre while the library is synchronized or scanned, like the
  tasks icon of the menu, instead of drifting a little.
- Issues: the two buttons of the file check say what they do, "Check new files" and "Check everything again", with a
  line that explains the difference (the first one also goes on where a stopped check was). They fit a phone.
- Space: the page opens at once. What it shows (each original compared with its compressed copy, every file looked at
  on the disk) is prepared after each scan and kept until the files or the checks change; when it is not ready yet,
  the page says it is looking at what can be freed and opens by itself when it is ready, instead of seeming stuck.
- Organize: applying the changes shows its progress instead of a page that seems stuck: a bar with the game or file
  being moved or deleted and how many are done, and a link to the task, which goes on if you leave the page.

## 1.30.0

### Improvements

- Settings › Compression › "Measure this server": for about half a minute it compresses parts of the biggest games of
  the library with several settings (files at once × threads), as a real compression reads, decrypts, compresses and
  writes them, into a hidden file deleted at the end, then shows the speed of each and chooses the fastest. It works
  the same on a PC, a NAS or in Docker, where the kind of disk cannot be known; the processors are read from the
  server. No scan or compression starts while it measures.
- XCZ files are compressed in blocks of 1 MB, as nsz does by default: a reader can start anywhere in the game without
  decompressing it whole, and the blocks are compressed by every thread at once. A block that does not shrink is
  written as it is. NSZ files stay in one stream, which saves a bit more. Checked with the decompressor of nsz.
- Compression settings: the processor threads that compress each file (automatic as before, any number up to every
  processor) and the files compressed, decompressed or converted at the same time (1 to 4), each with its line on the
  Tasks page. Automatic threads are shared among the files at once: the disk sets the speed more than the processor
  (on a game of 16 GB, two files at once with 2 threads each took 27% less time than one after the other, while 24
  threads on one file were slower than 4). Files written at once each keep their free space, so none starts without
  room to finish.
- File check: the files with NCAs rewritten by a tool (found "modified" but intact) are checked again once. Before
  1.28.5 the hashes of the sections past 4 GB of an NCA were not read, so such a big game could pass without its data
  being checked; until it is checked again it does not count as a sound copy for a cleanup. The other files keep their
  result, as they matched the SHA-256 of their content IDs.
- No library scans while a task changes the files (a compression, decompression, conversion, pack, organizing or a
  cleanup): the folder watcher and the scheduled synchronization wait for it to end. A compression of several games
  used to start a scan after each one; now the library is scanned once, when the task ends, and the watcher does not
  scan the same changes again.

## 1.29.0

### Improvements

- Library: the total size card is green, so each card of the overview has its own colour (missing DLC stays cyan).
- Compression: each file being worked on says its step ("Calculating hashes", "Compressing", "Verifying", step 2 of 3),
  and so do decompressions, conversions and packs. A finished compression shows a bar in two colours, what the files
  take now and what was saved, for the whole task and for each file.

### Fixes

- The spinners of running tasks no longer start again with a jump each time the tasks page refreshes, and the tasks
  icon of the menu turns around its centre without wobbling.
- Tasks: long file names are shortened on a phone instead of widening the page.
- Tasks: the list of files compressed no longer folds again by itself a moment after opening it, when the page
  refreshes the tasks.

## 1.28.5

### Improvements

- Tasks: a compression lists each file with its size before and after and the share saved (open while it runs, folded
  in the history), and its total says the share saved too. The progress of compressions, decompressions, conversions
  and packs is shown in sizes instead of bare numbers.

### Fixes

- Compression of games bigger than 4 GB: the sections of an NCA that lie past 4 GB were not seen, so the game was
  refused with "nothing in this file can be compressed" (or not compressed at all). They are now read like any other,
  and the NSZ of such a game is decompressed by nsz and matches the original. The file check also looks at the hashes
  of those sections again.
- Compression: games whose NCAs use AES-CTR sections without layer hash (type 5), have gaps between their sections or
  a section the app cannot decrypt were refused with "nothing in this file can be compressed". Like nsz, those parts
  are now kept in the stream as they are and the rest is compressed; the NSZ is still decompressed and compared with
  the original before it replaces it. When a file still cannot be compressed, the message says why for each NCA.

## 1.28.4

### Fixes

- File check: each file being checked moves on while an NSZ is decompressed, instead of staying still until its end,
  and the task tells it is preparing the list of files at the start, now made faster on a NAS.
- The log no longer fills with errors for updates whose date the versions database does not know, nor with a line for
  every game that has updates or DLC without the game (they are listed on the Issues page).

## 1.28.3

### Improvements

- **Tasks show their workers**: a running task tells how many files it works on at the same time (file checks) or
  with how many threads (compression), and under the total bar, a line for each file being worked on with its own
  progress.
- Every bar of a running task moves on smoothly, the ones of each file too; the cards of the sagas and of the save
  backups come in one after the other, like the library.

### Fixes

- A file whose check could only cover part of it (an update rewritten by a tool, whose romfs cannot be checked on its
  own) is no longer enough to delete another copy for it, and an old update or DLC is only deleted while its newer
  version is still there and was not found damaged, also by the previous check.
- Deleting from the Space page or the copies of other regions holds off every compression, check, pack and copy until
  it ends; a copy to the SD card no longer starts while files are organized.
- Damaged or crafted NSZ and XCZ files are refused (sections that leave holes or overflow, blocks bigger than their
  size, counts beyond the file) instead of reserving memory from what they claim.
- Sagas: "Trial of Mana" and other games with Demo or Trial inside their name are games again; a collection IGDB no
  longer knows is no series; the series are given in the same order every time.
- Save vault: it refuses LOCK requests, deleting or moving a whole folder of a game and names with ":", and stops an
  upload without a declared size before the disk is full.
- The install button of the app prompts only once on a double tap.

## 1.28.2

### New features

- **App on your phone**, in Settings: an **Install the app** button where the browser allows it (Chrome, Edge and
  Samsung Internet), a code to open the server on the phone, and the steps for Android, iPhone and iPad (iOS 26 and
  earlier) and computers, the ones of the device first. It tells when HTTPS is needed to install it as an app, and how to reach the
  app from outside your home: a VPN, a Cloudflare Tunnel or a reverse proxy with a dynamic DNS.

### Improvements

- Unraid template: the license is AGPL-3.0 (it still said MIT) and its screenshots are the current ones.
- The Tasks page tells that tasks run on the server: the browser or the device can be closed, they only stop when the
  app itself stops.

## 1.28.1

### New features

- **The page of each saga**: the name of a saga, its button **See the series** or the series on a game page open
  the page of the series, with all its games in big cards, the oldest first: the ones you have and the ones you are
  missing.

### Improvements

- Sagas never list demos (also those the titles database does not flag, such as "Demo for KIOSK"), nor count them.

## 1.28.0

### New features

- **Sagas from IGDB and Wikidata.** The series of the games now come from people who keep them: from IGDB (its
  collections, with the keys of Times to beat) and from Wikidata (no keys needed, updated with the titles data), with
  the name of the series in your language. The names and publishers only decide for the games neither knows, and an
  edition, a demo or the copy of another store joins the series of its game.

### Fixes

- **Deleting old updates or duplicates no longer deletes a file that still holds a game.** A file with several
  contents (a complete pack, a card with its update or a DLC) was deleted whole when one of its contents was older or
  a duplicate, also by the automations; it is now kept and listed as such, here and on the Space page.
- The cleanup of Organize and of the automations follows the rules of the Space page: a duplicate is only deleted when
  the copy that stays was checked and is sound, and an old update or DLC is kept when its newer version is damaged.
- Space page: an original is only deleted when its compressed copy holds every content of it, not only when a sound
  copy of the same name exists.
- Copies of other regions: one is only deleted while a checked, sound copy from another region stays (two games of
  the same name and region are different games), and files that also hold another game stay.
- Converting a card to NSP checks that every content of the card is in the NSP before the card can be deleted.
- Save vault: an upload cut halfway no longer counts as a backup, so it can never push a good backup out.
- Organize: a file with several DLC (or an update and DLC) is moved once instead of failing for the others; names are
  kept within the 255 bytes of Linux file systems (the names of the game and DLC are shortened, never the title ID or
  the version), names that Windows does not allow (such as "Con") get an underscore, and a pack is named with the
  update it holds, not with a newer one in another file.
- Organize and cleanup no longer run while a compression, conversion, pack or file check works on the files, and
  those tasks do not start while files are organized.
- Scanner: a DLC whose index is 0x800 is no longer taken for an update (the kind of a content comes from its metadata,
  or from the group digit of its title ID), and an older DLC replaced by a newer one is listed as an old file instead
  of vanishing from the library.
- **Sagas: games of another series are no longer mixed in.** A series is now a name and its publisher: "Kingdom Two
  Crowns", "Kingdom Hearts" and "Kingdom Rush" are three series, not one "Kingdom", and a word many games begin with
  ("Escape", "Hidden", "Cat"...) is no series; games of one publisher that begin alike ("Sonic Forces", "Sonic
  Frontiers") are, when that word belongs to that publisher.

### Security

- Save vault: even with the right password, it only keeps backups (ZIP files) in the folder of JKSV, refuses the
  WebDAV requests JKSV does not make, listings of the whole vault at once, and uploads that would leave less than
  2 GB free on the disk.

## 1.27.1

### Fixes

- **File check: no more good files reported as damaged.** NSZ and XCZ files made by nsz are now read in every form it
  writes: updates split in many sections, block compression whose last block is stored as it is, and a first section
  that does not start right after the header. Updates compressed by the app itself no longer fail their check either.
- **Files rewritten by NSC_Builder and similar tools** (title rights removed, cards converted to NSP) are no longer
  reported as damaged: when an NCA does not match its content ID, the hash tree of its data is checked, and the file
  is only damaged when that fails. Files found damaged by the previous check are checked again by themselves.
- Compression: every file of the NSZ (also tickets and certificates) must match the original, and none may be missing.

## 1.27.0

### New features

- **Complete packs**: from a game page, put the game, its latest update and all its DLC in one NSP (or NSZ when a
  part is compressed), to copy and install everything at once with DBI, Tinfoil and other installers. The delta
  fragments of the update, never used when installing from a file, are left out. The pack is checked before it gets
  its name, and you always choose whether the separate files are kept or deleted. The library reads the pack as the
  game with its update, DLC and cover.
- **Save vault for JKSV** (off until you turn it on in **Settings > Save vault**): the app becomes a WebDAV server for
  JKSV, the save manager of the console, with its own user and password. Every backup is matched to its game by the
  title ID inside it: a **Save backups** page with the history of each game, downloads, deletions and the games of
  your library without a backup; the latest backups on each game page; how many backups to keep per game; a message
  for every new one; and the webdav.json file for the SD card made for you.
- **The same game from several regions**: the copy suggested to keep is never a damaged one and prefers one already
  compressed, and each other copy can be deleted from the **Space** page with its updates and DLC, after a
  confirmation, only while another copy of the game stays.

### Fixes

- Game pages: the Collections menu is no longer cut by the header of the game.

## 1.26.1

### Improvements

- **Setup wizard**: every step has its own animated picture (a console with its controllers, folders full of games,
  a key and a shield, the moon and a clock, a file that shrinks, gears, a phone with messages, a trophy…), and
  Settings has a clear **Open the wizard** button.
- After the wizard saves, the covers of the games it finds are downloaded at once, also outside the background hours.
- Game pages: the title ID, the languages and the times to beat are no longer cut.
- Your year on Switch: the chart of the months fills its card.
- Statistics: the history shows the latest ten changes, the older ones on request.

### Fixes

- **No more sideways scrolling on desktop**: the menu of wide screens always fits, in every language (only the
  magnifier of the quick search next to it).

## 1.26.0

### New features

- **Setup wizard**: on the first start, a friendly step-by-step guide walks you through every setting (language and
  theme, your folders checked as you type, keys, console firmware, library options, background hours, compression,
  automations, notifications, times to beat and a first administrator), with a summary before saving. Every step can
  be skipped, and the wizard can be put off or turned off. Run it again from **Settings** or the quick search: each
  step shows how it is set now.
- **Automations** (off until you turn them on in **Settings > Automations**): new files are checked, compressed
  (except damaged ones), the updates they replace deleted and the library organized, one step after the other, with a
  message of what was done. Optionally only within the background hours; the queue survives a restart.
- **Your year on Switch**: a page that sums up what came to your library in a year (games, updates, DLC, space, the
  busiest month, your favorite genre and publisher, the biggest, oldest and newest game, your most complete series),
  ready to download as an image to share. From **Statistics** or the quick search.
- **Version history** on every game page: each update with its date, the one installed, and a button to look up what
  is new.
- **Times to beat** (optional, from IGDB): with your own free Twitch application, game pages show the quick, story and
  100% times, the library can be sorted by time to beat and Statistics adds up the hours of your games.

### Improvements

- The Tasks icon of the menu stops turning as soon as the last task ends or is cancelled, without reloading the page.
- The README, the Unraid, Synology and Portainer templates and Docker Hub describe every feature.

### License

- From this version the changes made in this repository are published under the **GNU AGPL-3.0**: the app stays free
  and open source, and nobody can turn it into a closed or paid product without sharing its source. Versions up to
  1.25.2 were published under the MIT license.

## 1.25.2

### Fixes

- **A weak `SLM_AUTH_PASSWORD` no longer stops the app.** Since 1.25.0 a password shorter than 10 characters or very
  common in `SLM_AUTH_PASSWORD` kept the container from starting. It now starts and the password still logs in, so
  nobody is locked out; the log and a notice on the pages of the administrators ask to change it to a stronger one.

## 1.25.1

### Improvements

- The app is free and will stay free. If it saves you time, you can now support its development with a tip on
  [Ko-fi](https://ko-fi.com/delfus77): a small **Support** link with a heart in the footer of every page, the donate
  button of the Unraid template and the Sponsor button of the repository. It is entirely voluntary and unlocks nothing.

## 1.25.0

### Important

- A weak `SLM_AUTH_PASSWORD` is now refused and the app does not start: it needs at least 10 characters, cannot be a
  very common password and cannot be the user name. Change it before updating if yours is shorter. Accounts created in
  **Users** keep working with their current password; the new rules apply when a password is set.

### New features

- **Quick search** from any page: <kbd>Ctrl</kbd>+<kbd>K</kbd> (<kbd>⌘</kbd>+<kbd>K</kbd> on a Mac), <kbd>/</kbd> or
  the magnifier in the header find games, series, collections, pages and sections of Settings as you type, forgiving
  typos. The arrows choose a result and Enter opens it. Nothing is added to the menus.
- **Login protection against brute force and bots**: after a few failed logins every attempt is slowed down, and after
  10 the address **and** the account are blocked, so guessing one account from many addresses, or many accounts from
  one address, are both stopped. The block grows with each further failure, and a flood from many addresses slows
  every attempt.
- **Behind Cloudflare**: the login protection uses the visitor's address. With a Cloudflare Tunnel nothing needs to be
  set; when Cloudflare connects directly, set `SLM_TRUSTED_PROXIES=cloudflare`.
- Install guide and templates for **Synology** (Container Manager) and **Portainer**.

### Improvements

- New passwords need at least 10 characters and cannot be a very common one or the user name.
- A short animated tour of the app at the top of the README.

### Fixes

- Windows, macOS and Linux programs: the games folder of the container (`/mnt/roms`) is no longer scanned by default
  on a computer, an empty covers file no longer shows an error, the keys are logged once, and starting the app twice
  opens the running one in the browser instead of failing.
- Settings > Notifications: the test button no longer goes out of the page on medium-sized windows.

## 1.24.0

### New features

- **Ready-to-run programs**: every release now has the program for Windows (x64 and ARM), macOS (Apple and Intel chips),
  Linux (x64 and ARM64) and Raspberry Pi (32-bit), with checksums. Unpack it and run it, nothing to build or install.
  The Windows program has its icon and version details.
- On Windows and macOS the app **opens in the browser** when it starts (`SLM_OPEN_BROWSER=false` turns it off).

### Improvements

- Statistics: the ring of **Space by content** fills clockwise like a bar that loads, instead of the whole chart
  turning with the number in its center.
- The genre chips of the game page keep their size when the box beside them is taller.
- The demo mode has genres, sequels and coming games, so the recommendations, the series and the upcoming releases
  can be tried too; new screenshots in the README.
- A visible thank-you to the authors this project builds on: @giwty, @dtrunk90 and @trembon.

## 1.23.0

### Fixes

- **Menu links from game pages no longer give 404** ([#3](https://github.com/DeLFuS77/switch-library-manager-web/issues/3),
  thanks to [@LonelyTV](https://github.com/LonelyTV) for the report): the links of the menu were relative, so from
  `/title/...` they pointed inside that folder. Every link now starts at the root of the site, and a test checks them.
- The library card of games with a missing update counts the same games as the "Update available" filter.
- No more movement when pages load or when changing sections of Settings: the fonts are preloaded, the space of the
  scroll bar is kept, pages change with a crossfade and the notices are in place from the first paint.
- Styles and scripts are loaded again whenever they change, so an update is never shown with old styles.

### New features

- **Diagnostics** (footer and Settings): keys, library folders, titles database, free space, automatic copies,
  protection and tasks checked at a glance, with what to do about what is wrong and a report to copy (no keys, no
  passwords).
- **Automatic copies of the configuration**: every week, the last 5 kept in the `backups` folder of the data folder,
  to download or restore from **Settings > Backup**, and a copy at any moment.
- **Settings by sections**: each section of the menu is shown on its own instead of one long page; Save still saves
  everything, and on phones the sections are a row above the content.
- Chinese names take the titles of the stores of mainland China, Hong Kong and Taiwan together.

## 1.22.0

### Security

- **Without users the app only answers on the local network** (VPNs such as Tailscale included). An app published
  on the internet before an administrator is created is no longer open to anyone; every page reminds you that login
  is off. If another service already protects the app, set `SLM_ALLOW_REMOTE_WITHOUT_LOGIN=true`.
- **Log out ends the session for good**, also on a copied cookie, and only one first administrator can be created.
- No open redirect after login; behind a reverse proxy the login limiter sees the client, not the proxy.
- Limits on request sizes and slow connections, HSTS over HTTPS, cross-origin isolation headers, no folder listings.
- Covers are only downloaded over HTTPS from the internet and with a size limit, webhooks do not follow redirects,
  oversized images are refused, organize names cannot point outside the game folder and temporary files never follow
  links. The settings, users and session files are readable only by the app.
- Restoring an old backup cannot disable the login or point the databases to plain http.

### Visual improvements

- **One card everywhere**: recommendations, series and upcoming games use the same card as the library.
- The star and the heart change at once with a little pop, the cover flies from the card to the game page, and the
  cards appear at once when you come back to a page.
- The game page shows the artwork of the game behind its header.
- Upcoming releases feature the next game for you with the days left.
- Repeated tasks are shown once ("×18") in Tasks.
- **Phones**: a bar at the bottom with the main sections.
- The same search bar on every list, Space says when there is nothing to clean up, and the statistics cards fit in
  one row.

## 1.21.0

### New features

- **Upcoming releases**: the games that can already be pre-ordered in the eShop, by month, with the date on the
  cover and the heart for the wishlist (the wishlist notification tells you when they come out). The games of the
  wishlist and the new games of the series you have are outlined, with a "For you" filter, and the games released
  in the last two weeks are shown apart.
- **Series**: a page with the series you have a game of, how many of their games you have ("3 of 4") with a bar,
  and the ones you are missing. The page of each game shows the progress of its series too.
- **You might like**: Missing Games, and the library when you have favorites, recommend games you do not have
  that look like your favorites, by their series and genres, with the reason and the heart for the wishlist.

### Improvements

- The same game from several stores counts once in the series, with or without marks such as ™, and network
  tests and trial missions are not counted as games.
- The menu fits on one line on wide screens with its new links, in every language.

## 1.20.0

### New features

- **Games without their base game**: the updates and DLC of games that are not in the library, which Issues listed
  file by file, are grouped by game with their cover, how many updates and DLC you have of each one, and the heart
  for the wishlist. Issues links to it.
- **Issues without the noise**: images, notes, checksums and other files that are never games (such as the
  screenshots other tools leave next to the games) are no longer reported. For any other type, Issues offers a
  button to ignore it and scan again.

### Improvements

- The SD card planner lists a few rows of a big library instead of thousands (its page weighed 8 MB with 11,000
  games) and has a search among the games that are not chosen; the games it does not list still count in the
  totals and in the copy.
- Big numbers are written with the thousands separator of the language everywhere (11.382 in Spanish, 11,382 in
  English), not in some places only.

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
