#Requires -Version 5.1
# SPDX-License-Identifier: Apache-2.0
param([switch] $CheckOnly, [switch] $Installer)
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\windows-toolchain.ps1"

# The checkout stays in place, including local edits. Tool and native output paths
# are fixed through B: so installation directories and temporary paths cannot enter
# the binaries. Junctions reuse installed tools without requiring symbolic-link privileges.
# Use QT_ROOT_DIR or MINGW_BIN to identify installations outside the discovered locations.

function Invoke-Native([string] $Program, [string[]] $Arguments) {
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program $($Arguments -join ' ') failed ($LASTEXITCODE)." }
}

function Remove-ToolJunctions([string] $Directory) {
    foreach ($name in $junctionNames) {
        $junction = Join-Path $Directory $name
        if (Test-Path -LiteralPath $junction) {
            $item = Get-Item -LiteralPath $junction -Force
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                [IO.Directory]::Delete($junction)
            }
        }
    }
}

function Test-BuildCheckout([string] $Git, [string] $Checkout) {
    # Git can report a clean checkout even when it has converted LF to CRLF on disk.
    $converted = @(Invoke-Native $Git @('-C', $Checkout, 'ls-files', '--eol') | Where-Object {
        if ($_ -match '^i/(lf|crlf|mixed|none)\s+w/(lf|crlf|mixed)\s+attr/([^\t]*)\t') {
            $index, $working, $attributes = $Matches[1], $Matches[2], $Matches[3]
            $expected = if ($index -eq 'none') { 'lf' } else { $index }
            # Forced text converts even mixed input. Git preserves existing CRLF in text=auto blobs.
            if ($attributes.Trim() -eq 'text eol=crlf' -or
                ($expected -eq 'lf' -and $attributes -match '\beol=crlf\b')) {
                $expected = 'crlf'
            }
            $working -ne $expected
        }
    })
    if ($converted.Count) {
        $examples = ($converted | Select-Object -First 5) -join [Environment]::NewLine
        throw ("Unexpected line endings in $($converted.Count) tracked paths under $Checkout.`n" +
            "Refresh your clean checkout or correct the line endings in edited files, then rebuild.`n" +
            "Run 'git ls-files --eol' there to see the index (i/) and working tree (w/).`n$examples")
    }
    if (!(Test-Path -LiteralPath "$Checkout\.gitmodules")) { return }
    $links = Invoke-Native $Git @('-C', $Checkout, '-c', 'core.quotepath=false', 'ls-files', '--stage') |
        Where-Object { $_ -like '160000 *' }
    foreach ($link in $links) {
        $entry, $path = $link -split "`t", 2
        if (!$entry.EndsWith(' 0')) { throw "Resolve the submodule conflict at $Checkout\$path." }
        $module = Join-Path $Checkout $path
        if (!(Test-Path -LiteralPath "$module\.git")) {
            throw "Missing submodule $path. Run 'git submodule update --init --recursive'."
        }
    }
}

function Test-QtPaths([string] $Qt) {
    $query = @{}
    foreach ($line in (Invoke-Native "$Qt\bin\qtpaths.exe" @('--query'))) {
        $key, $value = $line -split ':', 2
        $query[$key] = $value
    }
    if ($query.QT_VERSION -ne $pins.qt.version -or $query.QMAKE_XSPEC -ne 'win32-msvc') {
        throw "Expected the Qt $($pins.qt.version) win32-msvc kit at $Qt."
    }
    $dirs = @{
        QT_INSTALL_PREFIX = '.'; QT_INSTALL_BINS = 'bin'; QT_HOST_BINS = 'bin'; QT_INSTALL_LIBEXECS = 'bin'
        QT_INSTALL_HEADERS = 'include'; QT_INSTALL_LIBS = 'lib'; QT_INSTALL_PLUGINS = 'plugins'
        QT_INSTALL_DATA = '.'; QT_INSTALL_QML = 'qml'; QT_INSTALL_TRANSLATIONS = 'translations'
    }
    foreach ($key in $dirs.Keys) {
        $expected = Get-PhysicalDirectory (Join-Path $Qt $dirs[$key])
        if (!$query[$key] -or (Get-PhysicalDirectory $query[$key]) -ne $expected) {
            throw "The kit at $Qt resolves $key to '$($query[$key])'. Repair its qt.conf to use $expected."
        }
    }
}

