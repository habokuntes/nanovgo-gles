package nanovgo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/goxjs/gl"
	"log"
	"math"
	"strings"
)

const (
	glnvg_LOC_VIEWSIZE = iota
	glnvg_LOC_TEX
	glnvg_LOC_FRAG
	glnvg_MAX_LOCS
)

func NewContext(flags CreateFlags) (*Context, error) {
	params := &glParams{
		isEdgeAntiAlias: (flags & ANTIALIAS) != 0,
		context: &glContext{
			flags: flags,
		},
	}
	return createInternal(params)
}

type glShader struct {
	program   gl.Program
	fragment  gl.Shader
	vertex    gl.Shader
	locations [glnvg_MAX_LOCS]gl.Uniform
}

func (s *glShader) createShader(name, header, opts, vShader, fShader string) error {
	program := gl.CreateProgram()

	vertexShader := gl.CreateShader(gl.VERTEX_SHADER)
	gl.ShaderSource(vertexShader, strings.Join([]string{header, opts, vShader}, "\n"))
	gl.CompileShader(vertexShader)
	status := gl.Enum(gl.GetShaderi(vertexShader, gl.COMPILE_STATUS))
	if status != gl.TRUE {
		return dumpShaderError(vertexShader, name, "vert")
	}

	fragmentShader := gl.CreateShader(gl.FRAGMENT_SHADER)
	gl.ShaderSource(fragmentShader, strings.Join([]string{header, opts, fShader}, "\n"))
	gl.CompileShader(fragmentShader)
	status = gl.Enum(gl.GetShaderi(fragmentShader, gl.COMPILE_STATUS))
	if status != gl.TRUE {
		return dumpShaderError(fragmentShader, name, "vert")
	}

	gl.AttachShader(program, vertexShader)
	gl.AttachShader(program, fragmentShader)

	gl.BindAttribLocation(program, gl.Attrib{0}, "vertex")
	gl.BindAttribLocation(program, gl.Attrib{1}, "tcoord")

	gl.LinkProgram(program)
	status = gl.Enum(gl.GetProgrami(program, gl.LINK_STATUS))
	if status != gl.TRUE {
		return dumpProgramError(program, name)
	}

	s.program = program
	s.vertex = vertexShader
	s.fragment = fragmentShader

	return nil
}

func (s *glShader) deleteShader() {
	if s.program.Valid() {
		gl.DeleteProgram(s.program)
	}
	if s.vertex.Valid() {
		gl.DeleteShader(s.vertex)
	}
	if s.fragment.Valid() {
		gl.DeleteShader(s.fragment)
	}
}

func (s *glShader) getUniforms() {
	s.locations[glnvg_LOC_VIEWSIZE] = gl.GetUniformLocation(s.program, "viewSize")
	s.locations[glnvg_LOC_TEX] = gl.GetUniformLocation(s.program, "tex")
	s.locations[glnvg_LOC_FRAG] = gl.GetUniformLocation(s.program, "frag")
}

const (
	glnvg_NANOVG_GL_UNIFORMARRAY_SIZE = 11
)

const (
	glnvg_IMAGE_NODELETE ImageFlags = 1 << 16
)

type glContext struct {
	shader       glShader
	view         [2]float32
	textures     []*glTexture
	textureId    int
	vertexBuffer gl.Buffer
	flags        CreateFlags
	calls        []glCall
	paths        []glPath
	vertexes     []byte
	uniforms     []glFragUniforms

	stencilMask     uint32
	stencilFunc     gl.Enum
	stencilFuncRef  int
	stencilFuncMask uint32
}

func (c *glContext) findTexture(id int) *glTexture {
	for _, texture := range c.textures {
		if texture.id == id {
			return texture
		}
	}
	return nil
}

func (c *glContext) deleteTexture(id int) error {
	tex := c.findTexture(id)
	if tex != nil && (tex.flags&glnvg_IMAGE_NODELETE) == 0 {
		gl.DeleteTexture(tex.tex)
		tex.id = 0
		return nil
	}
	return errors.New("can't find texture")
}

func (c *glContext) bindTexture(tex *gl.Texture) {
	if tex == nil {
		gl.BindTexture(gl.TEXTURE_2D, gl.Texture{})
	} else {
		gl.BindTexture(gl.TEXTURE_2D, *tex)
	}
}

