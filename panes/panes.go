package panes

import "github.com/juliusplatzer/reds/renderer"

// Pane is a drawable, frame-updated display surface. ASDEXPane will implement
// this interface; STARS/ERAM panes can use the same shape later.
type Pane interface {
	Draw(ctx *Context, zcb *renderer.ZCmdBuffer)
}