function Test-DeployedBinaries([string] $Payload) {
    $expected = @('BitBox.exe', 'libserver.dll', 'breez_sdk_spark_bindings.dll') +
        $pins.qt.runtime + $pins.redist.files.PSObject.Properties.Name
    $actual = Get-ChildItem -LiteralPath $Payload -Recurse -File |
        Where-Object { $_.Extension -in @('.dll', '.exe') } |
        ForEach-Object { $_.FullName.Substring($Payload.Length + 1).Replace('\', '/') }
    $differences = @(Compare-Object $expected $actual -CaseSensitive)
    if ($differences) {
        foreach ($item in $differences) {
            $label = if ($item.SideIndicator -eq '=>') { 'Unexpected' } else { 'Missing' }
            Write-Host "$label deployed binary $($item.InputObject)"
        }
        throw "Qt deployment differs from the release. Check the components of $($tools.qt) against the toolchain manifest."
    }
}

if ($env:OS -ne 'Windows_NT' -or ![Environment]::Is64BitProcess) {
    throw 'Run this script in 64-bit PowerShell on Windows.'
}
$pins = Get-Content -Raw -LiteralPath "$PSScriptRoot\windows-toolchain.json" | ConvertFrom-Json
$tools = Get-VerifiedToolchain $pins (Get-ToolCandidates)
$root = (Get-Item "$PSScriptRoot\..\..").FullName
# Resolve the optional installer tool before replacing PATH with the pinned build toolchain.
$nsis = $null
if ($Installer) {
    $nsis = (Get-Command makensis.exe -CommandType Application -ErrorAction SilentlyContinue).Source
    if (!$nsis) { throw 'NSIS is required for installer packaging. Install it with: choco install nsis' }
}
$original = [Environment]::GetEnvironmentVariables()
$subst = "$env:SystemRoot\System32\subst.exe"
$cmd = "$env:SystemRoot\System32\cmd.exe"
$work = $null
$mapped = $false
$junctionNames = @('git','go','node','mingw','qt','vs','kits','redist')
try {
    # Start with OS facilities only. Compiler flags, Go/npm configuration, Qt overrides,
    # Visual Studio developer shells, shell startup files and the caller's PATH must not leak in.
    $keep = @('SystemRoot','SystemDrive','windir','ProgramFiles','ProgramFiles(x86)','ProgramW6432',
        'ProgramData','LOCALAPPDATA','APPDATA','USERPROFILE','USERNAME','COMPUTERNAME','OS','PROCESSOR_ARCHITECTURE',
        'PSModulePath','TEMP','TMP','HTTP_PROXY','HTTPS_PROXY','NO_PROXY','SSL_CERT_FILE','NODE_EXTRA_CA_CERTS')
    foreach ($key in $original.Keys) {
        if ($key -notin $keep) { [Environment]::SetEnvironmentVariable($key, $null) }
    }
    $env:ComSpec = $cmd
    $env:PATHEXT = '.COM;.EXE;.BAT;.CMD'
    $env:PATH = "$($tools.qt)\bin;$($tools.redist);$env:SystemRoot\System32;$env:SystemRoot"
    if ($CheckOnly) {
        Test-QtPaths $tools.qt
        return
    }
    Test-BuildCheckout "$($tools.git)\cmd\git.exe" $root

    if ([IO.DriveInfo]::GetDrives().Name -contains 'B:\') { throw 'Drive B: must be free for the fixed build paths.' }
    $work = Join-Path $PSScriptRoot 'build'
    if ([IO.Directory]::Exists($work)) {
        # A stopped build may have left tool junctions. Unlink them before clearing the build.
        Remove-ToolJunctions $work
        Push-Location -LiteralPath $PSScriptRoot
        try { Invoke-Native $cmd @('/d', '/c', 'rmdir', '/s', '/q', 'build') }
        finally { Pop-Location }
    }
    [void][IO.Directory]::CreateDirectory($work)
    Invoke-Native $subst @('B:', $work)
    $mapped = $true
    if (!(Get-PSDrive -Name B -ErrorAction SilentlyContinue)) {
        [void](New-PSDrive -Name B -PSProvider FileSystem -Root 'B:\' -Scope Script)
    }
    # Junctions give tools stable paths while reusing the checked installations in place.
    foreach ($name in $junctionNames) {
        [void](New-Item -ItemType Junction -Path "B:\$name" -Target $tools[$name])
    }
    foreach ($dir in @('temp','config')) { [void][IO.Directory]::CreateDirectory("B:\$dir") }
    $env:TEMP = $env:TMP = 'B:\temp'
    # Keep Git's system and user config so status uses the checkout's line-ending rules.
    $env:GIT_TERMINAL_PROMPT = '0'
    $env:GOROOT = 'B:\go'
    $env:GOTOOLCHAIN = 'local'
    $env:GOENV = 'off'
    # Always use this checkout's vendored modules, even inside a larger Go workspace.
    $env:GOWORK = 'off'
    $env:GOCACHE = 'B:\temp\go-cache'
    $env:GOPATH = 'B:\temp\gopath'
    $env:CC = 'gcc'
    $env:CXX = 'g++'
    $env:NPM_CONFIG_USERCONFIG = 'B:\config\.npmrc'
    $env:NPM_CONFIG_GLOBALCONFIG = 'B:\config\global.npmrc'
    $env:NPM_CONFIG_PREFIX = 'B:\node'
    $env:NPM_CONFIG_CACHE = 'B:\temp\npm-cache'
    $env:LC_ALL = $env:LANG = 'C'
    $env:TZ = 'UTC'
    $env:QT_HASH_SEED = '0'
    $env:BITBOX_WINDOWS_MAKE = 'B:/mingw/bin/mingw32-make.exe'
    $env:BITBOX_QT_BIN = 'B:/qt/bin'
    $buildDirectory = 'B:/'
    $payload = 'B:/windows'
    $env:BITBOX_VS_ROOT = 'B:\vs'
    $env:BITBOX_VC_VERSION = $pins.msvc.directory
    $env:BITBOX_SDK_VERSION = $pins.sdk.directory
    $vc = "B:\vs\VC\Tools\MSVC\$($pins.msvc.directory)"
    $sdkBin = "B:\kits\bin\$($pins.sdk.directory)\x64"
    $sdkInclude = "B:\kits\Include\$($pins.sdk.directory)"
    $sdkLib = "B:\kits\Lib\$($pins.sdk.directory)"
    $env:BITBOX_VC_BIN = "$vc\bin\Hostx64\x64"
    $env:BITBOX_CRT = 'B:\redist'
    $env:BITBOX_INCLUDE = "$vc\include;$sdkInclude\ucrt;$sdkInclude\shared;$sdkInclude\um;$sdkInclude\winrt;$sdkInclude\cppwinrt"
    $env:BITBOX_LIB = "$vc\lib\x64;$sdkLib\ucrt\x64;$sdkLib\um\x64"
    $systemPath = "$env:SystemRoot\System32;$env:SystemRoot;$env:SystemRoot\System32\Wbem;$env:SystemRoot\System32\WindowsPowerShell\v1.0"
    $env:BITBOX_COMPILE_PATH = "$env:BITBOX_VC_BIN;$sdkBin;B:\qt\bin;$env:BITBOX_CRT;B:\node;B:\go\bin;B:\mingw\bin;B:\git\cmd;B:\git\usr\bin;$systemPath"
    $env:PATH = $env:BITBOX_COMPILE_PATH
    Test-QtPaths 'B:\qt'
    # Additional plug-ins installed for other projects must not expand the release payload.
    $pluginNames = $pins.qt.runtime | Where-Object { $_.Contains('/') } | ForEach-Object { [IO.Path]::GetFileNameWithoutExtension($_) }
    $exclude = Get-ChildItem -Path 'B:\qt\plugins\*\*.dll' -File |
        Where-Object { $_.BaseName -notin $pluginNames } | ForEach-Object BaseName | Sort-Object -Unique
    $env:BITBOX_QT_EXCLUDE_PLUGINS = $exclude -join ','
    [IO.File]::WriteAllText('B:\qt-build.conf', "[Paths]`nPrefix=B:/qt`nHostPrefix=B:/qt`n")

    $env:SOURCE_DATE_EPOCH = Invoke-Native 'B:\git\cmd\git.exe' @('-C', $root, 'show', '-s', '--format=%ct', 'HEAD')
    Write-Host "Building the working tree at $root"
    Push-Location -LiteralPath $root
    try {
        Invoke-Native 'B:\git\usr\bin\bash.exe' @('--noprofile', '--norc', 'frontends/qt/make_windows.sh', $buildDirectory)
    } finally { Pop-Location }

    Test-DeployedBinaries $payload
    $version = [IO.File]::ReadAllText("$root\APP_VERSION").Trim()
    $destination = Join-Path $work "BitBox-$version-win64-build.tar.gz"
    Invoke-Native 'B:\git\usr\bin\bash.exe' @('--noprofile', '--norc', "$PSScriptRoot/archive_windows.sh",
        $buildDirectory, $destination.Replace('\', '/'))
    Write-Host "Created $destination (SHA-256 $((Get-FileHash -LiteralPath $destination).Hash))"

    # CI packages the same application files as the archive.
    if ($Installer) {
        Push-Location -LiteralPath $PSScriptRoot
        try {
            Invoke-Native $nsis @("-DVERSION=${version}.0", "-DBINDIR=$payload",
                '-DICONFILE=resources/win/icon.ico', 'setup.nsi')
        } finally { Pop-Location }
    }
} finally {
    foreach ($key in [Environment]::GetEnvironmentVariables().Keys) {
        if (!$original.ContainsKey($key)) { [Environment]::SetEnvironmentVariable($key, $null) }
    }
    foreach ($key in $original.Keys) { [Environment]::SetEnvironmentVariable($key, $original[$key]) }
    if ($mapped) { Invoke-Native $subst @('B:', '/D') }
    # Keep build outputs, but remove links to the installed tools.
    if ($work -and [IO.Directory]::Exists($work)) {
        Remove-ToolJunctions $work
    }
}