func (c *glContext) setStencilMask(mask uint32) {
	if c.stencilMask != mask {
		c.stencilMask = mask
		gl.StencilMask(mask)
	}
}

func (c *glContext) setStencilFunc(fun gl.Enum, ref int, mask uint32) {
	if c.stencilFunc != fun || c.stencilFuncRef != ref || c.stencilFuncMask != mask {
		c.stencilFunc = fun
		c.stencilFuncRef = ref
		c.stencilMask = mask
		gl.StencilFunc(fun, ref, mask)
	}
}

func (c *glContext) checkError(str string) {
	if c.flags&DEBUG == 0 {
		return
	}
	err := gl.GetError()
	if err != gl.NO_ERROR {
		log.Printf("Error %08x after %s\n", err, str)
	}
}

func (c *glContext) appendVertex(vertexes []nvgVertex) {
	offset := len(c.vertexes)
	c.vertexes = append(c.vertexes, make([]byte, 16*len(vertexes))...)
	subVertexes := c.vertexes[offset:]
	for i := range vertexes {
		vertex := &(vertexes[i])
		binary.LittleEndian.PutUint32(subVertexes[i*4*4:], math.Float32bits(vertex.x))
		binary.LittleEndian.PutUint32(subVertexes[i*4*4+4:], math.Float32bits(vertex.y))
		binary.LittleEndian.PutUint32(subVertexes[i*4*4+8:], math.Float32bits(vertex.u))
		binary.LittleEndian.PutUint32(subVertexes[i*4*4+12:], math.Float32bits(vertex.v))
	}
}

func (c *glContext) allocFragUniforms(n int) ([]glFragUniforms, int) {
	ret := len(c.uniforms)
	c.uniforms = append(c.uniforms, make([]glFragUniforms, n)...)
	return c.uniforms[ret:], ret
}

func (c *glContext) allocPath(n int) ([]glPath, int) {
	ret := len(c.paths)
	c.paths = append(c.paths, make([]glPath, n)...)
	return c.paths[ret:], ret
}

func (c *glContext) allocTexture() *glTexture {
	var tex *glTexture
	for _, texture := range c.textures {
		if texture.id == 0 {
			tex = texture
			break
		}
	}
	if tex == nil {
		tex = &glTexture{}
		c.textures = append(c.textures, tex)
	}
	c.textureId += 1
	tex.id = c.textureId
	return tex
}

