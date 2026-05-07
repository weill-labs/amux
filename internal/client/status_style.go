package client

func (cr *ClientRenderer) SetStatusStyle(style string) {
	if cr == nil || cr.renderer == nil {
		return
	}
	cr.renderer.SetStatusStyle(style)
}

func (r *Renderer) SetStatusStyle(style string) {
	r.withActor(func(st *rendererActorState) {
		st.compositor.SetStatusStyle(style)
	})
}
