The **Radar Emulation Display System** (REDS) is a high-fidelity emulation of [ASDE-X](https://www.faa.gov/air_traffic/technology/asde-x) with FAA SWIM integration.

### Installation

If you do not have a SWIFT Portal account yet, register [here](https://portal.swim.faa.gov/). Once logged in, navigate to `Subscriptions` and create a `New Subscription` with the following properties:

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

For a local setup, install the following dependencies:

```bash
xcode-select --install
brew install go openjdk@21 maven pkg-config glfw
```

If your default `java` or `mvn` uses a JDK **older** than 21, point the current shell at brew's JDK 21:

```bash
# check whether JDK 21 or newer is used
# java -version
export JAVA_HOME="$(brew --prefix openjdk@21)/libexec/openjdk.jdk/Contents/Home"
export PATH="$JAVA_HOME/bin:$PATH"
```

Fill in your SWIM credentials unquoted into the example environment file and run

```bash
cp .env.example .env
```

Finally, use

```bash
chmod +x build.sh
./build.sh
```

to run the app.

### Documentation
See [here](https://docs.virtualnas.net/crc/asdex/).