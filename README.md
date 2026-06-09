The **Radar Emulation Display System** (REDS) is a high-fidelity emulation of [ASDE-X](https://www.faa.gov/air_traffic/technology/asde-x) with FAA SWIM integration.

### Installation

If you do not have a SWIFT Portal account yet, register [here](https://portal.swim.faa.gov/). Once logged in navigate to `Subscriptions` and create a `New Subscription` with the following properties:

| Property | Value |
| --- | --- |
| **SWIM Product Type** | `STTDS` > `Surface Movement Event` |
| **Service Filters** | `Airport` > `ALL` & `Message Type` > `Position Reports` |
| **Subscription Name & Justification** | at user's discretion |

#### Requirements

* Go 1.25 or compatible
* JDK 21
* Maven
* C/C++ toolchain with cgo support
* `pkg-config` and GLFW
* OpenGL 3.3 capable graphics driver

#### macOS

For a local setup, install the following dependencies:

```bash
xcode-select --install
brew install go openjdk@21 maven pkg-config glfw
```

If Homebrew's JDK 21 is not already first on your `PATH`, configure it for the current shell:

```bash
export JAVA_HOME="$(brew --prefix openjdk@21)/libexec/openjdk.jdk/Contents/Home"
export PATH="$JAVA_HOME/bin:$PATH"
```

Fill in your SWIM credentials into the example environment file and run

```bash
cp .env.example .env
```

Finally, use

```bash
./build.sh
```

to run the app.