func (c *glContext) convertPaint(frag *glFragUniforms, paint *Paint, scissor *nvgScissor, width, fringe, strokeThr float32) error {
	frag.setInnerColor(paint.innerColor.PreMultiply())
	frag.setOuterColor(paint.outerColor.PreMultiply())

	if scissor.extent[0] < -0.5 || scissor.extent[1] < -0.5 {
		frag.setScissorMat([]float32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		frag.setScissorExt(1.0, 1.0)
		frag.setScissorScale(1.0, 1.0)
	} else {
		xform := scissor.xform
		frag.setScissorMat(xform.Inverse().ToMat3x4())
		frag.setScissorExt(scissor.extent[0], scissor.extent[1])
		scaleX := sqrtF(xform[0]*xform[0]+xform[2]*xform[2]) / fringe
		scaleY := sqrtF(xform[1]*xform[1]+xform[3]*xform[3]) / fringe
		frag.setScissorScale(scaleX, scaleY)
	}
	frag.setExtent(paint.extent)
	frag.setStrokeMult((width*0.5 + fringe*0.5) / fringe)
	frag.setStrokeThr(strokeThr)

	if paint.image != 0 {
		tex := c.findTexture(paint.image)
		if tex == nil {
			return errors.New("invalid texture in GLParams.convertPaint")
		}
		if tex.flags&IMAGE_FLIPY != 0 {
			flipped := TransformMatrixScale(1.0, -1.0)
			flipped.Multiply(paint.xform)
			frag.setPaintMat(flipped.Inverse().ToMat3x4())
		} else {
			frag.setPaintMat(paint.xform.Inverse().ToMat3x4())
		}
		frag.setType(nsvg_SHADER_FILLIMG)

		if tex.texType == nvg_TEXTURE_RGBA {
			if tex.flags&IMAGE_PREMULTIPLIED != 0 {
				frag.setTexType(0)
			} else {
				frag.setTexType(1)
			}
		} else {
			frag.setTexType(2)
		}
	} else {
		frag.setType(nsvg_SHADER_FILLGRAD)
		frag.setRadius(paint.radius)
		frag.setFeather(paint.feather)
		frag.setPaintMat(paint.xform.Inverse().ToMat3x4())
	}
	return nil
}

func (c *glContext) setUniforms(uniformOffset, image int) {
	frag := c.uniforms[uniformOffset]
	gl.Uniform4fv(c.shader.locations[glnvg_LOC_FRAG], frag[:])

	if image != 0 {
		c.bindTexture(&c.findTexture(image).tex)
		checkError(c, "tex paint tex")
	} else {
		c.bindTexture(&gl.Texture{})
	}
}

func (c *glContext) fill(call *glCall) {
	paths := c.paths[call.pathOffset : call.pathOffset+call.pathCount]

	// Draw shapes
	gl.Enable(gl.STENCIL_TEST)
	c.setStencilMask(0xff)
	c.setStencilFunc(gl.ALWAYS, 0x00, 0xff)
	gl.ColorMask(false, false, false, false)

	// set bindpoint for solid loc
	c.setUniforms(call.uniformOffset, 0)
	checkError(c, "fill simple")

	gl.StencilOpSeparate(gl.FRONT, gl.KEEP, gl.KEEP, gl.INCR_WRAP)
	gl.StencilOpSeparate(gl.BACK, gl.KEEP, gl.KEEP, gl.DECR_WRAP)

	gl.Disable(gl.CULL_FACE)
	for _, path := range paths {
		gl.DrawArrays(gl.TRIANGLE_FAN, path.fillOffset, path.fillCount)
	}
	gl.Enable(gl.CULL_FACE)

	// Draw anti-aliased pixels
	gl.ColorMask(true, true, true, true)
	c.setUniforms(call.uniformOffset+1, call.image)
	checkError(c, "fill fill")

	if c.flags&ANTIALIAS != 0 {
		c.setStencilFunc(gl.EQUAL, 0x00, 0xff)
		gl.StencilOp(gl.KEEP, gl.KEEP, gl.KEEP)
		// Draw fringes
		for _, path := range paths {
			gl.DrawArrays(gl.TRIANGLE_FAN, path.strokeOffset, path.strokeCount)
		}
	}

	// Draw fill
	c.setStencilFunc(gl.NOTEQUAL, 0x00, 0xff)
	gl.StencilOp(gl.ZERO, gl.ZERO, gl.ZERO)
	gl.DrawArrays(gl.TRIANGLES, call.triangleOffset, call.triangleCount)

	gl.Disable(gl.STENCIL_TEST)
}

func (c *glContext) convexFill(call *glCall) {
	paths := c.paths[call.pathOffset : call.pathOffset+call.pathCount]

	c.setUniforms(call.uniformOffset, call.image)
	checkError(c, "convex fill")

	for _, path := range paths {
		gl.DrawArrays(gl.TRIANGLE_FAN, path.fillOffset, path.fillCount)
	}

	if c.flags&ANTIALIAS != 0 {
		for _, path := range paths {
			gl.DrawArrays(gl.TRIANGLE_STRIP, path.strokeOffset, path.strokeCount)
		}
	}
}

func (c *glContext) stroke(call *glCall) {
	paths := c.paths[call.pathOffset : call.pathOffset+call.pathCount]

	if c.flags&STENCIL_STROKES != 0 {
		gl.Enable(gl.STENCIL_TEST)
		c.setStencilMask(0xff)

		// Fill the stroke base without overlap
		c.setStencilFunc(gl.EQUAL, 0x00, 0xff)
		gl.StencilOp(gl.KEEP, gl.KEEP, gl.INCR)
		c.setUniforms(call.uniformOffset+1, call.image)
		checkError(c, "stroke fill 0")
		for _, path := range paths {
			gl.DrawArrays(gl.TRIANGLE_STRIP, path.strokeOffset, path.strokeCount)
		}

		// Draw anti-aliased pixels.
		c.setUniforms(call.uniformOffset, call.image)
		c.setStencilFunc(gl.EQUAL, 0x00, 0xff)
		gl.StencilOp(gl.KEEP, gl.KEEP, gl.KEEP)
		for _, path := range paths {
			gl.DrawArrays(gl.TRIANGLE_STRIP, path.strokeOffset, path.strokeCount)
		}

		// Clear stencil buffer.
		gl.ColorMask(false, false, false, false)
		c.setStencilFunc(gl.ALWAYS, 0x00, 0xff)
		gl.StencilOp(gl.ZERO, gl.ZERO, gl.ZERO)
		checkError(c, "stroke fill 1")
		for _, path := range paths {
			gl.DrawArrays(gl.TRIANGLE_STRIP, path.strokeOffset, path.strokeCount)
		}
		gl.ColorMask(true, true, true, true)
		gl.Disable(gl.STENCIL_TEST)
	} else {
		c.setUniforms(call.uniformOffset, call.image)
		checkError(c, "stroke fill")
		for _, path := range paths {
			gl.DrawArrays(gl.TRIANGLE_STRIP, path.strokeOffset, path.strokeCount)
		}
	}
}

func (c *glContext) triangles(call *glCall) {
	c.setUniforms(call.uniformOffset, call.image)
	checkError(c, "triangles fill")
	gl.DrawArrays(gl.TRIANGLE_STRIP, call.triangleOffset, call.triangleCount)
}

type glParams struct {
	isEdgeAntiAlias bool
	context         *glContext
}

func (p *glParams) edgeAntiAlias() bool {
	return p.isEdgeAntiAlias
}

func (p *glParams) renderCreate() error {
	context := p.context
	//align := 4

	checkError(context, "init")

	if p.edgeAntiAlias() {
		err := context.shader.createShader("shader", shaderHeader, "#define EDGE_AA 1", fillVertexShader, fillFragmentShader)
		if err != nil {
			return err
		}
	} else {
		err := context.shader.createShader("shader", shaderHeader, "", fillVertexShader, fillFragmentShader)
		if err != nil {
			return err
		}
	}
	checkError(context, "init")
	context.shader.getUniforms()

	context.vertexBuffer = gl.CreateBuffer()

	checkError(context, "create done")
	gl.Finish()
	return nil
}

func (p *glParams) renderCreateTexture(texType nvgTextureType, w, h int, flags ImageFlags, data []byte) int {
	if nearestPow2(w) != w || nearestPow2(h) != h {
		if (flags&IMAGE_REPEATX) != 0 || (flags&IMAGE_REPEATY) != 0 {
			log.Printf("Repeat X/Y is not supported for non power-of-two textures (%d x %d)\n", w, h)
			flags &= ^(IMAGE_REPEATY | IMAGE_REPEATX)
		}
		if (flags & IMAGE_GENERATE_MIPMAPS) != 0 {
			log.Printf("Mip-maps is not support for non power-of-two textures (%d x %d)\n", w, h)
			flags &= ^IMAGE_GENERATE_MIPMAPS
		}
	}
	tex := p.context.allocTexture()
	tex.tex = gl.CreateTexture()
	tex.width = w
	tex.height = h
	tex.texType = texType
	tex.flags = flags

	p.context.bindTexture(&tex.tex)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)

	if texType == nvg_TEXTURE_RGBA {
		gl.TexImage2D(gl.TEXTURE_2D, 0, w, h, gl.RGBA, gl.UNSIGNED_BYTE, data)
	} else {
		gl.TexImage2D(gl.TEXTURE_2D, 0, w, h, gl.LUMINANCE, gl.UNSIGNED_BYTE, data)
	}

	if (flags & IMAGE_GENERATE_MIPMAPS) != 0 {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	} else {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	}
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

	if (flags & IMAGE_REPEATX) != 0 {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
	} else {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	}

	if (flags & IMAGE_REPEATY) != 0 {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
	} else {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	}

	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)

	if (flags & IMAGE_GENERATE_MIPMAPS) != 0 {
		gl.GenerateMipmap(gl.TEXTURE_2D)
	}

	p.context.checkError("create tex")
	p.context.bindTexture(&gl.Texture{})

	return tex.id
}

