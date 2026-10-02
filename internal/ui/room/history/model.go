package history

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
	roomstate "github.com/trigosec/coderoom/internal/room"
	rec "github.com/trigosec/coderoom/internal/ui/room/history/record"
)

// streamSlot tracks an open streaming record by its StreamID.
type streamSlot struct {
	recordIdx int
}

type renderCache struct {
	valid    bool
	key      rec.RenderKey
	rendered string
}

type viewRecord struct {
	record rec.Record
	cache  renderCache
}

type historyLine struct {
	raw               string
	plain             string
	layoutPrefixWidth int
}

// Cursor tracks the history caret on the visible rendered surface.
type Cursor struct {
	Row          int
	Col          int
	PreferredCol int
	Visible      bool
}

// Selection tracks a caret-anchored range on the visible history surface.
type Selection struct {
	Anchor             Cursor
	Visible            bool
	CursorEndExclusive bool
}

// Model holds the conversation record list and its viewport.
type Model struct {
	viewport      viewport.Model
	records       []viewRecord
	lines         []historyLine
	streaming     map[string]streamSlot // streamID → open record slot
	departed      map[string]bool
	debugRowNums  bool
	viewportReady bool
	cursor        Cursor
	selection     Selection
	colorByAlias  func(string) string
	departedColor string
	colorVersion  uint64
}

// ScrollStats summarizes the history viewport scroll position using the same
// wrapped content the viewport renders.
type ScrollStats struct {
	Top          int
	ViewportRows int
	ContentRows  int
	AtBottom     bool
}

// New returns an uninitialised Model; call SetSize before first use.
// colorByAlias resolves an active agent alias to its colour; it may be nil.
// departedColor is applied to records belonging to agents that have left.
func New(colorByAlias func(string) string, departedColor string) Model {
	return Model{
		records:       []viewRecord{},
		streaming:     make(map[string]streamSlot),
		departed:      make(map[string]bool),
		colorByAlias:  colorByAlias,
		departedColor: departedColor,
	}
}

// ScrollStats reports the current scroll position and content height.
func (m Model) ScrollStats() ScrollStats {
	viewportRows := m.viewport.Height()
	contentRows := len(m.lines)
	top := m.viewport.YOffset()

	if viewportRows < 0 {
		viewportRows = 0
	}
	if contentRows < 0 {
		contentRows = 0
	}
	if top < 0 {
		top = 0
	}
	maxTop := 0
	if viewportRows > 0 && contentRows > viewportRows {
		maxTop = contentRows - viewportRows
	}
	if top > maxTop {
		top = maxTop
	}

	atBottom := true
	if viewportRows > 0 && contentRows > 0 {
		atBottom = top+viewportRows >= contentRows
	}
	return ScrollStats{
		Top:          top,
		ViewportRows: viewportRows,
		ContentRows:  contentRows,
		AtBottom:     atBottom,
	}
}

func splitHistoryLines(content string) []historyLine {
	if content == "" {
		return nil
	}
	rows := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	lines := make([]historyLine, len(rows))
	for i, row := range rows {
		lines[i] = historyLine{
			raw:   row,
			plain: ansi.Strip(row),
		}
	}
	return lines
}

// resolveColor returns the display colour for alias, accounting for departed state.
func (m Model) resolveColor(alias string) string {
	if m.departed[alias] {
		return m.departedColor
	}
	if m.colorByAlias != nil {
		return m.colorByAlias(alias)
	}
	return ""
}

func (m Model) viewportRenderContext() rec.RenderContext {
	return rec.RenderContext{
		Key: rec.RenderKey{
			Mode:         rec.RenderViewport,
			Width:        m.viewport.Width(),
			ColorVersion: m.colorVersion,
		},
		ColorForAlias: m.resolveColor,
	}
}

// Ready reports whether the viewport has been initialized with a size.
func (m Model) Ready() bool { return m.viewportReady }

// Records returns the current record slice.
func (m Model) Records() []rec.Record {
	records := make([]rec.Record, len(m.records))
	for i, r := range m.records {
		records[i] = r.record
	}
	return records
}

// IsStreaming reports whether alias currently has any open stream.
func (m Model) IsStreaming(alias string) bool {
	for _, slot := range m.streaming {
		if m.records[slot.recordIdx].record.Alias == alias {
			return true
		}
	}
	return false
}

// StreamingIdx returns the record index for the open output stream of alias.
func (m Model) StreamingIdx(alias string) (int, bool) {
	for _, slot := range m.streaming {
		r := m.records[slot.recordIdx].record
		if r.Alias != alias || r.Msg == nil {
			continue
		}
		if r.Kind == rec.KindAgentOutput {
			return slot.recordIdx, true
		}
	}
	return 0, false
}

// IsDeparted reports whether alias has left or crashed.
func (m Model) IsDeparted(alias string) bool { return m.departed[alias] }

// Height returns the current viewport height.
func (m Model) Height() int { return m.viewport.Height() }

// Width returns the current viewport width.
func (m Model) Width() int { return m.viewport.Width() }

// ToggleDebugRowNums flips the row-number overlay.
func (m Model) ToggleDebugRowNums() Model {
	m.debugRowNums = !m.debugRowNums
	return m
}

// ReplaceSnapshot swaps the history state with a canonical room snapshot.
// If the viewport was scrolled to the bottom before the swap, it stays
// pinned to the bottom afterward; otherwise the scroll position is left
// alone so reading scrolled-up history isn't interrupted by new content.
func (m Model) ReplaceSnapshot(snapshot roomstate.Snapshot) Model {
	m.records = make([]viewRecord, len(snapshot.Records))
	for i, r := range snapshot.Records {
		m.records[i] = viewRecord{record: r}
	}

	m.departed = make(map[string]bool, len(snapshot.Departed))
	for alias, isDeparted := range snapshot.Departed {
		m.departed[alias] = isDeparted
	}

	m.streaming = make(map[string]streamSlot, len(snapshot.OpenStreams))
	for _, stream := range snapshot.OpenStreams {
		m.streaming[string(stream.StreamID)] = streamSlot{recordIdx: stream.RecordIdx}
	}

	return m.syncViewport(false)
}

// ApplyRoomDelta updates the history from an incremental room delta.
func (m Model) ApplyRoomDelta(delta roomstate.Delta) Model {
	for _, update := range delta.RecordUpdates {
		if update.Index < 0 {
			continue
		}
		if update.Index >= len(m.records) {
			expanded := make([]viewRecord, update.Index+1)
			copy(expanded, m.records)
			m.records = expanded
		}
		m.records[update.Index].record = update.Record
		m.records[update.Index].cache = renderCache{}
	}

	departedChanged := !sameDeparted(m.departed, delta.Meta.Departed)
	m.departed = make(map[string]bool, len(delta.Meta.Departed))
	for alias, isDeparted := range delta.Meta.Departed {
		m.departed[alias] = isDeparted
	}
	if departedChanged {
		m.colorVersion++
	}

	m.streaming = make(map[string]streamSlot, len(delta.Meta.OpenStreams))
	for _, stream := range delta.Meta.OpenStreams {
		m.streaming[string(stream.StreamID)] = streamSlot{recordIdx: stream.RecordIdx}
	}

	return m.syncViewport(false)
}

func sameDeparted(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for alias, isDeparted := range left {
		if right[alias] != isDeparted {
			return false
		}
	}
	return true
}
