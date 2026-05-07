package client

import (
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/render"
)

func iconSetForConfig(mode string) render.IconSet {
	icons, ok := render.IconSetForName(mode)
	if !ok {
		return render.DefaultIconSet()
	}
	return icons
}

func (cr *ClientRenderer) ConfigureTheme(cfg *config.Config) {
	if cfg == nil {
		cr.SetIconSet(render.DefaultIconSet())
		return
	}
	cr.SetIconSet(iconSetForConfig(cfg.EffectiveThemeIcons()))
}

func (cr *ClientRenderer) SetIconSet(icons render.IconSet) {
	if cr == nil || cr.renderer == nil {
		return
	}
	cr.renderer.SetIconSet(icons)
}

func (cr *ClientRenderer) IconSet() render.IconSet {
	if cr == nil || cr.renderer == nil {
		return render.DefaultIconSet()
	}
	return cr.renderer.IconSet()
}

func (r *Renderer) SetIconSet(icons render.IconSet) {
	r.withActor(func(st *rendererActorState) {
		st.compositor.SetIconSet(icons)
		next := *st.snapshot
		next.iconSet = st.compositor.IconSet()
		st.snapshot = &next
		r.publishSnapshot(&next)
	})
}

func (r *Renderer) IconSet() render.IconSet {
	if r == nil {
		return render.DefaultIconSet()
	}
	snap := r.loadSnapshot()
	if snap == nil {
		return render.DefaultIconSet()
	}
	if snap.iconSet == (render.IconSet{}) {
		return render.DefaultIconSet()
	}
	return snap.iconSet
}
