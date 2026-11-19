// Package edensync keeps the saves of Eden (and Citron) on a computer in step with the saves of
// Switch Library Manager Web, where NXDock uploads the ones of the console: the save of a game
// changed in the emulator goes to the library, and a newer one of the library (of the console, or
// of the emulator of another computer) goes into the emulator, only while the emulator is closed
// and always leaving a copy in the library of the save it replaces. When both changed since they
// were last in step, nothing is replaced: both are kept in the library and the user chooses which
// one the emulator keeps.
//
// It is pure Go with no part of the web app, so NXPress uses it as it uses switchfs, and Switch
// Library Manager Web uses it when it runs on the computer where the emulator is.
//
// # The folders and files of the emulator
//
// Read in the source of Eden (GPL-3.0, https://git.eden-emu.dev/eden-emu/eden), never copied:
//
//   - the data folder (src/common/fs/path_util.cpp, fs_paths.h): "user" next to the program
//     when it is there (portable), else %APPDATA%\eden on Windows and $XDG_DATA_HOME/eden
//     (~/.local/share/eden) on Linux and macOS; Citron is the same with "citron". The NAND folder
//     is nand in it, unless nand_directory of the settings (qt-config.ini) names another one.
//   - the profiles: nand/system/save/8000000000000010/su/avators/profiles.dat (see profiles.go).
//   - the save of a game of a profile: nand/user/save/0000000000000000/<user id>/<title ID>, or
//     nand/user/save/account/<UUID>/<title ID>/0 in newer builds (see profiles.go). The file
//     .yuzu_save_size in it belongs to the emulator, not to the game.
//
// # What NXPress calls
//
// Pairing, once, as NXDock does: AskPairing gives a code to show; the user accepts it in the
// library (the computer is listed under Eden on your computers, in Saves); CheckPairing every few
// seconds until it is PairAccepted gives the key, kept by NXPress.
//
// Then, every minute and when it starts:
//
//	folder, err := edensync.Find(edensync.Eden, "")      // or edensync.Open(edensync.Eden, path)
//	state, err := edensync.LoadState(statePath)          // a file of NXPress
//	syncer := &edensync.Syncer{Folder: folder, State: state, StatePath: statePath, Owner: edensync.OwnerNXPress,
//		Library: &edensync.HTTPLibrary{BaseURL: address, Key: key}}
//	report, err := syncer.Pass(ctx)                       // does nothing while Eden is open
//	err = edensync.Release(folder, edensync.OwnerNXPress) // when turned off, and as NXPress stops
//
// The Report tells what was uploaded, brought, which conflicts were told and which games wait
// (for the choice of the user, which NXPress may show too, or to be opened once in Eden). The
// profiles of Eden are read with Folder.Profiles; Library.Pairs tells which account of the
// console each one goes with, and ReportProfiles changes it.
//
// # One program at a time
//
// Switch Library Manager Web keeps the saves of the emulator of its own computer in step too, when
// it runs on it. Both on one computer would write the same save folders, so a pass first takes the
// lock of the data folder, slmw-eden-sync.lock in Folder.Root (see lock.go), and renews it on every
// pass: while the other program holds it and renews it, a pass does nothing and Report.HeldBy
// names that program, which HeldBy also tells the pages without taking it. A lock not renewed for
// LockStale, or of a process that no longer runs on this computer, is taken over; Release gives it
// up when the passes are turned off and as the program stops.
//
// # The library
//
// A pass sees the library through the interface Library, which HTTPLibrary implements with the
// paths of the network install, with the key of the computer as a bearer token:
//
//	GET  /install/saves                       {"saves": [LibrarySave…]}, every save of the user
//	GET  /install/saves/{titleId}/{id}        the ZIP of a save
//	POST /install/saves/{titleId}?emulator=eden&account=<profile name>&profile=<UUID>&auto=1
//	                                          the ZIP; answers {"id", "when", "size"}
//	GET  /install/saves/profiles              Pairs
//	PUT  /install/saves/profiles              {"emulator": "eden", "profiles": [ProfileReport…]}
//	POST /install/saves/conflicts             Conflict without id; answers {"id"}
//	GET  /install/saves/conflicts             {"conflicts": [Conflict…]} of this computer
//	POST /install/saves/conflicts/{id}/keep   {"keep": "console" or "emulator"}, the choice made
//	                                          on the computer, as on the page Saves
//	POST /install/saves/conflicts/{id}/done   the choice was applied
//
// The saves listed for a computer tell the hash of their files (LibrarySave.Hash), the one
// HashFolder and HashZip give, so a pass knows a save the emulator already holds without
// downloading it.
//
// # The decision
//
// Decide is the whole rule, without files or network: from the last time the save of a game of a
// profile was in step (State, by StateKey), the hash of its folder now, and the newest save of
// the library from a console and from an emulator, it tells DoNothing, DoUpload, DoBring or DoAsk.
// The saves of the library that count for a profile are the ones of the account of the console
// it goes with, and the ones of the profiles of any computer that go with the same account.
package edensync
