:: Compiles the Qt app. Part of `make windows`, which also compiles/bundles the deps
:: Usage: compile_windows.bat BUILD_DIRECTORY (absolute Windows path)
:: Explicit exits report failures to the caller instead of continuing after errors.
if "%~1"=="" exit /b 1
:: BitBox.pro is beside this batch file (%~dp0 is its directory).
:: Use its absolute path because we later change into the build directory.
set "PROJECT=%~dp0BitBox.pro"
call "C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Auxiliary\Build\vcvars64.bat" || exit /b 1
:: Unquote the first argument; /d changes the drive as well as the directory.
cd /d "%~1" || exit /b 1
qmake "%PROJECT%" || exit /b 1
nmake || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\msvcp140.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\msvcp140_1.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\msvcp140_2.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\msvcp140_atomic_wait.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\msvcp140_codecvt_ids.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\vccorlib140.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\vcruntime140.dll" windows\ || exit /b 1
COPY "%VCToolsRedistDir%\x64\Microsoft.VC143.CRT\vcruntime140_1.dll" windows\ || exit /b 1
