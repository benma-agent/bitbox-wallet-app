The BitBoxApp supports building on X86_64 machines. Building on 32bit
systems may be possible but it is explicitly unsupported. The following
platforms should be viable for development, building, and use of the BitBox
Wallet application.

* Debian: 11.6+
* Ubuntu: 22.04+
* Fedora: 36+
* MacOS: 12+
* Windows: Windows 10+

## Debian, Ubuntu, and Fedora GNU/Linux with Docker

Install [Docker]
(https://docs.docker.com/install/linux/docker-ce/):

Initialize the Docker image:
`make dockerinit`

Enter the Docker environment:
`make dockerdev`

Within the Docker dev environment, build the QT frontend:
`make qt-linux`

Build artifacts:
* `frontends/qt/build/linux/bitbox-4.0.0-1.x86_64.rpm`
* `frontends/qt/build/linux/bitbox_4.0.0_amd64.deb`
* `frontends/qt/build/linux/BitBox-x86_64.AppImage`

## MacOS

Install Go 1.26.10, Qt and create-dmg. Note that qt@6 from homebrew does **not** work as it is missing the
`rcc` tool.

```
# Install Go. Can also use the official installer
brew install go@1.26
brew install create-dmg
# Install Qt. Can also use the official installer.
pip install aqtinstall
aqt list-qt mac desktop --arch 6.8.2
aqt install-qt mac desktop 6.8.2 --modules qtpositioning qtserialport qtwebchannel qtwebengine --outputdir ~/Qt
```

Make sure you have `qt@6/bin`,  `qt@6/libexec`, `go@1.26/bin` and `go/bin` in your PATH, i.e. add to your `.zshrc`:

```bash
export PATH="$PATH:$HOME/Qt/6.8.2/macos/bin"
export PATH="$PATH:$HOME/Qt/6.8.2/macos/libexec"
export PATH="$(brew --prefix go@1.26)/bin:$PATH"
export PATH="$PATH:$HOME/go/bin"
```

Build the QT frontend for MacOS:
`make qt-osx`

Build artifacts:
* `frontends/qt/build/osx/BitBox.app`

### Signing & Notarization

Requires Xcode 10+ and macOS 12+.

```
$ # Sign with hardened runtime:
$ codesign -f --deep --strict --timestamp -o runtime --entitlements frontends/qt/resources/MacOS/entitlements.plist -s CODESIGN_IDENTITY frontends/qt/build/osx/BitBox.app
$ # Create DMG installer
$ make osx-create-dmg
$ # Notarize
$ xcrun notarytool submit --apple-id "APPLE_ID" --team-id "TEAM_ID" --password "PASSWORD" frontends/qt/build/osx/BitBox_Installer.dmg
$ # Check notarization status
$ xcrun notarytool info --apple-id "APPLE_ID" --team-id "TEAM_ID" --password "PASSWORD" NOTARIZATION_ID
```

If you don't know your TEAM_ID, you can find it in your Apple dev account or with:

```
xcrun altool --list-providers --username "APPLE_ID" --password "PASSWORD"
```

## Windows

### Build the release archive

On a Windows 10 or later x64 PC, open 64-bit Windows PowerShell in the repository root. Check the
installed tools before the first build.

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\windows-archive.ps1 -CheckOnly
```

The check covers Git for Windows with Bash, Go, Node.js with its bundled npm,
MinGW-w64 with GNU Make, the Qt MSVC kit and its modules, the MSVC x64 toolset and CRT, and the
Windows SDK. It prints the selected paths, missing or conflicting installations, required versions
and download links. Install or replace the reported components manually, then rerun the check.
For missing or mismatched MinGW files, run the versioned Chocolatey command printed by the
check in an administrator PowerShell window. Windows CI installs the same package version.
Visual Studio 2022 Build Tools, Community and Enterprise can supply the required components.
The build accepts different Git for Windows versions. It uses Git to identify the source commit
and initialize missing submodules, Git Bash to run the Make recipes, and GNU tar and gzip when
packaging. The pinned tool versions and file hashes are in
[windows-toolchain.json](../scripts/windows-toolchain.json).

The script checks standard install locations, registered Visual Studio and SDK locations, and tools
visible on `PATH`. For a custom Qt or MinGW location, set `QT_ROOT_DIR` to the Qt kit directory
or `MINGW_BIN` to the MinGW `bin` directory for the current PowerShell session. Multiple discovered
installations of a tool on a local PC are reported together for manual resolution.

Build the archive with one command. The dependency check runs again as part of the build.

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\windows-archive.ps1
```

If you already use `make` in Git Bash, `make qt-windows-archive` calls the same entry point. The
build itself uses GNU Make from the verified MinGW installation. When building the current
commit, the script uses your working tree, including local edits and initialized submodules.
Drive `B:` must be free. The script temporarily maps `B:\src` to your checkout and gives the
verified tools fixed paths. It refreshes the web build and npm dependencies in `frontends/web`.
Qt and the Go server write their native outputs to a fresh temporary build directory.
Git Bash, GNU Make, the compilers, qmake and windeployqt run with a build-specific environment.
The script checks the deployed EXEs and DLLs against the release file list. If Qt
deploys additional libraries, the error names them so you can adjust the kit's optional modules.
The Windows build compiles the Go server with the dependencies in `vendor/`. The release archive
uses the commit timestamp and normalized tar metadata.

The command prints the archive SHA-256 and writes
`frontends\qt\build\BitBox-<APP_VERSION>-win64-build.tar.gz`. An explicit `-Commit` different
from the checkout's `HEAD` uses a temporary checkout of that local commit. From a source ZIP,
pass `-Commit <full-40-character-SHA>` and, for a fork, `-Repository owner/repo` to fetch the
revision into a temporary checkout. Run the scripts from the revision being built. If Windows marks
a downloaded ZIP as an Internet file, select **Unblock** in its Properties before extracting it.

For an unsigned development installer built from the working tree, install NSIS, put its
`makensis` and the development tools on Git Bash's `PATH`, and run `make qt-windows`. This writes
`frontends\qt\BitBox-installer.exe` and deploys the app into `frontends\qt\build\windows\`.

### Verify the Windows build

For a two-PC check, use clean working trees at the same commit and the recorded submodule revisions.
Go records the commit and working-tree modification status in `libserver.dll`. Verification checks
submodule revisions and reports tracked files whose line endings differ from the Git index.
For a new Windows checkout, `git clone -c core.autocrlf=false <repository-url>` preserves the
committed line endings, as in CI. On the first PC, build into a new directory.

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\windows-archive.ps1 -OutputDirectory C:\builds\bitbox-reference
```

Copy that directory to the second PC, or share it over the network. On the second PC, rebuild and
compare with the reference folder.

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\windows-archive.ps1 -Reference C:\builds\bitbox-reference
```

Passing `-Reference` enables verification. It accepts the flat release folder or an existing
unsigned build `.tar.gz`.
For a CI reference, authenticate GitHub CLI once with `gh auth login` and use `-Verify` alone.
The script downloads the artifact from a successful `ci.yml` push run for the exact commit and
reads its tar.gz contents once. Use `-Repository owner/repo` for a fork.

Verification compares case-sensitive relative paths and the SHA-256 of every file under
`windows/` plus `APP_VERSION`, `icon.ico`, `setup.nsi`, and `sign-windows-release.py`. It reports
each missing, extra, or changed file and exits with an error on differences. File timestamps,
tar metadata, and gzip encoding can differ. Verification alone finishes at the comparison.
To produce the Linux signing archive in the same run, add `-CreateArchive` to either verification
command. The script packages the verified local files after a successful comparison.

To compare two folders you have already built, run the verifier directly from the repository root.

```powershell
go run -mod=vendor ./scripts/verify-windows-build C:\builds\bitbox-local C:\builds\bitbox-reference
```

### Sign and package on Linux

Copy the trusted release archive to the Linux signing machine.

On Debian or Ubuntu, install the packaging tools on the signing machine with apt.

```sh
sudo apt update
sudo apt install python3 nsis osslsigncode 7zip
```

On Linux, set `archive` to the local path of the trusted release archive, then extract it and run
the bundled script.

```sh
work=$(mktemp -d)
tar -xzf "$archive" -C "$work" &&
    python3 "$work/sign-windows-release.py" /path/to/sign-file
```

`sign-file` takes one absolute EXE or DLL path, signs and timestamps it in place, and exits nonzero on failure.
The release script verifies each newly signed file and writes `BitBox-<APP_VERSION>-win64-installer.exe`
to the current directory. The signing machine trusts the Windows build to supply authentic, intact
pre-signed DLLs and EXEs such as `D3Dcompiler_47.dll` and `QtWebEngineProcess.exe`. If `osslsigncode`
can extract a signature from a DLL or EXE in the archive, the script skips signing that file.
NSIS compresses the `windows/` directory into the installer, preserving the embedded signatures.
If NSIS is installed outside `/usr/share/nsis`, set `NSISDIR` to the directory containing `Plugins/`.

## Android

### Build
Enter the Docker environment: `make dockerdev`

Within the Docker dev environment, build the **debug version** of the Android App: `make android`

To update the app icon, execute `frontends/android/mkicon.sh`.
The script isn't run during `make android` build.

Build artifacts:
* `frontends/android/BitBoxApp/app/build/outputs/apk/*`

### Deploy
Adb is required for the deploy, on GNU/Linux install `android-tools-adb`

After connecting the device via USB, it is possible to verify the connection with `adb devices`

Inside `frontends/android` folder: `make deploy-debug`

### Release build

To assemble the signed release .apk and .aab files, run `make android-assemble-release`.

The keystore file `frontends/android/BitBoxApp/app/bitboxapp.jks` must be present.

Build artifacts:
* `frontends/android/BitBoxApp/app/build/outputs/apk/release/app-release.apk`
* `frontends/android/BitBoxApp/app/build/outputs/bundle/release/app-release.aab`


### Deploy troubleshooting
If the apk install goes wrong, here are some Android configuration that could help:
* Enable developer options
* Enable install via USB
* Enable USB debugging
* Set USB configuration to charge when the device is connected
* Disable MIUI optimization and restart (for Xiaomi devices)

## iOS

The instructions here are preliminary, as the iOS app is still in development.

To build the app and run it in the simulator:

   cd frontends/ios
   make prepare


Open XCode, load the project in /frontends/ios/BitBoxApp/BitBoxApp.xcodeproj.

In the menu, hit Project->Run (or ⌘R).

## Cross compile from GNU/Linux to Windows
It is not currently possible to cross compile the BitBox wallet for Windows.
The `qtwebwidgets` QT module only supports native building on Windows. It is
possible to cross compile `libserver.so` library for Windows from GNU/Linux.

Enter the Docker environment:
`make dockerdev`

Cross compile the library:
`cd frontends/qt/server/ && make windows-cross`

Build artifacts:
* `frontends/qt/server/libserver.dll`
* `frontends/qt/server/libserver.h`
