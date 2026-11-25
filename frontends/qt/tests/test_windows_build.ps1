# SPDX-License-Identifier: Apache-2.0
$ErrorActionPreference = 'Stop'
# Load the preflight functions without provisioning or running the Windows toolchain.
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile("$PSScriptRoot/../windows-archive.ps1", [ref]$null, [ref]$errors)
if ($errors) { throw ($errors | Out-String) }
foreach ($function in $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $false)) {
    . ([scriptblock]::Create($function.Extent.Text))
}
function Expect-Failure([scriptblock] $Action, [string] $Message) {
    try { & $Action } catch {
        if ($_.Exception.Message -notlike $Message) { throw }
        return
    }
    throw "Expected failure: $Message"
}
$work = Join-Path ([IO.Path]::GetTempPath()) ('bitbox build test ' + [guid]::NewGuid())
[void][IO.Directory]::CreateDirectory($work)
try {
    # Cleaning tool junctions preserves both installed tools and the previous build's output.
    $installed = [IO.Directory]::CreateDirectory("$work/installed-tool").FullName
    $build = [IO.Directory]::CreateDirectory("$work/build").FullName
    [IO.File]::WriteAllText("$installed/keep.txt", 'installed tool')
    [IO.File]::WriteAllText("$build/BitBox.exe", 'build output')
    $junctionNames = @('qt')
    $linkType = if ($env:OS -eq 'Windows_NT') { 'Junction' } else { 'SymbolicLink' }
    [void](New-Item -ItemType $linkType -Path "$build/qt" -Target $installed)
    Remove-ToolJunctions $build
    if (Test-Path "$build/qt") { throw 'Tool junction was not removed.' }
    if (!(Test-Path "$build/BitBox.exe")) { throw 'Build output was removed with the tool junction.' }
    Remove-Item -LiteralPath $build -Recurse -Force
    if ([IO.File]::ReadAllText("$installed/keep.txt") -ne 'installed tool') { throw 'Installed tool was changed.' }

    Invoke-Native git @('-C', $work, 'init', '--quiet')
    [IO.File]::WriteAllText("$work/.gitattributes", "* text=auto eol=lf`n")
    [IO.File]::WriteAllText("$work/source.txt", "original`n")
    Invoke-Native git @('-C', $work, 'add', '.gitattributes', 'source.txt')

    # Valid local edits are accepted and remain untouched by preflight.
    [IO.File]::WriteAllText("$work/source.txt", "edited`n")
    Test-BuildCheckout git $work
    if ([IO.File]::ReadAllText("$work/source.txt") -cne "edited`n") { throw 'Local edit changed.' }

    # Git can consider CRLF conversion clean; preflight still rejects the changed input bytes.
    [IO.File]::WriteAllText("$work/source.txt", "original`r`n")
    Expect-Failure { Test-BuildCheckout git $work } '*Unexpected line endings*source.txt*'
    [IO.File]::WriteAllText("$work/source.txt", "original`n")

    # A missing submodule reports setup instructions without populating or changing the checkout.
    [IO.File]::WriteAllText("$work/.gitmodules", "[submodule `"module`"]`npath = module`nurl = unused`n")
    Invoke-Native git @('-C', $work, 'update-index', '--add', '--cacheinfo', '160000,0000000000000000000000000000000000000001,module')
    Expect-Failure { Test-BuildCheckout git $work } '*Missing submodule*submodule update --init --recursive*'
    if (Test-Path "$work/module") { throw 'Preflight created a submodule.' }

    # Initialized submodules accept both LF and CRLF source files.
    [void][IO.Directory]::CreateDirectory("$work/module")
    Invoke-Native git @('-C', "$work/module", 'init', '--quiet')
    [IO.File]::WriteAllText("$work/module/source.txt", "original`n")
    Invoke-Native git @('-C', "$work/module", 'add', 'source.txt')
    Test-BuildCheckout git $work
    [IO.File]::WriteAllText("$work/module/source.txt", "original`r`n")
    Test-BuildCheckout git $work

    # Deployment accepts the recorded runtime set and rejects missing or additional binaries.
    $pins = [pscustomobject]@{
        qt = [pscustomobject]@{ runtime = @('Qt6Core.dll') }
        redist = [pscustomobject]@{ files = [pscustomobject]@{ 'vcruntime140.dll' = '' } }
    }
    $tools = @{ qt = 'fixture Qt' }
    $payload = [IO.Directory]::CreateDirectory("$work/payload").FullName
    foreach ($file in @('BitBox.exe', 'libserver.dll', 'breez_sdk_spark_bindings.dll', 'Qt6Core.dll', 'vcruntime140.dll')) {
        [IO.File]::WriteAllText("$payload/$file", 'fixture')
    }
    Test-DeployedBinaries $payload
    [IO.File]::WriteAllText("$payload/extra.dll", 'fixture')
    Expect-Failure { Test-DeployedBinaries $payload } '*Qt deployment differs*'
    Remove-Item "$payload/extra.dll", "$payload/Qt6Core.dll"
    Expect-Failure { Test-DeployedBinaries $payload } '*Qt deployment differs*'
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force
}
