package client

import (
	"io"
	"os"

	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/termprofile"
)

type processEnviron struct{}

func (processEnviron) Environ() []string {
	return os.Environ()
}

func (processEnviron) Getenv(key string) string {
	return os.Getenv(key)
}

func detectTerminalColorProfile(output io.Writer, environ termenv.Environ, outputOpts ...termenv.OutputOption) termenv.Profile {
	if output == nil {
		output = os.Stdout
	}
	if environ == nil {
		environ = processEnviron{}
	}
	return termprofile.Detect(output, environ, outputOpts...)
}

func attachColorProfile(output io.Writer, environ termenv.Environ, outputOpts ...termenv.OutputOption) string {
	return ""
}

func newAttachClientRenderer(cols, rows, scrollbackLines int, output io.Writer, environ termenv.Environ, outputOpts ...termenv.OutputOption) *ClientRenderer {
	cr := NewClientRendererWithScrollback(cols, rows, scrollbackLines)
	cr.SetColorProfile(detectTerminalColorProfile(output, environ, outputOpts...))
	return cr
}

func (cr *ClientRenderer) SetColorProfile(profile termenv.Profile) {
	if cr == nil || cr.renderer == nil {
		return
	}
	cr.renderer.SetColorProfile(profile)
}

func (cr *ClientRenderer) ColorProfile() termenv.Profile {
	if cr == nil || cr.renderer == nil {
		return termenv.TrueColor
	}
	return cr.renderer.ColorProfile()
}

func (r *Renderer) SetColorProfile(profile termenv.Profile) {
	r.withActor(func(st *rendererActorState) {
		st.compositor.SetColorProfile(profile)
	})
}

func (r *Renderer) ColorProfile() termenv.Profile {
	return withRendererActorValue(r, func(st *rendererActorState) termenv.Profile {
		return st.compositor.ColorProfile()
	})
}
