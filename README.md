The **Radar Emulation Display System** (REDS) is a high-fidelity emulation of [ASDE-X](https://www.faa.gov/air_traffic/technology/asde-x) with FAA SWIM integration.

### Installation

If you do not have a SWIFT Portal account yet, you may register [here](https://portal.swim.faa.gov/) if you intend on not depending the public server. Once logged in, navigate to `Subscriptions` and create a `New Subscription` with the following properties:

| Property | Value |
| --- | --- |
| **SWIM Product Type** | `STTDS` > `Surface Movement Event` |
| **Service Filters** | `Airport` > `ALL` & `Message Type` > `Position Reports` |
| **Subscription Name & Justification** | at user's discretion |

#### Requirements

* Go 1.25 or compatible
* JDK 21 or newer
* Maven
* C/C++ toolchain with cgo support
* `pkg-config` and GLFW
* OpenGL 3.3 capable graphics driver

#### macOS

Install the following dependencies:

```bash
xcode-select --install
brew install go openjdk@21 maven pkg-config glfw
```

If your default `java` or `mvn` uses a JDK **older** than 21, point the current shell at brew's JDK 21 using:

```bash
export JAVA_HOME="$(brew --prefix openjdk@21)/libexec/openjdk.jdk/Contents/Home"
export PATH="$JAVA_HOME/bin:$PATH"
```

Fill in your SWIM credentials unquoted into the example `.env` file, set `USE_PUBLIC_SERVER=false` and run

```bash
cp .env.example .env
```
If you do **not** have SWIM credentials you can skip this step. 

Finally, use

```bash
chmod +x build.sh
./build.sh
```

to run the app.

#### Windows

Install the following dependencies from an elevated PowerShell:

```powershell
choco install golang temurin21 maven msys2 -y --no-progress
```

Then install the native cgo/GLFW toolchain:

```powershell
C:\msys64\usr\bin\pacman.exe -Syu --noconfirm
C:\msys64\usr\bin\pacman.exe -S --needed --noconfirm base-devel mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-pkgconf mingw-w64-ucrt-x86_64-glfw
```

Fill in your SWIM credentials unquoted into the example `.env` file, set `USE_PUBLIC_SERVER=false` and run

```powershell
Copy-Item .env.example .env
notepad .env
```
If you do **not** have SWIM credentials you can skip this step. 
Finally, build and run:

```powershell
.\build.bat
```

### Documentation
See [here](https://docs.virtualnas.net/crc/asdex/).