func (p *glParams) renderDeleteTexture(id int) error {
	tex := p.context.findTexture(id)
	if tex.tex.Valid() && (tex.flags&glnvg_IMAGE_NODELETE) == 0 {
		gl.DeleteTexture(tex.tex)
		tex.id = 0
		tex.tex = gl.Texture{}
		return nil
	}
	return errors.New("invalid texture in GLParams.deleteTexture")
}

func (p *glParams) renderUpdateTexture(image, x, y, w, h int, data []byte) error {
	tex := p.context.findTexture(image)
	if tex == nil {
		return errors.New("invalid texture in GLParams.updateTexture")
	}
	p.context.bindTexture(&tex.tex)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)

	if tex.texType == nvg_TEXTURE_RGBA {
		data = data[y*tex.width*4:]
	} else {
		data = data[y*tex.width:]
	}
	x = 0
	w = tex.width

	if tex.texType == nvg_TEXTURE_RGBA {
		gl.TexSubImage2D(gl.TEXTURE_2D, 0, x, y, w, h, gl.RGBA, gl.UNSIGNED_BYTE, data)
	} else {
		gl.TexSubImage2D(gl.TEXTURE_2D, 0, x, y, w, h, gl.LUMINANCE, gl.UNSIGNED_BYTE, data)
	}

	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)

	p.context.bindTexture(nil)

	return nil
}

