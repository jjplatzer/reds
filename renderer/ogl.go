package renderer

import (
	"fmt"
	"image"
	"image/draw"
	"strings"
	"unsafe"

	"github.com/go-gl/gl/v3.3-core/gl"
)

type shaderProgram struct {
	id uint32
}

func (p shaderProgram) use() { gl.UseProgram(p.id) }

func (p shaderProgram) dispose() {
	if p.id != 0 {
		gl.DeleteProgram(p.id)
	}
}

func (p shaderProgram) uniform(name string) int32 {
	return gl.GetUniformLocation(p.id, gl.Str(name+"\x00"))
}

func (p shaderProgram) setMat4(name string, m Mat4) {
	gl.UniformMatrix4fv(p.uniform(name), 1, false, &m[0])
}

func (p shaderProgram) setRGBA(name string, c RGBA) {
	gl.Uniform4f(p.uniform(name), c.R, c.G, c.B, c.A)
}

func (p shaderProgram) setFloat(name string, f float32) {
	gl.Uniform1f(p.uniform(name), f)
}

func (p shaderProgram) setInt(name string, i int32) {
	gl.Uniform1i(p.uniform(name), i)
}

// OpenGLRenderer executes CmdBuffers using an OpenGL 3.3 core backend.
type OpenGLRenderer struct {
	solidShader    shaderProgram
	hatchShader    shaderProgram
	coloredShader  shaderProgram
	texturedShader shaderProgram
	fontShader     shaderProgram

	textures      map[TextureID]uint32
	nextTextureID TextureID

	vao uint32
	vbo uint32
	ebo uint32

	projection Mat4
	color      RGBA
	lineWidth  float32
	ready      bool
}

var _ Renderer = (*OpenGLRenderer)(nil)

func NewOpenGLRenderer() *OpenGLRenderer {
	return &OpenGLRenderer{
		textures:      make(map[TextureID]uint32),
		nextTextureID: 1,
		projection:    Identity(),
		color:         RGBA{A: 1},
		lineWidth:     1,
	}
}

func (r *OpenGLRenderer) Init() error {
	if err := gl.Init(); err != nil {
		return fmt.Errorf("initialize OpenGL: %w", err)
	}

	var err error
	if r.solidShader, err = compileProgram("solid", "solid.vert", "solid.frag"); err != nil {
		return err
	}
	if r.hatchShader, err = compileProgram("hatch", "solid.vert", "hatch.frag"); err != nil {
		r.Dispose()
		return err
	}
	if r.coloredShader, err = compileProgram("colored", "colored.vert", "colored.frag"); err != nil {
		r.Dispose()
		return err
	}
	if r.texturedShader, err = compileProgram("textured", "textured.vert", "textured.frag"); err != nil {
		r.Dispose()
		return err
	}
	if r.fontShader, err = compileProgram("font", "font.vert", "font.frag"); err != nil {
		r.Dispose()
		return err
	}

	gl.GenVertexArrays(1, &r.vao)
	gl.GenBuffers(1, &r.vbo)
	gl.GenBuffers(1, &r.ebo)

	r.resetState()
	r.ready = true
	return nil
}

func (r *OpenGLRenderer) Dispose() {
	for id, tex := range r.textures {
		gl.DeleteTextures(1, &tex)
		delete(r.textures, id)
	}
	if r.ebo != 0 {
		gl.DeleteBuffers(1, &r.ebo)
		r.ebo = 0
	}
	if r.vbo != 0 {
		gl.DeleteBuffers(1, &r.vbo)
		r.vbo = 0
	}
	if r.vao != 0 {
		gl.DeleteVertexArrays(1, &r.vao)
		r.vao = 0
	}
	r.solidShader.dispose()
	r.hatchShader.dispose()
	r.coloredShader.dispose()
	r.texturedShader.dispose()
	r.fontShader.dispose()
	r.ready = false
}

