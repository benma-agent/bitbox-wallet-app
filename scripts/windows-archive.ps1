#Requires -Version 5.1
# SPDX-License-Identifier: Apache-2.0
[CmdletBinding(DefaultParameterSetName = 'Build')]
param(
    [Parameter(ParameterSetName = 'Check')][switch] $CheckOnly,
    [Parameter(ParameterSetName = 'Build')][Parameter(ParameterSetName = 'Folder')][switch] $Verify,
    [Parameter(ParameterSetName = 'Build')][Parameter(ParameterSetName = 'Folder')][ValidateNotNullOrEmpty()][string] $Reference,
    [Parameter(ParameterSetName = 'Build')][switch] $CreateArchive,
    [Parameter(Mandatory, ParameterSetName = 'Folder')][ValidateNotNullOrEmpty()][string] $OutputDirectory,
    [Parameter(ParameterSetName = 'Build')][Parameter(ParameterSetName = 'Folder')][ValidatePattern('^[a-fA-F0-9]{40}$')][string] $Commit,
    [Parameter(ParameterSetName = 'Build')][Parameter(ParameterSetName = 'Folder')][ValidatePattern('^[\w.-]+/[\w.-]+$')]
    [string] $Repository = 'BitBoxSwiss/bitbox-wallet-app'
)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT' -or ![Environment]::Is64BitProcess) {
    throw 'Run this script in 64-bit PowerShell on Windows.'
}
. "$PSScriptRoot\windows-toolchain.ps1"
$pins = Get-Content -Raw -LiteralPath "$PSScriptRoot\windows-toolchain.json" | ConvertFrom-Json
$hosted = $env:GITHUB_ACTIONS -eq 'true' -and $env:RUNNER_ENVIRONMENT -eq 'github-hosted'
$tools = Get-VerifiedToolchain $pins (Get-ToolCandidates -Hosted:$hosted) $hosted
$root = Split-Path $PSScriptRoot -Parent

