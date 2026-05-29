// cmd/reds/main.go
//
// reds entrypoint. Opens a GLFW + Dear ImGui window, shows the startup menu
// (a port of the Qt launcher), and on Confirm will hand the Selection to the
// chosen scope. The scope dispatch is a stub for now — this milestone is just
// the menu and the platform layer underneath it.

package main

import (
	"fmt"
	"os"

	"github.com/juliusplatzer/reds/platform"

	"github.com/AllenDang/cimgui-go/imgui"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
)

const uiFontSize = 18 // logical px

func main() {
	// ImGui context must exist before the platform touches CurrentIO().
	imgui.CreateContext()
	defer imgui.DestroyContext()
	imgui.CurrentIO().SetIniFilename("") // no imgui.ini side file

	plat, err := platform.New(&platform.Config{
		Title:             "nascope",
		InitialWindowSize: [2]int{200, 350},
		MinWindowSize:     [2]int{200, 200},
		Resizable:         true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
		os.Exit(1)
	}
	defer plat.Dispose()

	loadFont()
	if err := initSVGIcons(); err != nil {
		fmt.Fprintf(os.Stderr, "reds: failed to initialize SVG icons: %v\n", err)
	}
	defer disposeSVGIcons()

	m := newMenu()
	if len(m.airports) == 0 {
		fmt.Fprintln(os.Stderr, "reds: no ASDE-X facilities found under resources/videomaps/asdex")
	}

	bg := colDialogBg
	for !plat.ShouldStop() {
		plat.ProcessEvents()
		plat.NewFrame()
		imgui.NewFrame()

		res := m.draw(plat.DisplaySize())

		imgui.Render()
		plat.Clear(bg.X, bg.Y, bg.Z)
		implogl3.RenderDrawData(imgui.CurrentDrawData())
		plat.PostRender()

		switch res {
		case menuConfirmed:
			onConfirm(m.selection)
			return
		case menuCancelled:
			return
		}
	}
}

// onConfirm is where the scope launch will go. For now it reports the choice;
// later it dispatches to the ASDE-X / STARS / ERAM scope in-process.
func onConfirm(sel Selection) {
	fmt.Fprintf(os.Stderr, "reds: selected %s / %s (scope launch not yet implemented)\n",
		sel.Mode, sel.Airport)
	// TODO: launch the selected scope here.
}
