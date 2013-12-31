package main

import (
	"fmt"
	"github.com/remogatto/application"
	"github.com/remogatto/egl"
	"github.com/remogatto/egl/platform"
	"github.com/remogatto/gorgasm"
	gl "github.com/remogatto/opengles2"
	"image/png"
	"runtime"
	"time"
	"unsafe"
)

const (
	FRAMES_PER_SECOND = 24
	GOPHER_PNG        = "res/drawable/gopher.png"
)

var (
	verticesArrayBuffer             uint32
	textureBuffer                   uint32
	unifTexture, attrPos, attrTexIn uint32
	currWidth, currHeight           int

	vertices = [24]float32{
		-1.0, -1.0, 0.0, 1.0, 0.0, 1.0,
		1.0, -1.0, 0.0, 1.0, 1.0, 1.0,
		1.0, 1.0, 0.0, 1.0, 1.0, 0.0,
		-1.0, 1.0, 0.0, 1.0, 0.0, 0.0,
	}
	vsh = `
        attribute vec4 pos;
        attribute vec2 texIn;
        varying vec2 texOut;
        void main() {
          gl_Position = pos;
          texOut = texIn;
        }
`
	fsh = `
        precision mediump float;
        varying vec2 texOut;
        uniform sampler2D texture;
	void main() {
		gl_FragColor = texture2D(texture, texOut);
	}
`
)

// Create a fragment shader from a string and return its reference.
func FragmentShader(s string) uint32 {
	shader := gl.CreateShader(gl.FRAGMENT_SHADER)
	check()
	gl.ShaderSource(shader, 1, &s, nil)
	check()
	gl.CompileShader(shader)
	check()
	var stat int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &stat)
	if stat == 0 {
		var s = make([]byte, 1000)
		var length gl.Sizei
		_log := string(s)
		gl.GetShaderInfoLog(shader, 1000, &length, unsafe.Pointer(&_log))
		panic(fmt.Sprintf("Error: compiling:\n%s\n", _log))
	}
	return shader

}

// Create a vertex shader from a string and return its reference.
func VertexShader(s string) uint32 {
	shader := gl.CreateShader(gl.VERTEX_SHADER)
	gl.ShaderSource(shader, 1, &s, nil)
	gl.CompileShader(shader)
	var stat int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &stat)
	if stat == 0 {
		var s = make([]byte, 1000)
		var length gl.Sizei
		_log := string(s)
		gl.GetShaderInfoLog(shader, 1000, &length, unsafe.Pointer(&_log))
		panic(fmt.Sprintf("Error: compiling:\n%s\n", _log))
	}
	return shader
}

// Create a program from vertex and fragment shaders.
func Program(fsh, vsh uint32) uint32 {
	p := gl.CreateProgram()
	gl.AttachShader(p, fsh)
	gl.AttachShader(p, vsh)
	gl.LinkProgram(p)
	var stat int32
	gl.GetProgramiv(p, gl.LINK_STATUS, &stat)
	if stat == 0 {
		var s = make([]byte, 1000)
		var length gl.Sizei
		_log := string(s)
		gl.GetProgramInfoLog(p, 1000, &length, &_log)
		panic(fmt.Sprintf("Error: linking:\n%s\n", _log))
	}
	return p
}

// renderLoop renders the current scene at a given frame rate.
type renderLoop struct {
	pause, terminate, resume chan int
	ticker                   *time.Ticker
	eglState                 platform.EGLState
}

// newRenderLoop returns a new renderLoop instance. It takes the
// number of frame-per-second as argument.
func newRenderLoop(eglState platform.EGLState, fps int) *renderLoop {
	renderLoop := &renderLoop{
		pause:     make(chan int),
		terminate: make(chan int),
		resume:    make(chan int),
		ticker:    time.NewTicker(time.Duration(1e9 / int(fps))),
		eglState:  eglState,
	}

	return renderLoop
}

// Pause returns the pause channel of the loop.
// If a value is sent to this channel, the loop will be paused.
func (l *renderLoop) Pause() chan int {
	return l.pause
}

// Terminate returns the terminate channel of the loop.
// If a value is sent to this channel, the loop will be terminated.
func (l *renderLoop) Terminate() chan int {
	return l.terminate
}

// Run runs renderLoop. The loop renders a frame and swaps the buffer
// at each tick received.
func (l *renderLoop) Run() {
	// Lock/unlock the loop to the current OS thread. This is
	// necessary because OpenGL functions should be called from
	// the same thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	display := l.eglState.Display
	surface := l.eglState.Surface
	context := l.eglState.Context

	if ok := egl.MakeCurrent(display, surface, surface, context); !ok {
		panic(egl.NewError(egl.GetError()))
	}

	l.initGL()

	for {
		select {

		// Pause the loop.
		case <-l.pause:
			l.ticker.Stop()
			l.pause <- 0

			// Terminate the loop.
		case <-l.terminate:
			l.terminate <- 0

			// Resume the loop.
		case <-l.resume:
			// Do something when the rendering loop is
			// resumed.

			// At each tick render a frame and swap buffers.
		case <-l.ticker.C:
			l.draw()
			egl.SwapBuffers(l.eglState.Display, l.eglState.Surface)
		}
	}
}

