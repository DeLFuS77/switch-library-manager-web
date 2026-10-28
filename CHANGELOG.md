# Changelog

## Unreleased

### Improvements

- In the view by covers of Tasks, a file worked on in steps, as in a compression (hashes, compression and check), says under its cover the step it is in, with its colour, and its bar is split in the steps, each in the colour it has in the view by bars (the hashes cyan, the work orange, the check green): the ones done stay full and only the one being done fills up, with the moving stripes, so the bar no longer seems to start again. A task of one step, as Verify, keeps its whole bar, with the moving stripes too

## 1.65.0

### New

- Place new files, on the Organize page: the files that lie loose in the library folders, like the uploaded ones, go to the folder their game has already, or to a new one for a new game, and nothing else of the library moves nor is renamed. The automations can do the same with Only the new files under their Organize step, so a game or update uploaded is put in its place without touching what is already organized (thanks @ChapeLu)
- An option to keep the folder each game has: its files go there and the folder keeps its name, so the template names only the folders of new games (thanks @ChapeLu)
- A subfolder for the base game, next to the ones of updates and DLC, such as Game › Base (thanks @ChapeLu)

### Improvements

- A collection whose file name already has the title ID of one of its games keeps it when Keep the names that already have their title ID is on, instead of taking the list of its games as a name
- In the view by covers of Tasks, the covers of the files at work and the bars under them fill up in blue instead of red, which tells errors elsewhere in the app. The covers of the files still waiting are darkened and the ended ones are shown as they are, so what is left is seen at a glance. The covers fill up in order, from the top left: the files ended first, as they end, then those at work and those waiting, even when several files are worked on at once

### Fixes

- With the names for the Switch, a game whose name is in Chinese or Korean with a number in it, like Rune Factory 4 Special, got a folder named only with the number (4): it takes the name of its file, or Unknown Title, as a name with no letter did (thanks @ChapeLu)
- In the view by covers of Tasks, the total of a running task jumped back and forth every second, and the bar under each cover grew again from nothing: each bar now goes on from where it was

## 1.64.0

### New

- The list of the files that cannot be compressed, on the Compress page, tells why for each one, not only for games in sparse storage: compressed already (an NSZ or XCZ under the name of an NSP or XCI), no game data, or an encryption no compressor reads. For the files noted by an older version without a reason, it is found in the background by reading their headers, without writing anything, and shown once known

### Improvements

- Before Compress, Decompress, Convert, Split or Combine start, the most their new files may take on each disk is weighed against what is free there, and when they may not fit the page says where and how much and asks before starting anyway. Files whose originals are deleted count only the ones written at once, as the app already gives their room back
- The list of games to combine warns of a game with a DLC that needs a newer version of the game than its newest update in the library: the combined file would hold a DLC that asks for an update it does not have
- Packs are called Combine now, a plainer word for what they do: the tab of the Compress page, its buttons, the one of the game page, the task and its notes, in every language. The files keep the (pack) in their names, so the ones already made are still found (thanks @NikoMuffin)

## 1.63.1

### Improvements

- Tasks by covers shows every file of a compression, decompression, conversion, pack, split or file check, not only the ones at work: the files waiting under Waiting, those at work filling up with their progress and a bar, and the ones ended with OK or Error. The covers keep their colours, and a file that failed gets a red edge. A check of a whole library shows its files a few hundred at a time, from where it is
- The parts of Tasks (Running, Waiting in the queue, History) fold and unfold with a click on their title, with how many tasks each one has, and stay as you left them

### Fixes

- In Tasks by bars, the cover of each file at work was darkened by its progress: it is shown as it is again, as its bar already tells the progress

## 1.63.0

### New

- The signature of Nintendo on every NCA is checked: with every whole check of a file (it reads the headers only), and on its own in seconds with Check signatures on the Issues page. The files with an NCA a tool rewrote (to take out its rights ID, for example) are listed in Issues as Not signed by Nintendo; they usually install and play
- Organize has a field {DLC_NUMBER}, the number of a DLC among the ones of its game (1, 2...), for short names like Game [DLC 1] [ID][v0] instead of the long name of the DLC
- Keep the names that already have their title ID, in Organize: a file whose name has its title ID in brackets is only moved, and the name you gave it stays

### Improvements

- The list of packs to make tells what each pack holds (1G+1U+2D: the game, its latest update and two DLC), and the games packed before follow it under Already packed, with the name of their pack, instead of being left out without a word
- In Tasks, the cover of each file at work fills up from the bottom with the progress of its step, and a file without a cover gets a ring around its number
- Tasks can be seen by bars, as before, or by covers: a grid of the files of each task, those at work filling up with their progress and the ones ended with OK or Error under them (the reason of an error on its label). The choice is kept by the browser

### Fixes

- The CHANGELOG of the repository listed every version twice since 1.61.0

## 1.62.0

### New

- The bot of Telegram of the notifications can answer, with "Answer in the chat of Telegram" in Settings (or the wizard): written to in its chat, it tells whether a game is in your library (/have zelda), what was added this week and the updates and DLC waiting (/new), the space (/space) and the tasks at work (/status), and starts a synchronization (/sync). In Spanish it takes /tengo, /nuevo, /espacio, /estado and /sincroniza. Only the chat of the notifications gets answers, in the language of the app, and nothing has to reach the app from outside: it asks Telegram for the messages

### Improvements

- Space shows each group (old updates, duplicates, compressed originals and the games of several regions) as one line with its count and size, which opens to list its files; each game of several regions opens the same way, with the flags of its copies on its line

## 1.61.0

### New

- Update now: on Windows, macOS and Linux the app installs a new version by itself, from the notice of a new version or the Update page. It downloads the file of its system from the release on GitHub, checks it against SHA256SUMS.txt of the release, tries the new program before it replaces the one that runs, keeps that one as .old and starts again. Nothing is changed when a check fails, and it waits while a task works on the files. Docker and Unraid keep updating the container
- A star on GitHub is asked for in the notice of a new version and in the window of what is new after an update
- The notice of a new version can no longer be put away for good: closing it hides it on that page only, and it shows again while the version is not installed
- The Update page shows what the new version brings, has Check now to ask GitHub at once and a bar for the download, and Go back to the version of before, kept when the app updated itself
- Install new versions by themselves at night, in Settings: between 3 and 5 in the morning, when no task works on the files (Windows, macOS and Linux)

### Fixes

- Uploading a game with a long name in Japanese (or with many accents) failed when the file took its name on Unraid, and its task stayed at 100%: the disks of ZFS keep names decomposed, where such a name takes more than the 255 bytes allowed. The name is now shortened before its title ID, as Organize does, and Organize measures names the same way. An upload that cannot take its name ends with the error

## 1.60.1

### Improvements

- The language flags of the cards have the shape and the size the region flags had on the cover, after the sign of a language

## 1.60.0

### Improvements

- Preferred regions, in order: up to three, in Settings and in the wizard. They are the zones of the eShop that sell a game with its own title ID (Europe, the Americas, Australia and New Zealand, Japan, Korea, Hong Kong and Taiwan, China), by name instead of a country code; the copy of the first one a game has is its main copy. A country chosen before is kept as its zone
- The wizard plays a short welcome when it starts
- Game cards show the languages of the game as round flags in a pill on the cover, yours first, the others counted as +N and shown on hover. The region flags move below the name, right of the version and the year, and only when they tell something: most games have one title ID for every store, so the United States flag alone is no longer shown

## 1.59.0

### New

- A game you have from several regions, with "Count a game once when you have it from several regions" in Settings, has one card with the flags of all the regions it was released in: the ones you have lit and the others dimmed (a country once, also when it sells the game in several languages; more than four counted as +N, which shows them on hover); a game you do not have shows one card of all its regions, instead of one for each. The setting is in the wizard too, with the two ways shown side by side
- The page of such a game lists its copies of each region (languages, update, DLC, size, damaged), and you can make one of them the main copy: the one the library shows and Space keeps, instead of the one suggested. "Back to the suggested copy" undoes it
- Preferred region, in Settings: the copy of that country is the main one of each game you have from several regions, unless you choose another on its page
- With each game counted once, a game of several regions is up to date when one of its copies is, and has the DLC of the copy that has the most: its card, the filters, the Updates page, its counter, the notifications and the statistics count it so. The Region filter finds it by the region of any of its copies