func (p *glParams) renderGetTextureSize(image int) (int, int, error) {
	tex := p.context.findTexture(image)
	if tex == nil {
		return -1, -1, errors.New("invalid texture in GLParams.getTextureSize")
	}
	return tex.width, tex.height, nil
}

func (p *glParams) renderViewport(width, height int) {
	p.context.view[0] = float32(width)
	p.context.view[1] = float32(height)
}

func (p *glParams) renderCancel() {
	c := p.context
	c.vertexes = c.vertexes[:0]
	c.paths = c.paths[:0]
	c.calls = c.calls[:0]
	c.uniforms = c.uniforms[:0]
}

func (p *glParams) renderFlush() {
	c := p.context

	if len(c.calls) > 0 {
		gl.UseProgram(c.shader.program)

		gl.BlendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
		gl.Enable(gl.CULL_FACE)
		gl.CullFace(gl.BACK)
		gl.FrontFace(gl.CCW)
		gl.Enable(gl.BLEND)
		gl.Disable(gl.DEPTH_TEST)
		gl.Disable(gl.SCISSOR_TEST)
		gl.ColorMask(true, true, true, true)
		gl.StencilMask(0xffffffff)
		gl.StencilOp(gl.KEEP, gl.KEEP, gl.KEEP)
		gl.StencilFunc(gl.ALWAYS, 0, 0xffffffff)
		gl.ActiveTexture(gl.TEXTURE0)

		c.stencilMask = 0xffffffff
		c.stencilFunc = gl.ALWAYS
		c.stencilFuncRef = 0
		c.stencilFuncMask = 0xffffffff

		// Upload vertex data
		gl.BindBuffer(gl.ARRAY_BUFFER, c.vertexBuffer)
		gl.BufferData(gl.ARRAY_BUFFER, c.vertexes, gl.STREAM_DRAW)
		gl.EnableVertexAttribArray(gl.Attrib{0})
		gl.EnableVertexAttribArray(gl.Attrib{1})
		gl.VertexAttribPointer(gl.Attrib{0}, 2, gl.FLOAT, false, 4*4, 0)
		gl.VertexAttribPointer(gl.Attrib{1}, 2, gl.FLOAT, false, 4*4, 8)

		// Set view and texture just once per frame.
		gl.Uniform1i(c.shader.locations[glnvg_LOC_TEX], 0)
		gl.Uniform2fv(c.shader.locations[glnvg_LOC_VIEWSIZE], c.view[:])

		for i := range c.calls {
			call := &c.calls[i]
			switch call.callType {
			case glnvg_FILL:
				c.fill(call)
			case glnvg_CONVEXFILL:
				c.convexFill(call)
			case glnvg_STROKE:
				c.stroke(call)
			case glnvg_TRIANGLES:
				c.triangles(call)
			}
		}
		gl.DisableVertexAttribArray(gl.Attrib{0})
		gl.DisableVertexAttribArray(gl.Attrib{1})
		gl.Disable(gl.CULL_FACE)
		gl.BindBuffer(gl.ARRAY_BUFFER, gl.Buffer{})
		gl.UseProgram(gl.Program{})
		c.bindTexture(nil)
	}
	c.vertexes = c.vertexes[:0]
	c.paths = c.paths[:0]
	c.calls = c.calls[:0]
	c.uniforms = c.uniforms[:0]
}