function Invoke-Native([string] $Program, [string[]] $Arguments) {
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program $($Arguments -join ' ') failed ($LASTEXITCODE)." }
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

function Get-CIArchive([string] $GitHub, [string] $Sha, [string] $Work) {
    $runs = Invoke-Native $GitHub @('run', 'list', '--repo', $Repository, '--workflow', 'ci.yml',
        '--event', 'push', '--commit', $Sha, '--status', 'success', '--limit', '1', '--json', 'databaseId,headSha') |
        ConvertFrom-Json
    if (!$runs -or $runs[0].headSha -ne $Sha) { throw "No successful ci.yml push run for $Sha in $Repository." }
    $ci = Join-Path $Work 'ci-artifact'
    Invoke-Native $GitHub @('run', 'download', "$($runs[0].databaseId)", '--repo', $Repository,
        '--name', 'windows-build-archive', '--dir', $ci) | Out-Host
    $archives = @(Get-ChildItem -LiteralPath $ci -File -Filter 'BitBox-*-win64-build.tar.gz')
    if ($archives.Count -ne 1) {
        throw "Expected one Windows build archive in CI run $($runs[0].databaseId)."
    }
    Write-Host "Comparing with CI run $($runs[0].databaseId) for $Sha"
    return $archives[0].FullName
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

function Complete-WindowsBuild(
    [string] $WorkRoot, [string] $Reference, [string] $OutputDirectory, [string] $ArchiveDirectory
) {
    $checkout = Join-Path $WorkRoot 'src'
    $bundle = Join-Path $WorkRoot 'release'
    if ($Reference) {
        $go = Join-Path $WorkRoot 'go/bin/go.exe'
        Push-Location -LiteralPath $checkout
        try { Invoke-Native $go @('run', '-mod=vendor', './scripts/verify-windows-build', $bundle, $Reference) }
        finally { Pop-Location }
    }
    Test-DeployedBinaries (Join-Path $bundle 'windows')

    if ($OutputDirectory) {
        $parent = [IO.Directory]::CreateDirectory((Split-Path $OutputDirectory -Parent)).FullName
        $pending = Join-Path $parent ([IO.Path]::GetRandomFileName())
        try {
            Copy-Item -LiteralPath $bundle -Destination $pending -Recurse -Force
            [IO.Directory]::Move($pending, $OutputDirectory)
        } finally {
            if ([IO.Directory]::Exists($pending)) { Remove-Item -LiteralPath $pending -Recurse -Force }
        }
        Write-Host "Created $OutputDirectory"
        return
    }
    if (!$ArchiveDirectory) { return }

    $version = [IO.File]::ReadAllText((Join-Path $bundle 'APP_VERSION')).Trim()
    $output = [IO.Directory]::CreateDirectory($ArchiveDirectory).FullName
    $destination = Join-Path $output "BitBox-$version-win64-build.tar.gz"
    $pending = Join-Path $output ([IO.Path]::GetRandomFileName())
    try {
        $gitBin = Join-Path $WorkRoot 'git/usr/bin'
        $env:PATH = "$gitBin$([IO.Path]::PathSeparator)$env:PATH"
        $writer = (Join-Path $checkout 'frontends/qt/archive_windows.sh').Replace('\', '/')
        Invoke-Native (Join-Path $gitBin 'bash.exe') @('--noprofile', '--norc', $writer,
            $bundle.Replace('\', '/'), $pending.Replace('\', '/'))
        $hash = (Get-FileHash -LiteralPath $pending -Algorithm SHA256).Hash
        if ([IO.File]::Exists($destination)) {
            [IO.File]::Replace($pending, $destination, [Management.Automation.Language.NullString]::Value)
        } else { [IO.File]::Move($pending, $destination) }
    } finally { if ([IO.File]::Exists($pending)) { [IO.File]::Delete($pending) } }
    Write-Host "Created $destination (SHA-256 $hash)"
}

$verifyFiles = $Verify -or $Reference
$writeArchive = !$OutputDirectory -and (!$verifyFiles -or $CreateArchive)
if ($Reference) { $Reference = (Get-Item -LiteralPath $Reference -Force).FullName }
if ($OutputDirectory) {
    $OutputDirectory = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputDirectory)
    if (Test-Path -LiteralPath $OutputDirectory) { throw "Choose a new output directory. $OutputDirectory already exists." }
}
if ($CheckOnly -or $writeArchive) {
    foreach ($name in @('tar.exe','gzip.exe')) {
        if (![IO.File]::Exists("$($tools.git)\usr\bin\$name")) { throw "Packaging requires $name in $($tools.git)\usr\bin." }
    }
}
$gh = if ($verifyFiles -and !$Reference) {
    $application = Get-Command gh.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (!$application) { throw 'Install and authenticate GitHub CLI to download the CI artifact for -Verify.' }
    $application.Source
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
    Test-QtPaths $tools.qt
    if ($CheckOnly) { return }

    if ([IO.DriveInfo]::GetDrives().Name -contains 'B:\') { throw 'Drive B: must be free for the fixed build paths.' }
    $work = Join-Path ([IO.Path]::GetTempPath()) ('bitbox-build-' + [guid]::NewGuid())
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
    foreach ($dir in @('src','temp','config')) { [void][IO.Directory]::CreateDirectory("B:\$dir") }
    $env:TEMP = $env:TMP = 'B:\temp'
    $env:GIT_CONFIG_NOSYSTEM = '1'
    $env:GIT_CONFIG_GLOBAL = 'B:/config/.gitconfig'
    $env:GIT_TERMINAL_PROMPT = '0'
    $env:GOROOT = 'B:\go'
    $env:GOTOOLCHAIN = 'local'
    $env:GOENV = 'off'
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

    $git = 'B:\git\cmd\git.exe'
    $source = "https://github.com/$Repository.git"
    if (Test-Path -LiteralPath "$root\.git") {
        $source = $root
        if (!$Commit) { $Commit = Invoke-Native $git @('-C', $root, 'rev-parse', '--verify', 'HEAD') }
    } elseif (!$Commit) { throw 'Supply -Commit with the full Git SHA when running from a source ZIP.' }
    Write-Host "Building commit $Commit"
    Invoke-Native $git @('-C', 'B:\src', 'init', '--quiet')
    Invoke-Native $git @('-C', 'B:\src', 'config', 'core.autocrlf', 'false')
    Invoke-Native $git @('-C', 'B:\src', 'fetch', '--quiet', '--no-tags', '--depth=1', '--update-shallow', $source, $Commit)
    Invoke-Native $git @('-C', 'B:\src', 'checkout', '--quiet', '--detach', 'FETCH_HEAD')
    if ((Invoke-Native $git @('-C', 'B:\src', 'rev-parse', 'HEAD')) -ne $Commit) { throw 'Checkout SHA mismatch.' }
    foreach ($name in @('windows-archive.ps1','windows-toolchain.ps1','windows-toolchain.json')) {
        $path = "B:\src\scripts\$name"
        if (!(Test-Path -LiteralPath $path) -or
            ([IO.File]::ReadAllText($path) -replace "`r`n", "`n") -cne
            ([IO.File]::ReadAllText("$PSScriptRoot\$name") -replace "`r`n", "`n")) {
            throw "Use the build scripts and toolchain manifest from commit $Commit ($name differs)."
        }
    }
    Invoke-Native $git @('-C', 'B:\src', '-c', 'core.autocrlf=false', 'submodule', 'update', '--init', '--recursive')
    $env:SOURCE_DATE_EPOCH = Invoke-Native $git @('-C', 'B:\src', 'show', '-s', '--format=%ct', 'HEAD')
    Push-Location -LiteralPath 'B:\src'
    try { Invoke-Native 'B:\git\usr\bin\bash.exe' @('--noprofile', '--norc', 'scripts/windows-archive-build.sh') }
    finally { Pop-Location }

    $qt = 'B:\src\frontends\qt'
    $bundle = [IO.Directory]::CreateDirectory('B:\release').FullName
    [IO.Directory]::Move("$qt\build\windows", "$bundle\windows")
    Copy-Item -LiteralPath @('B:\src\APP_VERSION', "$qt\resources\win\icon.ico",
        "$qt\setup.nsi", "$qt\sign-windows-release.py") -Destination $bundle

    if ($verifyFiles -and !$Reference) {
        $credentials = @('GH_TOKEN','GITHUB_TOKEN','GH_CONFIG_DIR')
        try {
            foreach ($key in $credentials) { [Environment]::SetEnvironmentVariable($key, $original[$key]) }
            $Reference = Get-CIArchive $gh $Commit $work
        } finally {
            foreach ($key in $credentials) { [Environment]::SetEnvironmentVariable($key, $null) }
        }
    }
    $archiveDirectory = if ($writeArchive) { Join-Path $root 'frontends/qt/build' }
    Complete-WindowsBuild 'B:\' $Reference $OutputDirectory $archiveDirectory
} finally {
    foreach ($key in [Environment]::GetEnvironmentVariables().Keys) {
        if (!$original.ContainsKey($key)) { [Environment]::SetEnvironmentVariable($key, $null) }
    }
    foreach ($key in $original.Keys) { [Environment]::SetEnvironmentVariable($key, $original[$key]) }
    if ($mapped) { Invoke-Native $subst @('B:', '/D') }
    # cmd's rmdir removes directory junctions themselves. Never recurse through a tool installation.
    if ($work -and [IO.Directory]::Exists($work)) {
        foreach ($name in $junctionNames) {
            $junction = Join-Path $work $name
            if ([IO.Directory]::Exists($junction)) { [IO.Directory]::Delete($junction) }
        }
        Push-Location -LiteralPath (Split-Path $work -Parent)
        try { Invoke-Native $cmd @('/d', '/c', 'rmdir', '/s', '/q', (Split-Path $work -Leaf)) }
        finally { Pop-Location }
    }
}