## 1.58.1

### Improvements

- Space: the files deleted leave the list as they go, one by one, instead of all at the end of the cleanup

### Fixes

- Space counted as "waiting for the verification" originals whose compressed copy was already checked, but not whole (an update needs its game for that) or found damaged, and "Verify now" answered that there was nothing to check. Such a copy now says why: checked in part, the original can be selected and deleted on request, as for duplicates; damaged, the original stays

## 1.58.0

### Improvements

- Measure this server takes a heavier setting when it is at least 5% faster, not 10%: a measure varies little enough for it, and a setting clearly faster was left out
- Users shows, next to the date each user was created, the last time they signed in: in the login page, or as an API client with their name and password. Users who never did say so

### Fixes

- Space said that files waited for the verification, and Maintenance that they could go once their other copy was checked, also for files that stay for another reason: old updates kept for their cheats, a file holding a part the library uses, an original its compressed copy does not hold whole. With every file checked, "Verify now" checked nothing and the files stayed. Only the files whose kept copy is still to be verified are counted now; with nothing to free, the page says the files below stay, each for the reason shown
- While a compression ran, Space listed the originals already compressed and deleted, which the library keeps until the scan at the end of the task, as "The compressed file does not hold everything this file holds". They are no longer listed
- In the same while, the download of such a game, and its ZIP, failed: the file of the library was gone. Its compressed copy is downloaded instead
- Activity did not show the sign-ins of the login page, right, wrong or blocked: only those of API clients, one for every request, so a program asking the app now and then filled the log. The login page is in it now; an API client is noted once a day for each user and address, and a failed or blocked attempt once an hour
- The count of files of a compression ("3 of 10 files") counted a file as soon as it was started, and with two files at once two before either was done; the check of the files did the same. A file is counted now once it is done: compressed and its copy checked, or checked

## 1.57.5

### Improvements

- Check new files, with no file new or changed since the last check, says there is nothing to check instead of starting a task that checks nothing, which looked as if the button did nothing and was pressed again. Check everything again still reads every file

### Fixes

- The downloads for Windows, macOS and Linux had the version in their names, the file and the folder inside it (switch-library-manager-web-1.57.4-windows-x64): unpacked, each new version went into a folder of its own and started without the settings and the library, which the app keeps next to the program. They no longer have it: switch-library-manager-web-windows-x64.zip holds the folder switch-library-manager-web, and a new version unpacked over it replaces the program and keeps everything. This once, copy the files of the new folder into the folder you use now
- The games nothing can compress (in sparse storage, or compressed already under another name) were offered and tried again, each with a warning, by the first compression after Organize moved them into other folders: their mark now goes with them, as the results of the checks of the files already did
- Every copy of an old update kept for its cheats was kept: an NSP of the version the library holds as an NSZ stayed after the cleanup, at the root of the library or wherever it was. The cheats need one copy only: the one of the library stays, and the others go once it was checked sound

## 1.57.4

### Fixes

- A game whose file name starts with a dot (".cat [0100…][v0].nsz", named so by an older version or another tool for a game whose name starts with one) was taken for a hidden file: the scan passed over it, so it was neither in the library nor organized, and its folder stayed as it was. Such files are games now; Organize gives them names without the dot, which hides them on Linux and on many NAS. The "._" files of macOS and the other hidden files are still passed over. Uploaded from the browser, such a game was refused; it is saved without the dot now

### Improvements

- Deleting empty folders, in the cleanup and in Organize, also deletes the folders that were empty already, found by the scan as it walks the library, and the folders with nothing in them but files Windows or macOS leave (Thumbs.db, desktop.ini, .DS_Store, "._" files), which go with them. Before, only the folders the run itself emptied were looked at, and a Thumbs.db kept a folder. A folder with a hidden file of its own still stays. The preview lists the folders that are empty already

## 1.57.3

### New

- "A folder for each DLC", under the folders with their ID in Organize: turned off, the game and its updates still get their ID and every DLC goes to the DLC folder, without a folder each. The paths stay shorter: with a folder for each DLC, a library of 14,700 games had over 700 paths of more than 260 characters, which many programs of Windows cannot read; with every DLC in the DLC folder, 76. On by default, as before

### Improvements

- With a folder for each update and DLC, the folder of a DLC no longer repeats the name of its game when the DLC starts with it, written in any case and with any signs (KONOSUBA - God's... and KonoSuba God's...), or with two of its words and a dash (WRC Generations - Porsche...): DLC › Porsche 911 GT3 RS RGT Extra liveries [ID]. A DLC the titles database does not name goes to "DLC [ID]" instead of a folder named by its ID only

## 1.57.2

### Fixes

- Old versions of a DLC are no longer kept "for the cheats": cheats are for a game and its updates, and a DLC at version 0 counted as the update 0 of its game, so the cleanup never deleted it
- When a game has two copies and the one found first could not be read (identified by its name only, often a damaged file), the copy that could be read is the one the library keeps. Before, the sound copy was the duplicate: Organize left it where it was, and the cleanup kept both
- Between copies of a game, the one the check vouched for whole is kept instead of one it could check only partly: a cartridge whose NCA files a tool rewrote to put the update in, with the romfs of the update, which cannot be checked on its own. The cleanup then deletes the other copies, which it refused while the copy kept could not be checked whole. Not when the settings ask for NSP or XCI, and a copy chosen in Issues still wins
- A duplicate that holds several titles (a cartridge with its update, a SuperNSP) is deleted, by the cleanup or from the Space page, only when the copies kept of all its titles are sound, not only the one its reason names

## 1.57.1

### Fixes

- The icons read from the game files, for games whose cover the eShop does not have, are kept when the library is scanned again, instead of being read again after every scan with a task of covers each time that told 0 covers. That task, when it only reads icons, tells how many it found and ends at 100 %
- A task the app was stopped in the middle of shows how long it ran until it was last heard of, instead of "took 0s"
- The cleanup of duplicates and old updates no longer leaves files with "The copy that is kept was not found; rescan the library" when a game had three copies or three versions: each one names the file the library keeps, not one deleted with it. A copy of an old update counts as an old update

## 1.57.0

### New

- Three fields for the names of files when organizing, as Switch Library Manager has them: {PACKAGE_CONTENTS}, the titles of each kind a file holds (1G+1U+2D for a SuperNSP with the game, an update and two DLC), and {SIZE_GB} and {SIZE_MB}, its size (2.7GB, 2650MB)

### Improvements

- A game with nothing to compress tells why in Tasks: a file compressed already (an NSZ or XCZ named as an NSP or XCI), NCA files with no game data, or an encryption no compressor reads, besides sparse storage, instead of only "Nothing in the file can be compressed"
- Measure this server takes the biggest games of the library that can be compressed: a big one in sparse storage, or damaged, is passed over instead of failing the measure with "There is no game in the library to measure the disk with"
- Organize gives a file its new name in its own folder when that name is already taken where the file would go, instead of leaving it with its old name: a file of another size with that name was an error ("A file could not be changed"), and a copy of the same size was left as it was. Both are now a warning that tells why, in the preview and in Tasks; for a file of another size it adds that they are two files of the same game and version, and that Check can tell which one is good. When the new name is taken in its own folder too, the file is left as it is, with a warning instead of an error
- The preview and the result of Organize show the changes in groups that open and close, closed at first with their counts as the summary: With problems (open when there are any), Needs a look (files left as they are, renamed in their folder, or whose title ID changes), then Move, Rename, Create folder, Delete and Empty folders. A group shows its first 100 changes and the next ones when asked, so a library of thousands of files no longer gives a list of thousands of rows at once
- The Compress page starts at the compression level chosen in Settings, the one Measure this server measures, instead of always at Balanced

