The **Radar Emulation Display System** (REDS) is a high-fidelity emulation of [ASDE-X](https://www.faa.gov/air_traffic/technology/asde-x),
[STARS](https://www.faa.gov/air_traffic/technology/tamr) and [ERAM](https://www.faa.gov/air_traffic/technology/eram).

REDS uses the public live REDS server by default. You do not need SWIM credentials, Java, Maven, or a local SMES process for the desktop app.

### Build the desktop app

#### macOS

Install the build tools:

```bash
xcode-select --install
brew install go pkg-config glfw
```

Build the app bundle and ZIP using:

```bash
./build.sh --package
```

which creates

```text
build/REDS.app
build/REDS-<version>-macOS.zip
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
build\REDS-<version>-Windows.zip
```

Run:

```powershell
.\build\REDS-Windows\REDS.exe
```

### Local SWIM development

Only set up `.env` if you want REDS to use your own SWIM connection instead of the public server:

```bash
cp .env.example .env
```

Then set:

```env
USE_PUBLIC_SERVER=false
```

### Documentation

ASDE-X and ERAM are based on [CRC](https://docs.virtualnas.net/crc/) and STARS is based on the TI 6191.409, Rev. 30 and [vice](https://pharr.org/vice/).