func (p *glParams) renderFill(paint *Paint, scissor *nvgScissor, fringe float32, bounds [4]float32, paths []nvgPath) {
	c := p.context
	var glPaths []glPath
	c.calls = append(c.calls, glCall{
		pathCount: len(paths),
		image:     paint.image,
	})
	call := &c.calls[len(c.calls)-1]
	glPaths, call.pathOffset = c.allocPath(call.pathCount)

	if len(paths) > 0 && paths[0].convex {
		call.callType = glnvg_CONVEXFILL
	} else {
		call.callType = glnvg_FILL
	}

	// Allocate vertices for all the paths
	newVertexes := make([]nvgVertex, maxVertexCount(paths)+6)
	vertexes := newVertexes[:]
	vertexOffset := len(c.vertexes) / 16 // c.vertexes is []byte, but data unit is [4]float32

	for i := range paths {
		glPath := &glPaths[i]
		path := &paths[i]

		fillCount := len(path.fills)
		if fillCount > 0 {
			glPath.fillOffset = vertexOffset
			glPath.fillCount = fillCount
			copy(vertexes, path.fills)
			vertexes = vertexes[fillCount:]
			vertexOffset += fillCount
		} else {
			glPath.fillOffset = 0
			glPath.fillCount = 0
		}

		strokeCount := len(path.strokes)
		if fillCount > 0 {
			glPath.strokeOffset = vertexOffset
			glPath.strokeCount = strokeCount
			copy(vertexes, path.strokes)
			vertexes = vertexes[strokeCount:]
			vertexOffset += strokeCount
		} else {
			glPath.strokeOffset = 0
			glPath.strokeCount = 0
		}
	}

	// Quad
	call.triangleOffset = vertexOffset
	call.triangleCount = 6
	vertexes[0] = nvgVertex{bounds[0], bounds[3], 0.5, 1.0}
	vertexes[1] = nvgVertex{bounds[2], bounds[3], 0.5, 1.0}
	vertexes[2] = nvgVertex{bounds[2], bounds[1], 0.5, 1.0}

	vertexes[3] = nvgVertex{bounds[0], bounds[3], 0.5, 1.0}
	vertexes[4] = nvgVertex{bounds[2], bounds[1], 0.5, 1.0}
	vertexes[5] = nvgVertex{bounds[0], bounds[1], 0.5, 1.0}

	// Register all new vertexes to GLContext as []byte for OpenGL API
	c.appendVertex(newVertexes)

	// Setup uniforms for draw calls
	var paintUniform *glFragUniforms
	if call.callType == glnvg_FILL {
		var uniforms []glFragUniforms
		uniforms, call.uniformOffset = c.allocFragUniforms(2)
		// Simple shader for stencil
		u0 := &uniforms[0]
		u0.reset()
		u0.setStrokeThr(-1.0)
		u0.setType(nsvg_SHADER_SIMPLE)
		paintUniform = &uniforms[1]
	} else {
		var uniforms []glFragUniforms
		uniforms, call.uniformOffset = c.allocFragUniforms(1)
		paintUniform = &uniforms[0]
	}
	// Fill shader
	paintUniform.reset()
	c.convertPaint(paintUniform, paint, scissor, fringe, fringe, -1.0)
}