## 1.56.1

### Fixes

- With no connection, a synchronization no longer spends minutes trying to download the titles database and the files that go with it (versions, names in other languages, covers, series, cheats), each one several times on each server: once the titles database gets no answer from its servers after a few attempts, the rest is not tried, the library is scanned with the saved copy, and the synchronization tells in the history of Tasks that the titles database could not be downloaded for lack of connection. A server that does answer, but with an error, still has the other servers tried as before
- Downloading covers stops as soon as the cover server no longer answers, also when the connection is lost after some covers came, instead of trying every cover left; it tells in the history of Tasks that the covers left are downloaded after the next scan. A cover that got no answer is no longer marked as failed, which made it wait a day to be tried again once the connection was back

## 1.56.0

### Improvements

- Block compression, and every XCZ, now uses the zstd library in C at the level chosen, as the solid NSZ already did: smaller files at the same level, and the estimate of the savings matches them
- The measure of the server gives each thread of a trial whole pieces of work, so settings with many threads are no longer measured as slower than they are, and it measures block compression as it runs
- Decompressing and checking NSZ and XCZ is faster: the zstd stream is decoded on several threads and ahead of the decryption, the blocks of a block compressed file several at once, and what is written is hashed beside the writing. On a game of 685 MB, a check went from 1.3 s to 0.4 to 0.8 s and a decompression from 2.7 s to 1.2 to 1.8 s
- The programs for macOS are built on a Mac with the zstd library in C, as the ones for Windows and Linux already were: the balanced and max levels compress one file on several threads there too, and the max level in blocks and XCZ files are smaller
- Automatic threads use every processor but an eighth (at least one left free for the rest of the server), instead of half of them and 4 at most: the zstd library in C compresses one file on all of them, 38 % faster with 24 threads than with 12 on a processor of 24. Fewer are used when the memory is short
- Measure this server tries half the processors and all the ones a compression works on, on one, two and four files at once, instead of three fourths of them and four threads at most. It takes 1 to 3 minutes

## 1.55.0

### New

- The queue of tasks can be put in order: each task has buttons to move it up or down before it starts
- A message by the channels of the notifications when the queue of tasks is done, with each task that ran and how it ended: to leave long work for the night and know when it ended. On by default, a switch under Notifications

### Improvements

- Games of the library the titles database does not list (removed from the eShop, sold in one store only) are no longer second-class: they learn of their updates from the versions Nintendo published, which were dropped for them; their publisher comes from their own files (read again once); and with the IGDB keys set, IGDB adds their genres, release date, description and cover. They show in Statistics by genre, publisher and year, and Missing still lists only the games of the database
- Organize tells in Tasks each file it could not change and each one it left as it is, with why (up to 30, and how many more), as the preview already did; its count of files changed no longer counts the ones left. The updates and DLC whose game is not in the library, left where they are when the option to organize them is off, are listed with why instead of being left out silently

## 1.54.0

### New

- A queue of tasks: a compression, decompression, conversion, pack, split or check of the files asked for while another one works waits its turn, instead of being refused with "A compression is already running". They start one after the other as the files get free, also after a restart. Tasks shows the queue in its order, and the ones marked with their box are taken out before they start
- Organize, Compress and Statistics show their parts as tabs, like the tabs of a folder, instead of one under the other: Actions and Options in Organize; Compress, Convert, Split, Packs and Decompress (each with its count) in Compress; Space, Games and Library history in Statistics, which on a phone was five screens long. The tab shown is in the address, so a reload keeps it, and the arrow keys go from tab to tab
- On phones and tablets a turning fan next to Synchronize shows that tasks are at work, with how many when there are several, and opens Tasks: the menu with the link of Tasks is closed there. It goes away when the last task ends

### Improvements

- The measure of the server in Settings saves the fastest setting by itself, instead of filling the fields to save, and is remembered with its date. It also measures Balanced and Maximum with that setting and tells how many minutes 10 GB of games take with each here, to choose the level knowing what it costs
- Pages open faster: the styles, scripts, fonts and icons were loaded again on every page, about 280 KB and a third of a second on a NAS, as no answer said they could be kept. Now browsers keep them (the styles and scripts of a version for good), the service worker asks for a page while it starts instead of after, and a list opened from the menu comes filtered at once instead of loading twice
- The page shown while the app starts stays at least two and a half seconds, ticking its steps one by one, instead of flashing by after a restart

### Fixes

- Phones: the step of the tune-up on top of Organize, Compress and Space is one line of text that wraps, instead of two narrow columns
- Tasks follow the files Organize renamed or moved: a compression, decompression, conversion, pack, split, check or copy to an SD card started before the scan that follows (or resumed after a restart) works on each file where it is now, instead of failing with "no such file or directory" for every file. A file deleted or moved by something else is skipped with a note that says so, and the library is scanned again
- Coming back to the Library (or Updates, DLC, Missing) from the menu starts at its first page and without the search made before; it kept the page and the search even after closing the app. Its order and filters stay, and going back from a game keeps everything
- The views of the Library have room around the red of the one shown and between them, and on phones big counts are short (25k) so that the three fit
- The bar of a file in Tasks reaches its end: each step fills its share. The check of an NSZ counts the files inside it, fewer bytes than the original, and the bar of a compression stopped around three quarters before jumping to the next file; converting a game card with padding, decompressing and packs fell short the same way
- Phones: the bar at the bottom gives each section the room of its name, so a long one (Updates in Spanish) no longer runs into the ones beside it, and the mark of the section shown is the same short line on each
- Phones: the views of the Library (All, My games, Missing) take the room of their names instead of equal thirds, where "My games" ran out of its own and touched the red of All
- The mark of the page shown in the menu of phones is a short rounded bar inside the corners of the link; it stuck out above and below them
- The section shown in Settings keeps a border in its red on phones, instead of losing the border the others have

## 1.53.0

### Improvements

- Balanced and Maximum compress each NSZ on several threads with the zstd library in C (zstd 1.5.7), in the programs for Windows and Linux and the Docker image: on a game measured, Maximum went from 13 to 25 MB/s with 8 threads and makes files about 6% smaller (the default level of nsz, 19), Balanced compresses as Maximum did and as fast as before (about 200 MB/s). The files are the same standard NSZ, with a window of 8 MiB at most, that every installer reads. Fewer threads are used when little memory is free (Maximum takes about 190 MB per thread). The programs for macOS keep the compression in Go, as before
- Compressing, decompressing, converting, making packs and splitting read each file once instead of twice or three times: the original is hashed while it is compressed, and what is written is hashed and checked while it is written (the NCZ of a pack of NSZ are decompressed from the disk), then the new file is opened again for its files, their sizes and their start and end. The checks are the same as before, a damaged NCA is still found and its file removed; the steps of a compression go from three to two
- Checking a file hashes its NCA beside reading and decompressing them, instead of after: on a DLC of 6.6 GB, the NSZ is checked in 8 seconds instead of 10.5 and the NSP in 3 instead of 4. It is the check of the files, the last step of a compression and the check of a pack. Still one read of each file at a time, as hard disks prefer

### Fixes

- A file found damaged by a compression (an NCA that does not match its content ID and fails its hash trees, or a file cut short) is remembered like the check of the files finds it: it shows in Issues and is no longer offered on the Compress page while it does not change, which says how many are left out. It failed again on every compression, so the same files kept coming back

### Development

- Tested with DBI (firmware 22.1.0): a game with its update and DLC compressed at the max level with 8 threads (zstd in C, level 19) installs, every NCA with TRANSFER OK and SIGNATURE OK, and the game boots with the update. It took 17 minutes instead of 54 and the files came out 6% smaller
- The programs are built with Zig 0.16.0 (checked against its SHA-256) as the C compiler, static on Linux; every pull request builds the programs of a release and starts the one of Linux. The tests run with the zstd library in C and in Go only