func (r *OpenGLRenderer) resetState() {
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.STENCIL_TEST)
	gl.Disable(gl.MULTISAMPLE)
	gl.Disable(gl.CULL_FACE)
	gl.Disable(gl.SCISSOR_TEST)
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, 0)
	gl.UseProgram(0)
	r.projection = Identity()
	r.color = RGBA{A: 1}
	r.lineWidth = 1
	gl.LineWidth(1)
}

func (r *OpenGLRenderer) CreateTextureFromImage(img image.Image, magNearest bool) TextureID {
	if img == nil {
		return 0
	}
	id, tex := r.newTexture()
	r.textures[id] = tex
	r.UpdateTextureFromImage(id, img, magNearest)
	return id
}

func (r *OpenGLRenderer) CreateTextureR8(width, height int, pixels []byte, magNearest bool) TextureID {
	if width <= 0 || height <= 0 || len(pixels) < width*height {
		return 0
	}
	id, tex := r.newTexture()
	r.textures[id] = tex

	gl.BindTexture(gl.TEXTURE_2D, tex)
	setTextureParams(magNearest)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage2D(gl.TEXTURE_2D, 0, int32(gl.R8), int32(width), int32(height), 0, gl.RED, gl.UNSIGNED_BYTE, gl.Ptr(pixels))
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	return id
}

func (r *OpenGLRenderer) UpdateTextureFromImage(id TextureID, img image.Image, magNearest bool) {
	tex := r.textures[id]
	if tex == 0 || img == nil {
		return
	}
	rgba := imageToRGBA(img)
	b := rgba.Bounds()

	gl.BindTexture(gl.TEXTURE_2D, tex)
	setTextureParams(magNearest)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage2D(gl.TEXTURE_2D, 0, int32(gl.RGBA8), int32(b.Dx()), int32(b.Dy()), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba.Pix))
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)
	gl.BindTexture(gl.TEXTURE_2D, 0)
}

func (r *OpenGLRenderer) DestroyTexture(id TextureID) {
	tex := r.textures[id]
	if tex == 0 {
		return
	}
	gl.DeleteTextures(1, &tex)
	delete(r.textures, id)
}

func (r *OpenGLRenderer) RenderCmdBuffer(cb *CmdBuffer) RendererStats {
	return r.renderCmdBuffer(cb, true)
}

func (r *OpenGLRenderer) RenderZCmdBuffer(zcb *ZCmdBuffer) RendererStats {
	var stats RendererStats
	if !r.ready || zcb == nil || zcb.Empty() {
		return stats
	}

	reset := true
	for _, z := range zcb.keys {
		cb := zcb.buffers[z]
		if cb == nil || cb.Empty() {
			continue
		}
		stats.Add(r.renderCmdBuffer(cb, reset))
		reset = false
	}
	return stats
}

