@echo off
:: Compiles the Qt app. Part of the Windows build, which also compiles/bundles the deps.
setlocal
if defined BITBOX_VS_ROOT goto release

set "VSWHERE=%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe"
for /f "usebackq delims=" %%I in (`""%VSWHERE%" -products * -version "[17.0,18.0)" -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -latest -property installationPath"`) do set "VSROOT=%%I"
if not defined VSROOT (
    echo Visual Studio 2022 C++ x64 build tools are required. 1>&2
    exit /b 1
)
call "%VSROOT%\VC\Auxiliary\Build\vcvars64.bat" || exit /b 1
set "QMAKE=qmake.exe"
set "NMAKE=nmake.exe"
set "QMAKE_CONFIG="
set "CRT=%VCToolsRedistDir%\x64\Microsoft.VC143.CRT"
goto compile

:release
call "%BITBOX_VS_ROOT%\VC\Auxiliary\Build\vcvarsall.bat" amd64 %BITBOX_SDK_VERSION% -vcvars_ver=%BITBOX_VC_VERSION% || exit /b 1
if /i not "%VCToolsVersion%"=="%BITBOX_VC_VERSION%" exit /b 1
if /i not "%WindowsSDKVersion%"=="%BITBOX_SDK_VERSION%\" exit /b 1
:: vcvarsall also adds unversioned tools and install paths. qmake and its generated
:: makefiles must resolve cl, link, rc, headers and libraries from the verified kits.
set "PATH=%BITBOX_COMPILE_PATH%"
set "INCLUDE=%BITBOX_INCLUDE%"
set "LIB=%BITBOX_LIB%"
set "LIBPATH="
set "VCINSTALLDIR=%BITBOX_VS_ROOT%\VC\"
set "VCToolsInstallDir=%VCINSTALLDIR%Tools\MSVC\%BITBOX_VC_VERSION%\"
set "WindowsSdkDir=B:\kits\"
set "UniversalCRTSdkDir=%WindowsSdkDir%"
set "WindowsSdkVerBinPath=%WindowsSdkDir%bin\%BITBOX_SDK_VERSION%\"
set "QMAKE=%BITBOX_QT_BIN%\qmake.exe"
set "NMAKE=%BITBOX_VC_BIN%\nmake.exe"
set "CRT=%BITBOX_CRT%"
set "QMAKE_CONFIG=-qtconf B:/qt-build.conf -spec win32-msvc CONFIG+=bitbox_release_archive"

:compile
cd build || exit /b 1
"%QMAKE%" ..\BitBox.pro %QMAKE_CONFIG% || exit /b 1
"%NMAKE%" || exit /b 1
for %%F in (msvcp140.dll msvcp140_1.dll msvcp140_2.dll msvcp140_atomic_wait.dll msvcp140_codecvt_ids.dll vccorlib140.dll vcruntime140.dll vcruntime140_1.dll) do (
    copy /y "%CRT%\%%F" windows\ >nul || exit /b 1
)
