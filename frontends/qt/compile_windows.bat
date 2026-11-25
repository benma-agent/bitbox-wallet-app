:: Compiles the Qt app. Part of `make windows`, which also compiles/bundles the deps
:: Usage: compile_windows.bat BUILD_DIRECTORY (absolute Windows path)
:: Explicit exits report failures to the caller instead of continuing after errors.
if "%~1"=="" exit /b 1
:: BitBox.pro is beside this batch file (%~dp0 is its directory).
:: Use its absolute path because we later change into the build directory.
set "PROJECT=%~dp0BitBox.pro"
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
set "QMAKE_CONFIG=-qtconf B:/qt-build.conf -spec win32-msvc"

:: Unquote the first argument; /d changes the drive as well as the directory.
cd /d "%~1" || exit /b 1
"%QMAKE%" "%PROJECT%" %QMAKE_CONFIG% || exit /b 1
"%NMAKE%" || exit /b 1