func (p *glParams) renderStroke(paint *Paint, scissor *nvgScissor, fringe float32, strokeWidth float32, paths []nvgPath) {
	c := p.context
	var glPaths []glPath
	p.context.calls = append(c.calls, glCall{})
	call := &c.calls[len(c.calls)-1]
	call.callType = glnvg_STROKE
	glPaths, call.pathOffset = c.allocPath(len(paths))
	call.pathCount = len(paths)
	call.image = paint.image

	// Allocate vertices for all the paths
	newVertexes := make([]nvgVertex, maxVertexCount(paths))
	vertexes := newVertexes[:]
	vertexOffset := len(c.vertexes) / 16 // c.vertexes is []byte, but data unit is [4]float32

	for i := range paths {
		glPath := &glPaths[i]
		path := &paths[i]

		strokeCount := len(path.strokes)
		if strokeCount > 0 {
			glPath.strokeOffset = vertexOffset
			glPath.strokeCount = strokeCount
			copy(vertexes, path.strokes)
			vertexes = vertexes[strokeCount:]
			vertexOffset += strokeCount
		} else {
			glPath.strokeOffset = 0
			glPath.strokeCount = 0
		}
	}

	// Register all new vertexes to GLContext as []byte for OpenGL API
	c.appendVertex(newVertexes)

	// Fill shader
	if c.flags&STENCIL_STROKES != 0 {
		var uniforms []glFragUniforms
		uniforms, call.uniformOffset = c.allocFragUniforms(2)
		u0 := &uniforms[0]
		u0.reset()
		c.convertPaint(u0, paint, scissor, strokeWidth, fringe, -1.0)
		u1 := &uniforms[1]
		u1.reset()
		c.convertPaint(u1, paint, scissor, strokeWidth, fringe, -1.0-0.5/266.0)
	} else {
		var uniforms []glFragUniforms
		uniforms, call.uniformOffset = c.allocFragUniforms(1)
		u0 := &uniforms[0]
		u0.reset()
		c.convertPaint(u0, paint, scissor, strokeWidth, fringe, -1.0)
	}
}

func (p *glParams) renderTriangles(paint *Paint, scissor *nvgScissor, vertexes []nvgVertex) {
	c := p.context
	p.context.calls = append(c.calls, glCall{})
	call := &c.calls[len(c.calls)-1]
	call.callType = glnvg_TRIANGLES
	call.image = paint.image

	call.triangleOffset = len(c.vertexes) / 16 // c.vertexes is []byte, but data unit is [4]float32
	call.triangleCount = len(vertexes)

	c.appendVertex(vertexes)

	// Fill shader
	var uniforms []glFragUniforms
	uniforms, call.uniformOffset = c.allocFragUniforms(1)
	u0 := &uniforms[0]
	u0.reset()
	c.convertPaint(u0, paint, scissor, 1.0, 1.0, -1.0)
	u0.setType(nsvg_SHADER_IMG)
}

func (p *glParams) renderDelete() {
	c := p.context
	c.shader.deleteShader()
	if c.vertexBuffer.Valid() {
		gl.DeleteBuffer(c.vertexBuffer)
	}
	for _, texture := range c.textures {
		if texture.tex.Valid() && (texture.flags&glnvg_IMAGE_NODELETE) == 0 {
			gl.DeleteTexture(texture.tex)
		}
	}
	p.context = nil
}

func dumpShaderError(shader gl.Shader, name, typeName string) error {
	str := gl.GetShaderInfoLog(shader)
	msg := fmt.Sprintf("Shader %s/%s error:\n%s\n", name, typeName, str)
	log.Println(msg)
	return errors.New(msg)
}

func dumpProgramError(program gl.Program, name string) error {
	str := gl.GetProgramInfoLog(program)
	msg := fmt.Sprintf("Program %s error:\n%s\n", name, str)
	log.Println(msg)
	return errors.New(msg)
}

func checkError(p *glContext, str string) {
	if p.flags&DEBUG == 0 {
		return
	}
	err := gl.GetError()
	if err != gl.NO_ERROR {
		log.Printf("Error %08x after %s\n", int(err), str)
	}
}

func maxVertexCount(paths []nvgPath) int {
	count := 0
	for i := range paths {
		path := &paths[i]
		count += len(path.fills)
		count += len(path.strokes)
	}
	return count
}

var fillVertexShader string = `
#ifdef NANOVG_GL3
   uniform vec2 viewSize;
   in vec2 vertex;
   in vec2 tcoord;
   out vec2 ftcoord;
   out vec2 fpos;
#else
   uniform vec2 viewSize;
   attribute vec2 vertex;
   attribute vec2 tcoord;
   varying vec2 ftcoord;
   varying vec2 fpos;
#endif
void main(void) {
   ftcoord = tcoord;
   fpos = vertex;
   gl_Position = vec4(2.0*vertex.x/viewSize.x - 1.0, 1.0 - 2.0*vertex.y/viewSize.y, 0, 1);
}`