func (r *OpenGLRenderer) renderCmdBuffer(cb *CmdBuffer, reset bool) RendererStats {
	var stats RendererStats
	if !r.ready || cb == nil || cb.Empty() {
		return stats
	}
	stats.Buffers++
	if reset {
		r.resetState()
	}

	for i := range cb.commands {
		cmd := &cb.commands[i]
		switch cmd.type_ {
		case cmdResetState:
			r.resetState()
		case cmdLoadProjectionMatrix:
			r.projection = cmd.matrix
		case cmdClear:
			gl.ClearColor(cmd.color.R, cmd.color.G, cmd.color.B, cmd.color.A)
			gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		case cmdViewport:
			gl.Viewport(int32(cmd.x), int32(cmd.y), int32(cmd.w), int32(cmd.h))
		case cmdScissor:
			gl.Enable(gl.SCISSOR_TEST)
			gl.Scissor(int32(cmd.x), int32(cmd.y), int32(cmd.w), int32(cmd.h))
		case cmdDisableScissor:
			gl.Disable(gl.SCISSOR_TEST)
		case cmdBlend:
			gl.Enable(gl.BLEND)
			gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		case cmdDisableBlend:
			gl.Disable(gl.BLEND)
		case cmdSetColor:
			r.color = cmd.color
		case cmdLineWidth:
			r.lineWidth = cmd.lineWidth
			gl.LineWidth(cmd.lineWidth)
		case cmdDrawLines:
			stats.Add(r.drawPoints(cb.points[cmd.vertexOffset:cmd.vertexOffset+cmd.vertexCount], cb.indices[cmd.indexOffset:cmd.indexOffset+cmd.indexCount], false, DrawSolid, 0))
		case cmdDrawTriangles:
			stats.Add(r.drawPoints(cb.points[cmd.vertexOffset:cmd.vertexOffset+cmd.vertexCount], cb.indices[cmd.indexOffset:cmd.indexOffset+cmd.indexCount], true, cmd.drawMode, cmd.hatchOffset))
		case cmdDrawColoredLines:
			stats.Add(r.drawColored(cb.coloredPoints[cmd.vertexOffset:cmd.vertexOffset+cmd.vertexCount], cb.indices[cmd.indexOffset:cmd.indexOffset+cmd.indexCount], false))
		case cmdDrawColoredTriangles:
			stats.Add(r.drawColored(cb.coloredPoints[cmd.vertexOffset:cmd.vertexOffset+cmd.vertexCount], cb.indices[cmd.indexOffset:cmd.indexOffset+cmd.indexCount], true))
		case cmdDrawTexturedTriangles:
			stats.Add(r.drawTextured(cmd.textureID, cb.texturedPoints[cmd.vertexOffset:cmd.vertexOffset+cmd.vertexCount], cb.indices[cmd.indexOffset:cmd.indexOffset+cmd.indexCount]))
		case cmdDrawFontTriangles:
			stats.Add(r.drawFont(cmd.textureID, cb.fontPoints[cmd.vertexOffset:cmd.vertexOffset+cmd.vertexCount], cb.indices[cmd.indexOffset:cmd.indexOffset+cmd.indexCount]))
		case cmdCall:
			stats.Add(r.renderCmdBuffer(cmd.called, false))
		}
	}

	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, 0)
	gl.BindVertexArray(0)
	gl.UseProgram(0)
	return stats
}

func (r *OpenGLRenderer) ReadPixels(x, y, w, h int, outRGBA []byte) {
	if len(outRGBA) < w*h*4 || w <= 0 || h <= 0 {
		return
	}
	gl.ReadPixels(int32(x), int32(y), int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(outRGBA))
}

func (r *OpenGLRenderer) drawPoints(vertices []PointVertex, indices []uint32, triangles bool, mode DrawMode, hatchOffset float32) RendererStats {
	var stats RendererStats
	if len(vertices) == 0 || len(indices) == 0 {
		return stats
	}
	shader := r.solidShader
	if triangles && mode == DrawHatched {
		shader = r.hatchShader
	}
	shader.use()
	shader.setMat4("u_projection", r.projection)
	shader.setRGBA("u_color", r.color)
	if triangles && mode == DrawHatched {
		shader.setFloat("u_offset", hatchOffset)
	}

	r.bindIndexedBuffers(len(vertices)*int(unsafe.Sizeof(PointVertex{})), unsafe.Pointer(&vertices[0]), indices)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, int32(unsafe.Sizeof(PointVertex{})), 0)

	if triangles {
		gl.DrawElementsWithOffset(gl.TRIANGLES, int32(len(indices)), gl.UNSIGNED_INT, 0)
		stats.Triangles += len(indices) / 3
	} else {
		gl.LineWidth(r.lineWidth)
		gl.DrawElementsWithOffset(gl.LINES, int32(len(indices)), gl.UNSIGNED_INT, 0)
		stats.Lines += len(indices) / 2
	}
	gl.DisableVertexAttribArray(0)
	stats.DrawCalls++
	stats.BufferBytes += len(vertices)*int(unsafe.Sizeof(PointVertex{})) + len(indices)*4
	return stats
}