## 1.52.0

### New

- The setup wizard can browse the folders of the server to choose the folder of the games, instead of writing its path: the drives on Windows, the root, the home folder and the usual places of disks and shares (/mnt, /media, /Volumes...) elsewhere. Administrators only
- Organize can put each update and DLC in a folder named with its own ID: Game [ID] › Update [ID of the update], and DLC › name of the DLC [ID of the DLC]. The game folder gets its ID too when its template has none. Off by default, a switch under Folders

### Improvements

- The bar of each file in Tasks has a meaning: in a compression, decompression, conversion, pack or split, the colour of its step (cyan for the hashes, yellow for the work, green for the check); in a check of the files, the colour of what it holds (red for a game, yellow for an update, cyan for a DLC, green for a complete pack), with a dot of that colour and its name next to the size. They were taken from the name of the file
- The setup wizard checks the administrator before saving anything: a password the app refuses (too short, too common, or the user name) made "Save and start" fail again and again with only the word "Administrator". Now the security step says why and does not go on; "Show the password" helps to see what was typed
- The steps of the setup wizard scroll inside, so the title and the buttons stay in place on long steps (notifications); on phones Back, Skip and Next stay together
- The summary of the setup wizard has a "Change" button on each row to go back to its step, says whether the keys were found, and shows whole paths on phones
- The setup wizard tells which folder is missing in plain words, clears the warning once the folders are right, checks the keys as soon as their step opens, writes the example folder the way of the system of the server (with the Docker hint only in a container), and keeps "Check the folders every" next to the switch it depends on
- The compression level and "Keep the originals" of the wizard look off while it only compresses when asked
- The time left of a compression, decompression, conversion, pack or split follows the speed of each of its steps: the hashes go at hundreds of MB/s and the max compression at a few, and their average said "2 minutes left" for a compression that took an hour. Until a step was measured, no time is shown instead of a wrong one
- A complete pack takes the NSZ (or XCZ) of a game when the library holds it next to the NSP of the same name: the pack comes out compressed, half the size, instead of a 20 GB NSP made of the separate NSP
- The warning of little free space counts the space left, not only its share: a disk of 1 TB with 89 GB free was told short of space. A volume is short of space below 10% free and below 50 GB
- The Compress page and Settings tell that Maximum is about 10 times slower than Balanced (each file on one core) for files a few % smaller, and that installers read every level the same

### Fixes

- Built with Go 1.27.3, which fixes a security problem of the Go standard library (GO-2026-6809: compromised intermediate certificates trusted by crypto/x509)
- The last screen of the setup wizard closed by itself as soon as the first scan ended, with a small library in a second

### Development

- The tools that update the title database use Pillow 13.0.0 and certifi 2026.10.19 (security fixes in both), and Python 3.12, which Pillow 13 needs

## 1.51.1

### Improvements

- The help of the compression threads in Settings tells that a normal NSZ is compressed on about one core whatever the threads (one zstd stream per file), that the threads only speed up game cards (XCZ, in blocks), and that compressing more files at once is what uses more cores
- Each file in Tasks shows the step being done with the size of the file, the one seen in its folder: a 13 GB game being compressed showed "15.7 GB / 39.0 GB", the bytes of its three steps (hashes, compression, check) added up. The Total bar still counts every step

### Fixes

