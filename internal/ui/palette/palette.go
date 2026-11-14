// Package palette contains shared UI color tokens and small color-related
// helpers. It intentionally has no dependencies on higher-level UI packages to
// avoid import cycles.
package palette

// ColorDeparted is the colour used to render historical output from agents that
// have left or crashed, replacing their assigned colour so their records dim
// rather than lose colour entirely.
const ColorDeparted = "#6b7280"

// ColorApproval uses the terminal's yellow so its theme controls contrast.
const ColorApproval = "3"

// File-change diff colours used when rendering patch output in the transcript.
const (
	ColorFileChangeDiffHeader = "#4FC3F7"
	ColorFileChangeHunk       = "#64B5F6"
	ColorFileChangeAdd        = "#66BB6A"
	ColorFileChangeDel        = "#EF5350"
	ColorFileChangeMeta       = "#BA68C8"
)
