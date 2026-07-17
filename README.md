The **Radar Emulation Display System** (REDS) is a high-fidelity emulation of [ASDE-X](https://www.faa.gov/air_traffic/technology/asde-x).

REDS uses the public live REDS server by default. You do not need SWIM credentials, Java, Maven, or a local SMES process for the desktop app.

### Build the desktop app

#### macOS

Install the build tools:

```bash
xcode-select --install
brew install go pkg-config glfw
```

Build the app bundle:

```bash
./build.sh --app
```

This creates:

```text
build/REDS.app
```

Launch it:

```bash
open build/REDS.app
```

Optional: copy it to Applications:

```bash
rm -rf /Applications/REDS.app
cp -R build/REDS.app /Applications/REDS.app
```

#### Windows

Install the build tools from an elevated PowerShell:

```powershell
choco install golang msys2 -y --no-progress
```

Install the native toolchain:

```powershell
C:\msys64\usr\bin\pacman.exe -Syu --noconfirm
C:\msys64\usr\bin\pacman.exe -S --needed --noconfirm base-devel mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-pkgconf mingw-w64-ucrt-x86_64-glfw
```

Build the portable Windows app:

```powershell
.\build.bat --package
```

This creates:

```text
build\REDS-Windows\
build\REDS-Windows.zip
```

Run:

```powershell
.\build\REDS-Windows\REDS.exe
```

### Local SWIM development

Only set up `.env` if you want REDS to use your own SWIM/SMES connection instead of the public server:

```bash
cp .env.example .env
```

Then set:

```env
USE_PUBLIC_SERVER=false
```

### Documentation

See [Virtual NAS ASDE-X documentation](https://docs.virtualnas.net/crc/asdex/).