// eventsLoop receives events from the framework and reacts
// accordingly.
type eventsLoop struct {
	pause, terminate chan int
	renderLoop       *renderLoop
}

// newEventsLoop returns a new eventsLoop instance. It takes a
// renderLoop instance as argument.
func newEventsLoop(renderLoop *renderLoop) *eventsLoop {
	eventsLoop := &eventsLoop{
		pause:      make(chan int),
		terminate:  make(chan int),
		renderLoop: renderLoop,
	}
	return eventsLoop
}

// Pause returns the pause channel of the loop.
// If a value is sent to this channel, the loop will be paused.
func (l *eventsLoop) Pause() chan int {
	return l.pause
}

// Terminate returns the terminate channel of the loop.
// If a value is sent to this channel, the loop will be terminated.
func (l *eventsLoop) Terminate() chan int {
	return l.terminate
}

// Run runs eventsLoop listening to events originating from the
// framwork.
func (l *eventsLoop) Run() {
	for {
		select {
		case <-l.pause:
			l.pause <- 0
		case <-l.terminate:
			l.terminate <- 0

			// Receive events from the framework.
		case untypedEvent := <-gorgasm.Events:
			switch event := untypedEvent.(type) {

			// Finger down/up on the screen.
			case gorgasm.ActionUpDownEvent:
				if event.Down {
					application.Logf("Finger is DOWN at coord %d %d", event.X, event.Y)
				} else {
					application.Logf("Finger is now UP")
				}

				// Finger is moving on the screen.
			case gorgasm.ActionMoveEvent:
				application.Logf("Finger is moving at coord %d %d", event.X, event.Y)

			case gorgasm.PauseEvent:
				application.Logf("Application was paused. Stopping rendering ticker.")
				l.renderLoop.pause <- 1

			case gorgasm.ResumeEvent:
				application.Logf("Application was resumed. Reactivating rendering ticker.")
				l.renderLoop.resume <- 1

			}
		}
	}
}

func check() {
	error := gl.GetError()
	if error != 0 {
		application.Logf("An error occurred! Code: 0x%x", error)
	}
}

func (l *renderLoop) loadImage(filename string) ([]byte, int, int) {
	// Request an asset to the asset manager. When the app runs on
	// an Android device, the apk will be unpacked and the file
	// will be read from it and copied to a byte buffer.
	assetBuffer := <-gorgasm.LoadAsset(filename)

	// Decode the image.
	img, err := png.Decode(assetBuffer)
	if err != nil {
		panic(err)
	}

	// Prepare the image to be placed on a texture.
	bounds := img.Bounds()
	width, height := bounds.Size().X, bounds.Size().Y
	buffer := make([]byte, width*height*4)
	index := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			buffer[index] = byte(r)
			buffer[index+1] = byte(g)
			buffer[index+2] = byte(b)
			buffer[index+3] = byte(a)
			index += 4
		}
	}
	return buffer, width, height
}

func (l *renderLoop) initGL() {
	// Get native surface dimensions
	width := l.eglState.SurfaceWidth
	height := l.eglState.SurfaceHeight

	// Set the viewport
	gl.Viewport(0, 0, gl.Sizei(width), gl.Sizei(height))
	check()

	// Compile the shaders
	program := Program(FragmentShader(fsh), VertexShader(vsh))
	gl.UseProgram(program)
	check()

	// Get attributes
	attrPos = uint32(gl.GetAttribLocation(program, "pos"))
	attrTexIn = uint32(gl.GetAttribLocation(program, "texIn"))
	unifTexture = gl.GetUniformLocation(program, "texture")
	gl.EnableVertexAttribArray(attrPos)
	gl.EnableVertexAttribArray(attrTexIn)
	check()

	// Upload vertices data
	gl.GenBuffers(1, &verticesArrayBuffer)
	gl.BindBuffer(gl.ARRAY_BUFFER, verticesArrayBuffer)
	gl.BufferData(gl.ARRAY_BUFFER, gl.SizeiPtr(len(vertices))*4, gl.Void(&vertices[0]), gl.STATIC_DRAW)
	check()
	// Upload texture data
	imageBuffer, width, height := l.loadImage(GOPHER_PNG)
	gl.GenTextures(1, &textureBuffer)
	gl.BindTexture(gl.TEXTURE_2D, textureBuffer)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.Sizei(width), gl.Sizei(height), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Void(&imageBuffer[0]))
	check()

	gl.ClearColor(0.0, 0.0, 0.0, 1.0)
}

func (l *renderLoop) draw() {
	gl.Clear(gl.COLOR_BUFFER_BIT)
	gl.BindBuffer(gl.ARRAY_BUFFER, verticesArrayBuffer)
	gl.VertexAttribPointer(attrPos, 4, gl.FLOAT, false, 6*4, 0)

	// bind texture - FIX size of vertex

	gl.VertexAttribPointer(attrTexIn, 2, gl.FLOAT, false, 6*4, 4*4)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, textureBuffer)
	gl.Uniform1i(int32(unifTexture), 0)

	gl.DrawArrays(gl.TRIANGLE_FAN, 0, 4)
	gl.Flush()
	gl.Finish()
}