- Decompressing an NSZ from the Compress page failed for updates with many subsections (more than 64 sections in an NCZ, as the app's own compression writes them for real updates): the size needed was refused before starting. It now uses the same limits as the decompression itself
- Decompressing an NSZ stayed at 0% in Tasks until each file was done, as if it had hung on a big game: the progress now moves on while it decompresses
- Decompressing an NSZ (and checking one) is more than 3 times faster on files made by other tools: the compressed data is read in big pieces instead of a few bytes at a time, and the large window of those files (128 MiB) is no longer moved in memory every MiB (from 20 to 67 MB/s on a 3 GB DLC)
- A stopped decompression, check or copy to the SD card said "The compression was cancelled"; it now says the task was cancelled

### Development

- Tests of the edge cases of the block format that corrupted data in nsz: a size that is an exact multiple of the block size (nsz #210), and a short last block that does not compress and is stored as it is (nsz #79, #190)
- Tests cover a multi-content NSP (a "SuperNSP": base, update and DLC in one file): with tickets, and ticketless with the keys in title.keys, it compresses to NSZ, verifies and decompresses back byte for byte; without the keys it is refused clearly instead of making a bad file
- Tested with DBI (firmware 22.1.0): a game with its update and DLC, compressed by the app at the max level, installs from separate NSZ files and from one complete pack, every NCA with TRANSFER OK and SIGNATURE OK, and the game boots with the update. The update's NCZ had 866 sections

## 1.51.0

### Features

- Activity log on its own page (administrators), with filters by user, kind of action and text, and export to CSV. It now records more: sign-in attempts (who, from which address and when, and whether it worked, was wrong or was blocked — never the password tried), synchronizations, file checks and uploads, each with the address it came from
- Four roles for the users: Guest (looks, downloads nothing), Player (the read-only users of before: also downloads games, updates, DLC and cheats), Editor (also uploads, synchronizes, checks and compresses games without deleting the originals, and keeps favorites, the wishlist and collections) and Administrator. The Users page explains each one
- Cheats can be downloaded packed for Ryujinx (and its forks) and for Yuzu, Eden and Citron, besides the Atmosphère ZIP for the Switch: the game page and the library's cheats download now offer the three, each with the folders the target expects. For the Yuzu-style emulators each cheat gets its own folder so they can be turned on one by one, with the master code included. Suggested by LonelyTV in #85
- Notifications for failed tasks: a compression, a file check, a synchronization or any other task that fails is told by Discord, Telegram or the webhook, with its reason. On by default, with a switch in Settings and in the setup assistant; what you cancel is not told
- Smart collections: the filters shown in the Library (state of the game, genre, players, language, collection, status...) can be kept under a name; they show above the cards with how many games they have now, and fill and empty by themselves. Kept in the backups
- My game: each user keeps how their games go (pending, playing, finished, completed 100% or abandoned), a rating of 1 to 5 stars and a note, on the page of each game; the cards show the state, and the Library filters by it with how many games each state has. Without login it is one for everyone; read-only users keep their own too

### Improvements

- Library: a filter lists the games whose version needs a newer firmware than the one of your console (set in Settings), with how many they are; it shows when there are some
- Games without their title key are found before they are compressed: after a scan the NSP and NSZ files are looked at once (the headers of their contents and their tickets only, again only when a file changes), Issues lists those without a ticket whose key title.keys does not have either, with how to fix it, and Compress and the automations leave them out instead of failing halfway
- Uploads show in Tasks, with the file, its progress and its size, so they can be followed from another device; an upload nothing comes to for three minutes ends there with how to go on, and its file is kept for a day to go on where it stopped
- With several library folders, the Library asks which one to upload to

### Fixes

- Users: the hint of the password said 8 characters, while 10 are needed

### Development

- The check that presses the save buttons in a real browser also covers a field of another section of Settings, the first administrator, adding a user and changing their role, and the language and the colour of the account

## 1.50.1

### Fixes

- Safari on iPhone could close the Library while going from page to page ("a problem repeatedly occurred"), after reloading it once: on phones and tablets the pages no longer fade into each other, which made the browser keep a picture of the whole page, and the marks of the cards have no blur behind them, which made a layer of its own for each card

## 1.50.0

### Features

- Upload games from the browser: the Upload games button of the Library, or files dropped on the page, send NSP, NSZ, XCI and XCZ files to the library folder. They go in pieces, so a cut connection goes on where it stopped, also when the same file is chosen again after the page was closed; the free space is checked first, a name already in the folder is refused, and the library is scanned once each file is whole, with the automations as for any new file. Administrators only
- Notifications for new cheats: when the cheats database gets cheats for the version of a game you have, it is told by Discord, Telegram or the webhook, like new updates and DLC. On by default, with a switch in Settings and in the setup assistant. The games that have cheats already are only recorded the first time, not sent all at once

### Improvements

- Space: a duplicate whose kept copy was checked without errors but not whole (an update, whose game is needed to check everything) can now be selected and deleted, after a confirmation that says so. Select all leaves it out, and Organize and the automations still keep it. Damaged or unverified copies stay blocked
- The legend of the library explains the violet wand of the games with cheats for your version
- Issues: each file that cannot be read, is damaged or cut short has its own Delete button, after a confirmation. The files that cannot be read could not be deleted from the app before
- A game whose NSP has no ticket, and whose title key title.keys does not have, cannot be compressed: the task now says so in your language, with how to fix it, instead of a technical message in English

### Fixes

- The Tasks page could stop updating and show a task still running long after it ended: each page of the app open in a tab kept a connection to the server, and with several tabs the browser had none left for the page seen. A tab in the background now closes its connection and looks again once it is seen, and a list that never comes or fails to draw no longer stops the next updates
- A compression showed only its total bar, without the files being compressed, their covers, their steps nor the sizes, since 1.49.0: once a file had saved something, the size saved (a big number) stopped the drawing of the card halfway. The comparisons of the pages now take numbers of any size

### Development

- The tests press the save buttons of Settings and Organize in a real browser and check that the server kept the new value

## 1.49.3

### Fixes

- Settings and Organize saved nothing since 1.49.0: the note of the switch to keep the updates with cheats was taken for the message of the last save, and removing it stopped the save. Saving works again, and the save button goes off once saved
- The small covers of the files of a task no longer blink each time the bar or the percent are updated

## 1.49.2

### Improvements

- The name of a cartridge with several games uses the English names of its games, and with names for the Switch leaves out those with kanji or hangul, which came out in pieces (they count in "N more"); up to 110 characters, so the two volumes of a collection fit
- The mark of the games with cheats for your version is violet and pulses like the mark of an update, seen at a glance on the covers

## 1.49.1

### Improvements

- The bar of a synchronization changes colour from red to green like the bar of a task, shows how far it is next to it instead of squeezed inside, and is seen reaching its end before the notice goes

## 1.49.0

### Improvements

- Cheats for versions the database does not know yet: the build ID is read from the game itself (a few kilobytes of the file, kept while the file stays the same), so newer updates find their cheats too
- **With cheats for another version**, a new choice in Filters > Other: the games with cheats only for other versions than the one you have
- An old update with cheats is kept when the newer one has none, so the game can be installed at the version its cheats are for: in Delete old updates, the automations and Space. The switch is in Settings (next to the cheats), in the setup wizard and in Organize > Cleanup; turned off, a warning says these updates will be deleted
- Organize names a cartridge with several games after all of them, like Alphadia Genesis 2 + Asdivine Cross + 2 more [01005CE015080000][4 games].xcz, in a folder of its own, instead of as the update of the game it met last
- The bar of a long task shows that it started from the first megabytes, not only from its first whole percent
- Under the total of a task that works on several files, the space saved so far (compressions) instead of the name of a file its lane already shows

## 1.48.2

### Improvements

- When a file of a task ends, its bar fills up, it rises and fades, and the next file moves up into its place instead of jumping; the new next file slides in
- The cheats are downloaded when the app starts and when they are turned on, not only with the next synchronization
- In Issues, a duplicate tells the games its file holds and why it stays before the button to keep it
- The list to split says how many games a cartridge with several games holds

## 1.48.1

### Improvements

- The bar of a running task changes colour little by little as it advances: red, orange, amber and yellow, and green only at the end. The colours span the whole bar and blend smoothly, also between the updates of the page
- A bar is seen reaching its end before it goes: the bar of a file when the next one starts, and the bar of the task when it ends well (not when it is cancelled, fails or is paused)

## 1.48.0

### New

- **Cheats**: the games with cheats for the version you have get a mark on their cover and a filter in the Library (Filters > Other). The game page lists the cheats of your version and gives them ready for the SD card (a ZIP with atmosphere/contents/<title ID>/cheats/<build ID>.txt) or as the text file; when there are cheats for other versions only, it says which. The Library gives the cheats of every game at once. They come from switch-cheats-db, downloaded once a day at most during a synchronization, and can be turned off in Settings
- **Split game cards with several games**: an XCI or XCZ that holds a collection of games on one cartridge (like the KEMCO RPG Selection) is listed in Compress > Split multi-title files and splits into one XCI or XCZ per game, with its update and DLC, each named after its own game. The files of each game are copied as they are, under the header of the original card, and every part is checked before the original is deleted. A usual card, with one game and its update, is not listed
- **Filters in Missing ones**, like in the Library: games or demos, genre, players, language of the game and region, each with how many games it leaves (asked on the original project)
- Organize lets you choose which copy of a duplicate the library keeps: the NSP or NSZ, the XCI or XCZ, or the one the app picks (a file with several titles, which stays anyway, otherwise the smaller file). Changing it scans the library again
- **Keep this one**: each duplicate in Issues has a button to keep that copy instead of the other one; the choice wins over the one of Organize and the library is scanned again

### Improvements

- Tasks shows the cover of the game of each file being compressed, decompressed, checked or split, and the next file the task takes
- Issues tells, as Space does, when a duplicate or old file also holds the game, its latest update or a DLC that the library keeps (a file with several titles, like a cartridge with a collection of games), so it is not deleted
- A file that holds several games (a collection on one cartridge, a pack of several games) says which ones, on the game page and in Issues
- Saving which copy of a duplicate is kept tells how many duplicates keep the other copy now

### Fixes

- A search in the Library no longer drops the filters of genre, players and language

## 1.47.0

### New

- **Split multi-title files** (asked by switchsv on GBAtemp): Compress has a section that lists the NSP and NSZ files
  holding several titles (a "SuperNSP" with a game, its updates and its DLC, or a complete pack) and turns them back
  into one file per title, in bulk. Each title keeps its own Meta NCA, the NCAs it lists, its ticket and its
  certificate, copied as they are: nothing is decrypted, decompressed or changed, an NSZ gives NSZ parts with the same
  NCZ files, and the parts merged again give the original byte for byte. Every part is written under a hidden name
  and checked whole before it gets its name; the original is deleted only if asked, once all its parts are checked,
  and it is kept when some NCA of it belongs to no title.
- **Complete packs in bulk**: Compress lists the games with an update or DLC in separate files and makes the pack of
  each one chosen, or of all of them, as one task that shows its progress, can be paused and goes on after a restart,
  like a compression. The pack of a game page is made the same way.
- Settings > Library: your prod.keys (and title.keys, if you have them) can be uploaded from the page, from any
  computer, instead of copied to the server by hand. The app checks they are keys of a console before keeping them in
  its data folder, readable by itself only, points the settings to them and scans the library again; their contents
  are never shown nor written to the log, and backups still leave them out. Only administrators can do it.
- The setup wizard can upload your keys too, in its step of the keys: the field then points to them and they are
  checked at once. Its step of the folders warns of a folder listed twice or inside another one when they are checked,
  instead of only when everything is saved at the end.

### Improvements

- The endless animations (the shimmer of a cover loading, the drift of the numbers, the pulse of the ribbons, the
  auras, the cursor of the cards) stand still while they are out of the window, and go on when they come back:
  nothing changes on screen, and the browser no longer works for what nobody sees, on a phone or a TV above all.
- Issues: the cards of a row have the same height with their buttons on one line, and the card of the updates and DLC
  without their game shows the three games with the most files, their names in the colour of the text.

### Fixes

- A complete pack that deletes the separate files deletes the folders they leave empty too (an Updates or DLC
  folder of the game), when "Delete the empty folders" of Organize is on; the library folder and anything outside it
  always stay.
- The Tasks link of the menu shows how many tasks are running, in violet, next to the red count of the failed ones;
  with two tasks running it showed only the red 1 of a task that had failed, which looked like a count that did not
  go up.
- A compression (or decompression, conversion) resumed after a restart goes through its files by name too, as the new
  ones do since 1.46.0; it kept the biggest first, so one started before the update still took the largest games
  first. The file it was writing starts again in any case.
- Complete packs kept only one of a ticket and its certificate, and left out the XML of each CNMT, as they share the
  ID in their names: every file of the packages is in the pack now, only the NCAs found in two packages go once.

## 1.46.0

### Improvements

- Space, Compress and the other long lists draw only the rows near the screen: on a library of 40,000 files Space
  laid out its thousand rows in about a second, now in less than a tenth; the rows are still there for the search of
  the browser and the selection.
- The style sheet of the app is less than 60 % of its size (451 KB to 255 KB before compression): the icons, flags
  and parts of Bootstrap the app never uses are left out when it is built, so the first paint comes sooner.
- The SD card planner keeps its proposal until the library, the settings, the favorites or the collections change,
  instead of working it out again on every visit; it was the slowest page of a big library.
- Issues: the files cut short (a download or a copy that did not finish, an empty file) have a card of their own,
  "Files cut short", and can be deleted from it like the damaged ones, after a confirmation that tells how many and
  how much space; a file changed in the last 10 minutes is left alone, as it may still be arriving. The other files
  that cannot be read stay listed, with what may be wrong and the advice to check them before deleting them.
- Issues: each file shows its size, and the files that could not be read say why in plain words ("Cut short", "Its
  contents cannot be decrypted"), with the error of the reader below for whoever wants the detail.
- The turning fan of the Tasks link, its ring and its glow are violet on every page, not only on Tasks, and a lighter
  violet on the dark theme, where the one of before could hardly be seen; the header and the empty page of the tools
  wear that lighter violet too.
- Compressing, decompressing, converting and checking files go through them by name instead of the biggest first, so
  the count of files moves from the start instead of waiting for the largest games. The check still starts with the
  files never checked or checked longest ago, and a task resumed after a restart keeps its order.
- The mark of the page in the menu glides from the link of the page before to the new one, like the cursor of a
  console menu, and so does the red of the view of the Library (All, My games, Missing ones); with less motion asked
  by the system they just change.
- The save buttons of the preferences are on only when something changed: the bar of Settings slides away once all
  is saved and comes back with the next change, and the Save buttons of Organize and My account stay dim until a
  field changes; undoing a change turns them off again. A field filled by the app itself (the folder picker) counts
  as a change too.
- Settings refuse a library folder listed twice or inside another one, and say which: its files would be read twice.

### Changes

- The Library opens on All, your games and the ones you are missing (greyed out), and the switch under its title
  reads All, My games and Missing ones, in that order. My games has an address of its own (`/index.html?missing=0`);
  the links to All of before still open it.
- Library: the count next to its title goes, as the switch of the views already shows the number of each one.

### Fixes

- Library: My games counts as the list does, without the demos when they are hidden and with the copies of a game
  from other regions once when Settings say so; it could show more games than the list, the big number and the
  filters (12,604 against 12,356 on a big library).
- Organizing and cleaning up with "Delete the empty folders" no longer seem stuck on a NAS: the app walked the whole
  library to find them, minutes for a big one even when a handful of files moved (two minutes for 13 files on a
  library of 40,000). Now it looks only at the folders the run took files from, and their parents, one entry of
  each at most. Empty folders the run did not touch stay where they are.
- A library folder listed twice in Settings, or inside another one listed, is scanned once: its files were read
  twice and each one counted as a duplicate of itself, which Space could offer to delete with the only copy. Each
  file is also kept once by its path, whatever the folders.

## 1.45.0

### Improvements

- On the Tasks page the turning fan of the Tasks link, its ring and its glow wear the violet of the tools, the color
  of the header of the page, instead of white: the white was meant for the red background the active link used to
  have, and on its soft background of now it hardly stood out. On the other pages they stay red.
- The icon of an empty page wears the color of the header of the page too: violet on the tools (an empty Tasks page,
  for instance), cyan on what there is to discover, and the red of before on the library and the pages of the app.
- Library on a phone: the switch of My games, Missing ones and All stays on one line instead of breaking in two, and
  the four numbers on top slide sideways in one row, so the games start higher up.
- Couch mode: the Library opens on the covers; its numbers and its bar of games up to date, which are on the
  Statistics page too, leave the top of the page.
- Your year on Switch: the chart of the months has one color, the cyan of what to discover; the busiest months are
  the solid bars with their number in bold, instead of a red that read as an error.

### Fixes

- Couch mode: the header fits the screen of the TV. Search, Synchronize and the account went past its right edge;
  now the logo goes without its name, the search is a magnifier and, below a screen of 1900px, the links of the menu
  show their icons and counts (their names stay for screen readers).
- Couch mode: the pill in the corner no longer covers the Save button of Settings and the other bars at the bottom of
  the page, which sit above it.
- The shimmer of a cover still loading stands still when the system asks for less motion; a cover further down the
  page could keep it moving forever.
- The games you do not have, shown in grey among yours (All), keep the contrast of WCAG AA in the light theme: the
  card was veiled as a whole, which took their date and the "Not in your library" chip below it; now only the name is
  quieter. The accessibility check of each change looks at that view too.
- The page of a game you do not have is under Library › Missing ones in its path, instead of Missing Games, which
  left the menu.

## 1.44.0

### Changes

- The missing games are part of the Library now: a switch under its title shows your games, the ones you are missing
  (what was the Missing Games page, with its wishlist and suggestions) or all of them, each with its number. Missing
  Games leaves the menu, which is shorter; its address still works and opens the Library on the missing games.

## 1.43.0

### New

- Compress tells how much each file will likely save, from what the files of the same kind (and format) you compressed
  before saved: a game −17 %, an update −5 %, a DLC −40 %... with a line on top with the average of each kind and what
  all the files listed would save. It shows once three files of a kind have been compressed, and is kept apart from the
  history of the tasks, so clearing it does not lose it.
- A compression (and a decompression, conversion or pack) can be paused from Tasks and resumed later: it stops where
  it is, in the middle of a file too, and goes on from the same point, for instance while you play or download. The
  card says it is paused, its bars stand still, and the speed and the time left count only the time it worked. A
  paused task can still be cancelled.
- Interface sounds, like the menu of a console: a soft tick to move between the cards (with the keyboard or a
  controller), two notes to open and to go back, a pop for the star and the heart and a chime when a task ends. They are
  made by the browser, with no sound file, and stay off until turned on from the menu of the header, on each device.
- Couch mode (in testing), for a TV seen from the sofa: everything is a quarter bigger (half on a very wide screen), the page takes
  the whole width and the whole screen, the covers take the room, the secondary texts go and the cursor of the cards is
  thicker. It is turned on from the menu of the header or with the Select button of a controller, and left with Select,
  Esc or the pill it shows in a corner.

### Changes

- Tasks: during a compression the files already compressed are in a panel of their own, under the files being worked
  on, with the total saved at its top; their bars wear still diagonal stripes, blue for the size left and green for
  what was saved, so that they are no longer taken for the progress of the task.
- The boxes to choose the games of the SD planner and the IDs copied with a click on the page of a game are targets
  of 24px.
- The screenshots and the tour of the README and of the Unraid template show the new look of the app.
- Every change of the app is now checked for accessibility: each page of the demo, in both themes, on a wide screen
  and on a phone of 320px, must keep the contrast of WCAG AA, no text under 12px, controls of 24px, no sideways scroll
  and no endless animation when the system asks for less motion.

## 1.42.0

### New

- The app can be used with a game controller connected to the computer, the phone or the TV, like a console: the
  D-pad or the left stick go to the nearest card or control in that direction, A opens it, B closes a window or goes
  back, and Start opens the quick search. The card chosen wears the cursor of the keyboard, and a short notice the
  first time explains the buttons.

### Fixes

- The fan of the Tasks link turns around its center in every browser and at every scale of the screen: it is drawn
  as a square picture instead of a letter of the icon font, which sits a bit differently in its box in each browser.
  On the dark of the menu the fan and its ring are red; on the Tasks page, where the link has the red background of
  the active page, they are white, as on the red buttons, instead of red on red.
- Accessibility: texts that were too faint for their background now reach the contrast of WCAG AA (4.5:1) in both
  themes: the outlined grey and red buttons, the chosen section of Settings, the count of the chosen filter, the
  green "Up to date" and "Done" chips and the green texts of the light theme, and "No DLC" under the cards. The star
  and the heart of the cards are targets of 24px at least.
- Accessibility with the keyboard: the lists "Sort by" and "Per page" no longer load the page again at each arrow:
  the arrows only choose, and Enter or leaving the list applies the choice. After the page is loaded again the focus
  is back on the list (also on the ascending/descending buttons). Every button, field and page number shows the cyan
  ring of the app when it has the focus, the search button of the header too.
- Accessibility: the switch "Show the missing games too" says whether it is on; the quick search says how many
  results it found and gives the focus back where it was when it closes; the box to choose a cover is a target of
  24px; at a width of 320px the page no longer scrolls sideways; and the bottom bar of the phone does not hide the
  control with the focus. The details under the numbers of the overview are no longer faded.
- The chart "Games by year of release" of the statistics is drawn again as a row of bars: the chart of "Your year
  in Switch" had the same name and turned each year into a red bar across the whole width.
- The keyboard focus ring is a darker cyan in the light theme, where the light cyan could hardly be seen on white.
- Texts that were too faint for their background now reach the contrast of WCAG AA: the saga line under the cards of
  Upcoming and the active label of the bottom bar of the phone in the dark theme, the kinds of file of Compress and
  the counts of Issues in the light theme, the breadcrumb of the page of a game, the links under the fields of
  Settings and the red outlined buttons of the dark theme.
- The charts of the statistics are calmer: one color for each chart instead of a different color on each row, no
  stripes (in the rest of the app they mean that something is working) and numbers of the same width.

### Changes

- One color for each meaning: the updates (the circle on the covers, the chips and the counts) are amber now, and
  the gold stays for the favorites and the completed sagas, so that an update no longer looks like a favorite.
- The texts follow one scale of six sizes and none is smaller than 12px: the badges on the covers, the details of the
  cards, the counts and the labels of the bottom bar of the phone are easier to read.
- The bar of the SD card planner is cyan while the games fit, amber when the card is nearly full and red only when
  they do not fit, instead of red all the time.
- The game card chosen with the keyboard wears a ring in the color of the focus that breathes, like the cursor of a
  console (still when the system asks for less motion); the numbers of the overview, the sagas and the steps of the
  setup show a ring of the same color.
- Updates: each card shows the jump of version in large (the version you have, an arrow and the new one) and ignoring
  its updates is a small button with an eye at the side of the date, instead of a full-width button on every card.
- Settings on a phone: all the sections are in sight as pills on two or three rows, instead of a row where only two
  of ten could be seen; the notice of the setup wizard puts its button under the text.
- The cards of "You may like" have the width of the cards of the library and all the same height.
- Space says how many files wait for the verification when nothing can be freed yet, instead of a lonely "0 B"; the
  buttons to copy to the SD card sit on the line of the folder; the note of Tasks on the server is a short line
  instead of a box larger than the page.
- Upcoming: the months follow one another in the same grid, each one opened by a tile with its name and how many
  games come out, instead of a row of the whole width for each month with often a single game.
- DLC: the games sit side by side on a wide screen, with a bigger cover and the bar of the DLC you have, as in the
  library.
- Compress: the files of the same game are listed together under its name, with how many there are and their size; the
  name of a file stays on one line (its full path shows on hover) and a line explains the bar under each size.
- The header of each page wears the icon of the page and a soft light in the color of its group of the menu: red for
  the library, cyan for what to discover, violet for the tools and grey for the settings of the app.
- Series: bigger covers laid out as a fan, the missing games as dark silhouettes and the bar in cyan (green once the
  series is complete) instead of the red of the actions.

## 1.41.5

### Improvements

- The Tasks link of the menu shows work going on with a turning fan inside a ring that fills with the progress of
  the task, and glows; the arrows of before were the ones of Synchronize.

### Fixes

- The Tasks link of the menu shows a task started after the page was opened (a button of the page, a
  synchronization, a scheduled or automatic task): it only followed the tasks when one was already running.
- The turning icon of Tasks in the side menu is no longer out of shape: it turns in a square box, around its center.
- Spanish: the first step of the setup wizard is headed "Bienvenido".

## 1.41.4

### Fixes

- Compress: the files nothing could be compressed in (games in sparse storage and the like) are no longer offered
  again after a compression. They were noted with the size of their progress, the size times the passes of the
  task, so they never matched again; the ones noted that way are recognized too, without trying them once more.

## 1.41.3

### Fixes

- Deleting the empty folders after organizing is fast also with a few of them: the app asked the disk about every
  file of the library and then opened every folder again to find them, which took minutes on a NAS; now one walk of
  the folders tells which ones are empty.

## 1.41.2

### Fixes

- The hidden temporary file a compression, decompression or conversion leaves when the app is stopped in the middle,
  and that no task going on removes (a task cancelled, from an older version, or whose game was moved), is removed
  after the next scan once nothing writes it for half an hour; so are the uploads to the save vault that were cut.
  It took gigabytes nobody could see. The activity log says how many files and how much space; only files with the
  names the app gives its temporary files are touched.
- A damaged game file that claims a table of names of gigabytes is refused before that memory is taken, instead of
  being able to bring the app down; containers are read up to 65,536 files, far more than any game has. Found by
  the new fuzz tests, which throw random bytes at the readers of NSP, XCI, RomFS, the content metadata and the file
  names, a little on every change and every week.

### Security

- Each release carries its signed provenance as a file (`.intoto.jsonl`), to check the programs offline.

## 1.41.1

### Improvements

- Tasks: a compression, decompression, conversion or packing says its speed, how long is left and about when it ends
  ("2h 15m · 18:40", with the day when it ends another day), as the checks and the copies to the SD card already did;
  the time left reads as hours and minutes instead of "2h15m0s".
- The cards at the top of the library come alive: the color of each one drifts slowly behind it at its own pace,
  with a second glow in the other corner, and pointing at one makes its glow grow and its icon turn a little (still
  when the system asks for reduced motion).
- Settings → Backup: the password fields show their whole hint, also on a phone.
- Animations wait to be seen: the bars, rings and charts of Statistics, the sagas and the other pages fill when they
  scroll into view, instead of at the load of the page, out of sight. The stat cards of every page drift in color
  like the cards of the library.
- Tune-up shows a ring with how many steps are done, the next step breathes in its color and a done step pops its
  check. A complete saga wears the golden aura of the favorites. The countdown of the next release closes a ring as
  the day comes near.
- A long description of a game shows its start, with "Show more" for the rest. The check of an up to date game pops
  in, and the arrow of a new version nudges when pointed at.
- After an update, the library opens a small window with what is new in that version, in the language of the
  interface, and a link to all the changes; it shows once per version, and a box stops it for good. A new
  installation shows nothing.

## 1.41.0

### Improvements

- Settings → Backup: a backup can be downloaded with a password, encrypted (AES-256 with a key derived by Argon2id),
  for keeping it outside the server; it holds the password hashes of the users and the tokens of the notifications.
  Restoring asks for the password of an encrypted backup; one without a password works as before.

### Security

- The app is built with Go 1.27.2 instead of 1.26.0, which fixes known vulnerabilities of the standard library in the
  HTTP server, TLS and the page templates; `golang.org/x/net`, `golang.org/x/crypto` and `klauspost/compress` are
  updated for the same reason.
- At most 1024 connections are kept open at once, and the heavy requests (the exports and the preview of Organize)
  run two at a time: clicking again and again, or a script, no longer leaves a NAS without disk or processor.
- Supply chain: the GitHub Actions are pinned by commit and the base image of Docker by digest, so a moved tag cannot
  change what is built; the checkouts keep no credentials; the titles data is generated with NUT at a fixed commit in
  a job that can only read, and published by another job that runs no code of other projects; a new Security
  workflow looks for known vulnerabilities of Go and npm on every change and every week.
- Every program of a release and every Docker image comes with a signed provenance (Sigstore, through GitHub) that
  proves it was built by this repository from the tagged source, and the images carry their SBOM; the README tells
  how to check them with `gh attestation verify`.
- CodeQL looks for security mistakes in the Go and JavaScript code on every change and every week, OpenSSF Scorecard
  rates the practices of the project weekly, and SECURITY.md tells how to report a vulnerability privately.
- The titles database is downloaded from addresses on the internet only, as the covers already were, so an address
  set in settings.json cannot make the app call services of the local network; the in-memory caches of the logins
  that passed are keyed with a secret made at each start; the workflows give no permissions by default.

## 1.40.2

### Improvements

- Compress: a game in sparse storage, a layout of some recent games that no compressor can shrink yet (nsz neither),
  is now told as such instead of with a technical message; it stays as it is and installs normally. A file nothing
  could be compressed in is remembered and no longer offered, so compressing the whole library does not stop at it
  each time; the page lists those files apart, and one replaced by another file is offered again.

### Fixes

- Tasks: the list of the files a running compression has done is no longer drawn again while it is scrolled (the bar
  dragged or a finger sliding), which cut the gesture every second and kept it from going down.

## 1.40.1

### Fixes

- Organize no longer wants to move a game whose name has no letter for the Switch-safe names (a name only in Chinese
  or Korean) into a folder called `_`, again and again: it keeps the name of its file, or Unknown Title.
- Compress shows from the start how many files are selected, the small ones in the last row too, and Tune-up says
  "1 change is waiting" and "1 damaged file" in the singular.
- Without HTTPS the browser console no longer fills with warnings about the opener policy on every page, nor with an
  error when a transition between pages is skipped.
- Save vault: an upload cut in the middle (the console going to sleep, the Wi-Fi) leaves the previous save as it
  was, instead of a cut file in its place.
- Automations: the new files they are working on are kept until they are done, so a restart in the middle or the
  automations turned off meanwhile put them back in the queue instead of losing them.
- Reloading the keys before a synchronization no longer leaves a moment without keys for a compression that is
  running.
- Space deletes the copy with the extra name ("Game (2).nsz") instead of the clean one also when the copy is the file
  in the library, and only when the clean file holds all the copy holds; before, it was always shown as in use.
- A download of the titles database that fails because the disk is full is not downloaded again and again.
- Restoring a backup says why it failed also when the answer is not the app's (a file too large for a proxy), the
  errors of a form are shown even for a field the page does not have, the Tasks page and the task indicator go on
  updating when the page is opened again with the browser's Back button, and filtering a long list of files fast no
  longer shows the rows of an older filter.

## 1.40.0

### Security

- Without login the app answers only to addresses, names without a domain and names of the local network (`.local`,
  `.lan`, `.home.arpa`, `.internal`, `.ts.net`…): a web site could point a name of its own at the address of the app
  (DNS rebinding) and use it from the browser of someone on the network. Other names can be listed in the new
  `SLM_ALLOWED_HOSTS` setting (also in the Unraid template).
- Cloudflare's `CF-Connecting-IP` header is believed only when the request came through Cloudflare (a tunnel, a proxy
  reached from a Cloudflare address, or `SLM_TRUSTED_PROXIES`): through another proxy a client could send a new
  address with each attempt and dodge the login protection.
- When the login protection remembers too many addresses, it makes room with the ones that block nothing: an attack
  from many addresses no longer frees the ones it got blocked. Password checks run a few at a time, so a flood of
  logins leaves the server responsive, and a wrong current password in My account counts as a failed login.

### Improvements

- Faster pages on large libraries: the files still to check, the files of each game and the tasks list are worked out
  once per change instead of on every request, sizes are measured outside the locks, and the progress of a long
  compression is saved every 30 seconds instead of after each file.

### Fixes

- **Files of the library**
  - Originals are deleted after a compression, decompression or conversion only when the new file was checked whole;
    one checked only in part keeps them. A task that goes on after a restart removes the half written file first.
  - Copies to the SD card are written to the card before they count as copied.
  - Space never offers an NSZ as a duplicate of the NSP it was made from, and its cleaning never deletes a file
    together with the copy that should be kept. Issues offers deletion only for damaged files.
  - The parts of a split game (`00`, `01`…) are never moved or deleted one by one, and the compression list leaves out
    old updates that are not in use.
  - A library folder that is missing (a disk or a share not mounted) stops the scan instead of emptying the library.
- **Scans and background work**
  - A scan asked for while another task runs is done right after it, instead of being forgotten; files the app wrote
    itself during a scan are no longer reported as changed by the folder watcher.
  - The compression that goes on after a restart goes before the automatic compression of new files, and is kept for
    the next start when another task keeps it from starting; with the automatic compression on, it could be lost. The
    nightly compression marks the night as done only once it started or there is nothing left, and the automatic
    compression of new files is also resumed after a restart.
  - Covers found from other copies of a game are used, and the covers no longer start again in a loop when paused for
    a scan.
- **Data files**
  - Settings, favorites, wishlist, collections, history, activity, tasks, verifications and the other files of the data
    folder are saved through a temporary file of their own, on the disk before it takes the place of the old one: two
    saves never mix and a crash leaves the previous version. A file that cannot be read is kept aside as
    `name.corrupt-date` and reported, instead of being taken for empty and written over; settings that cannot be opened
    are no longer replaced by the defaults.
  - Backups take the verification results of large libraries (up to 64 MB per file), refuse files repeated inside,
    and restoring one no longer reports the games added since as new.
  - The versions of the titles database are kept only once the files were read: a download cut in the middle is
    downloaded whole the next time instead of being taken as current.
- **Notifications**
  - A notification reaches the channels that work even if another one fails, and is not sent again to them each time;
    long messages are cut to fit Discord and Telegram. Nothing is reported as new right after the start, before the
    library is loaded, and turning a kind of notification back on does not report everything missed meanwhile.
  - The low disk space warning is tried again when it could not be sent.
- **Pages**
  - Pages reload after a synchronization also with login on, and go to the login when the session ended.
  - Copying a title ID works without HTTPS (a NAS opened by its address).
  - Buttons come back after a request that failed, and a task that did not start is not followed.
  - The cursor of the Statistics charts shows again, and Esc no longer brings back the first-run wizard.
  - Regions of the same game are grouped only when they are really different regions, and demos are told apart.
  - Statistics, a game and Space count each file once in their sizes.
- Update notes are cut at a whole character, and a failed automatic backup is tried again an hour later instead of
  every minute.

## 1.39.2

### Improvements

- Wording: the texts name installers "such as DBI", and the README speaks of a key dumping tool without naming one.

### Fixes

- A compression, decompression or conversion the app is stopped in the middle of (a restart, an update, a backup of
  the server at night) goes on by itself with the files it had left once the app is started again, shown in Tasks as
  "Going on after a restart"; one cancelled by hand does not. The task also keeps what it did as it goes: it said
  nothing of the files it had compressed before it was stopped.
- The menu shows next to Missing Games how many games are missing, as the page counts them, in grey; it showed the
  games on the wishlist in red, which read as "only 2 games missing". The wishlist keeps its hearts and its filter on
  the page.

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