func (r *OpenGLRenderer) drawColored(vertices []ColoredVertex, indices []uint32, triangles bool) RendererStats {
	var stats RendererStats
	if len(vertices) == 0 || len(indices) == 0 {
		return stats
	}
	r.coloredShader.use()
	r.coloredShader.setMat4("u_projection", r.projection)

	r.bindIndexedBuffers(len(vertices)*int(unsafe.Sizeof(ColoredVertex{})), unsafe.Pointer(&vertices[0]), indices)
	stride := int32(unsafe.Sizeof(ColoredVertex{}))
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(ColoredVertex{}.X)))
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(ColoredVertex{}.Color)))

	if triangles {
		gl.DrawElementsWithOffset(gl.TRIANGLES, int32(len(indices)), gl.UNSIGNED_INT, 0)
		stats.Triangles += len(indices) / 3
	} else {
		gl.LineWidth(r.lineWidth)
		gl.DrawElementsWithOffset(gl.LINES, int32(len(indices)), gl.UNSIGNED_INT, 0)
		stats.Lines += len(indices) / 2
	}
	gl.DisableVertexAttribArray(0)
	gl.DisableVertexAttribArray(1)
	stats.DrawCalls++
	stats.BufferBytes += len(vertices)*int(unsafe.Sizeof(ColoredVertex{})) + len(indices)*4
	return stats
}

func (r *OpenGLRenderer) drawTextured(textureID TextureID, vertices []TexturedVertex, indices []uint32) RendererStats {
	var stats RendererStats
	if len(vertices) == 0 || len(indices) == 0 {
		return stats
	}
	r.texturedShader.use()
	r.texturedShader.setMat4("u_projection", r.projection)
	r.texturedShader.setInt("u_texture", 0)
	r.texturedShader.setRGBA("u_color", r.color)
	r.bindTexture(textureID)

	r.bindIndexedBuffers(len(vertices)*int(unsafe.Sizeof(TexturedVertex{})), unsafe.Pointer(&vertices[0]), indices)
	stride := int32(unsafe.Sizeof(TexturedVertex{}))
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(TexturedVertex{}.X)))
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(TexturedVertex{}.U)))
	gl.DrawElementsWithOffset(gl.TRIANGLES, int32(len(indices)), gl.UNSIGNED_INT, 0)
	gl.DisableVertexAttribArray(0)
	gl.DisableVertexAttribArray(1)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	stats.DrawCalls++
	stats.Triangles += len(indices) / 3
	stats.BufferBytes += len(vertices)*int(unsafe.Sizeof(TexturedVertex{})) + len(indices)*4
	return stats
}

func (r *OpenGLRenderer) drawFont(textureID TextureID, vertices []FontVertex, indices []uint32) RendererStats {
	var stats RendererStats
	if len(vertices) == 0 || len(indices) == 0 {
		return stats
	}
	r.fontShader.use()
	r.fontShader.setMat4("u_projection", r.projection)
	r.fontShader.setInt("u_fontAtlas", 0)
	r.bindTexture(textureID)

	r.bindIndexedBuffers(len(vertices)*int(unsafe.Sizeof(FontVertex{})), unsafe.Pointer(&vertices[0]), indices)
	stride := int32(unsafe.Sizeof(FontVertex{}))
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(FontVertex{}.X)))
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(FontVertex{}.U)))
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 4, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(FontVertex{}.Color)))
	gl.EnableVertexAttribArray(3)
	gl.VertexAttribPointerWithOffset(3, 4, gl.FLOAT, false, stride, uintptr(unsafe.Offsetof(FontVertex{}.Background)))
	gl.DrawElementsWithOffset(gl.TRIANGLES, int32(len(indices)), gl.UNSIGNED_INT, 0)
	gl.DisableVertexAttribArray(0)
	gl.DisableVertexAttribArray(1)
	gl.DisableVertexAttribArray(2)
	gl.DisableVertexAttribArray(3)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	stats.DrawCalls++
	stats.Triangles += len(indices) / 3
	stats.BufferBytes += len(vertices)*int(unsafe.Sizeof(FontVertex{})) + len(indices)*4
	return stats
}

