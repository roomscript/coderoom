package history

import rec "github.com/roomscript/coderoom/internal/ui/room/history/record"

func renderRecordCached(r viewRecord, ctx rec.RenderContext) (string, viewRecord) {
	key := ctx.Key
	if key.Mode == rec.RenderTranscript {
		key.Width = 0
	}
	if r.cache.valid && r.cache.key == key {
		return r.cache.rendered, r
	}
	ctx.Key = key
	r.cache = renderCache{
		valid:    true,
		key:      key,
		rendered: rec.Render(r.record, ctx),
	}
	return r.cache.rendered, r
}