var fillFragmentShader = `
#ifdef GL_ES
#if defined(GL_FRAGMENT_PRECISION_HIGH) || defined(NANOVG_GL3)
 precision highp float;
#else
 precision mediump float;
#endif
#endif
#ifdef NANOVG_GL3
#ifdef USE_UNIFORMBUFFER
       layout(std140) uniform frag {
               mat3 scissorMat;
               mat3 paintMat;
               vec4 innerCol;
               vec4 outerCol;
               vec2 scissorExt;
               vec2 scissorScale;
               vec2 extent;
               float radius;
               float feather;
               float strokeMult;
               float strokeThr;
               int texType;
               int type;
       };
#else
       // NANOVG_GL3 && !USE_UNIFORMBUF
       uniform vec4 frag[UNIFORMARRAY_SIZE];
#endif
       uniform sampler2D tex;
       in vec2 ftcoord;
       in vec2 fpos;
       out vec4 outColor;
#else
       // !NANOVG_
       uniform vec4 frag[UNIFORMARRAY_SIZE];
       uniform sampler2D tex;
       varying vec2 ftcoord;
       varying vec2 fpos;
#endif
#ifndef USE_UNIFORMBUFFER
       #define scissorMat mat3(frag[0].xyz, frag[1].xyz, frag[2].xyz)
       #define paintMat mat3(frag[3].xyz, frag[4].xyz, frag[5].xyz)
       #define innerCol frag[6]
       #define outerCol frag[7]
       #define scissorExt frag[8].xy
       #define scissorScale frag[8].zw
       #define extent frag[9].xy
       #define radius frag[9].z
       #define feather frag[9].w
       #define strokeMult frag[10].x
       #define strokeThr frag[10].y
       #define texType int(frag[10].z)
       #define type int(frag[10].w)
#endif

float sdroundrect(vec2 pt, vec2 ext, float rad) {
       vec2 ext2 = ext - vec2(rad,rad);
       vec2 d = abs(pt) - ext2;
       return min(max(d.x,d.y),0.0) + length(max(d,0.0)) - rad;
}

// Scissoring
float scissorMask(vec2 p) {
       vec2 sc = (abs((scissorMat * vec3(p,1.0)).xy) - scissorExt);
       sc = vec2(0.5,0.5) - sc * scissorScale;
       return clamp(sc.x,0.0,1.0) * clamp(sc.y,0.0,1.0);
}
#ifdef EDGE_AA
// Stroke - from [0..1] to clipped pyramid, where the slope is 1px.
float strokeMask() {
       return min(1.0, (1.0-abs(ftcoord.x*2.0-1.0))*strokeMult) * min(1.0, ftcoord.y);
}
#endif

void main(void) {
   vec4 result;
       float scissor = scissorMask(fpos);
#ifdef EDGE_AA
       float strokeAlpha = strokeMask();
#else
       float strokeAlpha = 1.0;
#endif
       if (type == 0) {                        // Gradient
               // Calculate gradient color using box gradient
               vec2 pt = (paintMat * vec3(fpos,1.0)).xy;
               float d = clamp((sdroundrect(pt, extent, radius) + feather*0.5) / feather, 0.0, 1.0);
               vec4 color = mix(innerCol,outerCol,d);
               // Combine alpha
               color *= strokeAlpha * scissor;
               result = color;
       } else if (type == 1) {         // Image
               // Calculate color fron texture
               vec2 pt = (paintMat * vec3(fpos,1.0)).xy / extent;
#ifdef NANOVG_GL3
               vec4 color = texture(tex, pt);
#else
               vec4 color = texture2D(tex, pt);
#endif
               if (texType == 1) color = vec4(color.xyz*color.w,color.w);
               if (texType == 2) color = vec4(color.x);
               // Apply color tint and alpha.
               color *= innerCol;
               // Combine alpha
               color *= strokeAlpha * scissor;
               result = color;
       } else if (type == 2) {         // Stencil fill
               result = vec4(1,1,1,1);
       } else if (type == 3) {         // Textured tris
#ifdef NANOVG_GL3
               vec4 color = texture(tex, ftcoord);
#else
               vec4 color = texture2D(tex, ftcoord);
#endif
               if (texType == 1) color = vec4(color.xyz*color.w,color.w);
               if (texType == 2) color = vec4(color.x);
               color *= scissor;
               result = color * innerCol;
       }
#ifdef EDGE_AA
       if (strokeAlpha < strokeThr) discard;
#endif
#ifdef NANOVG_GL3
       outColor = result;
#else
       gl_FragColor = result;
#endif
}`