func (r *OpenGLRenderer) bindIndexedBuffers(vertexBytes int, vertexPtr unsafe.Pointer, indices []uint32) {
	gl.BindVertexArray(r.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, vertexBytes, vertexPtr, gl.DYNAMIC_DRAW)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(indices)*int(unsafe.Sizeof(uint32(0))), unsafe.Pointer(&indices[0]), gl.DYNAMIC_DRAW)
}

func (r *OpenGLRenderer) newTexture() (TextureID, uint32) {
	var tex uint32
	gl.GenTextures(1, &tex)
	id := r.nextTextureID
	r.nextTextureID++
	return id, tex
}

func (r *OpenGLRenderer) bindTexture(id TextureID) {
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, r.textures[id])
}

func setTextureParams(magNearest bool) {
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, int32(gl.NEAREST))
	mag := int32(gl.LINEAR)
	if magNearest {
		mag = int32(gl.NEAREST)
	}
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, mag)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, int32(gl.CLAMP_TO_EDGE))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, int32(gl.CLAMP_TO_EDGE))
}

func imageToRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok && rgba.Rect.Min.X == 0 && rgba.Rect.Min.Y == 0 && rgba.Stride == rgba.Rect.Dx()*4 {
		return rgba
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba
}

func compileProgram(label, vertexName, fragmentName string) (shaderProgram, error) {
	vertexSource, err := shaderSource(vertexName)
	if err != nil {
		return shaderProgram{}, err
	}
	fragmentSource, err := shaderSource(fragmentName)
	if err != nil {
		return shaderProgram{}, err
	}

	vertex, err := compileShader(label+" vertex", vertexSource, gl.VERTEX_SHADER)
	if err != nil {
		return shaderProgram{}, err
	}
	defer gl.DeleteShader(vertex)

	fragment, err := compileShader(label+" fragment", fragmentSource, gl.FRAGMENT_SHADER)
	if err != nil {
		return shaderProgram{}, err
	}
	defer gl.DeleteShader(fragment)

	program := gl.CreateProgram()
	gl.AttachShader(program, vertex)
	gl.AttachShader(program, fragment)
	gl.LinkProgram(program)

	var status int32
	gl.GetProgramiv(program, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		log := programInfoLog(program)
		gl.DeleteProgram(program)
		return shaderProgram{}, fmt.Errorf("link %s shader program: %s", label, log)
	}

	return shaderProgram{id: program}, nil
}

func shaderSource(name string) (string, error) {
	data, err := shaderFS.ReadFile("shaders/" + name)
	if err != nil {
		return "", fmt.Errorf("read shader %s: %w", name, err)
	}
	return string(data), nil
}

func compileShader(label, source string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)
	csource, free := gl.Strs(source + "\x00")
	gl.ShaderSource(shader, 1, csource, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		log := shaderInfoLog(shader)
		gl.DeleteShader(shader)
		return 0, fmt.Errorf("compile %s shader: %s", label, log)
	}
	return shader, nil
}

func shaderInfoLog(shader uint32) string {
	var length int32
	gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &length)
	if length <= 1 {
		return "unknown error"
	}
	buf := strings.Repeat("\x00", int(length+1))
	gl.GetShaderInfoLog(shader, length, nil, gl.Str(buf))
	return strings.TrimRight(buf, "\x00")
}

func programInfoLog(program uint32) string {
	var length int32
	gl.GetProgramiv(program, gl.INFO_LOG_LENGTH, &length)
	if length <= 1 {
		return "unknown error"
	}
	buf := strings.Repeat("\x00", int(length+1))
	gl.GetProgramInfoLog(program, length, nil, gl.Str(buf))
	return strings.TrimRight(buf, "\x00")
}
