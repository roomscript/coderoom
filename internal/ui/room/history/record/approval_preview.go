package record

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/ui/room/approval"
)

const approvalFileLimit = 10

func evidenceLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func fileApprovalPreview(p *room.FileApproval, width int) string {
	lines := []string{"⚡ Approval: " + p.Summary}
	if p.Explanation != "" {
		lines = append(lines, "Explanation: "+p.Explanation)
	}
	budget, omitted, truncated := fileChangePreviewLines, 0, false
	for i, file := range p.Files {
		body := evidenceLines(file.Text)
		if i >= approvalFileLimit {
			omitted += len(body)
			continue
		}
		lines = append(lines, file.ChangeKind+": "+file.Path+" ("+approvalFileCounts(file)+")")
		count := min(budget, len(body))
		for _, line := range body[:count] {
			safe := approval.ExpandTabs(approval.EscapeText(line), 2)
			cols := fileChangePreviewMaxCols(width)
			truncated = truncated || ansi.StringWidth(safe) > cols
			// Escaping and tab expansion precede column measurement and styling.
			lines = append(lines, truncateColumns(safe, cols))
		}
		budget -= count
		omitted += len(body) - count
	}
	return appendApprovalOmissions(lines, p, omitted, truncated)
}

func appendApprovalOmissions(lines []string, p *room.FileApproval, omitted int, truncated bool) string {
	if len(p.Files) == 0 {
		lines = append(lines, "Proposed file details unavailable: no matching file-change evidence.")
	}
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("%d change-text lines omitted", omitted))
	}
	if len(p.Files) > approvalFileLimit {
		lines = append(lines, fmt.Sprintf("%d files omitted", len(p.Files)-approvalFileLimit))
	}
	if p.Supplemental {
		lines = append(lines, "Full request in transcript (includes supplemental evidence)")
	}
	if omitted > 0 || len(p.Files) > approvalFileLimit || p.Supplemental || truncated {
		lines = append(lines, "Ctrl+O history, Ctrl+G open transcript")
	}
	return strings.Join(lines, "\n")
}

func approvalFileCounts(file room.ApprovalFile) string {
	if file.Format == "addition" {
		return fmt.Sprintf("+%d -0", len(evidenceLines(file.Text)))
	}
	if file.Format == "content" {
		return fmt.Sprintf("%d supplied content lines", len(evidenceLines(file.Text)))
	}
	if file.Format == "snippet" {
		added, deleted, ok := snippetCounts(file.Text)
		if ok {
			return fmt.Sprintf("snippet +%d -%d", added, deleted)
		}
	}
	if file.Format == "unified" {
		added, deleted, ok := unifiedCounts(file.Text)
		if ok {
			return fmt.Sprintf("+%d -%d", added, deleted)
		}
	}
	return "counts unavailable; file-change evidence missing or unsupported"
}

func snippetCounts(text string) (int, int, bool) {
	lines := evidenceLines(text)
	if len(lines) == 0 || (lines[0] != "@@ replace matching occurrence @@" && lines[0] != "@@ replace all matching occurrences @@") {
		return 0, 0, false
	}
	added, deleted := 0, 0
	for i, line := range lines[1:] {
		if !isSnippetLine(line) {
			return 0, 0, false
		}
		// The adapter appends an empty prefixed line for a trailing snippet newline.
		if isSnippetTrailingNewline(lines, i+1) {
			continue
		}
		if line[0] == '+' {
			added++
		} else {
			deleted++
		}
	}
	return added, deleted, true
}

func isSnippetLine(line string) bool {
	return strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")
}

func isSnippetTrailingNewline(lines []string, i int) bool {
	return len(lines[i]) == 1 && (i+1 == len(lines) || !strings.HasPrefix(lines[i+1], lines[i]))
}

var unifiedHunk = regexp.MustCompile(`^@@ -[0-9]+(?:,([0-9]+))? \+[0-9]+(?:,([0-9]+))? @@(?:.*)$`)

func unifiedCounts(text string) (int, int, bool) {
	var counts unifiedCounter
	for _, line := range evidenceLines(text) {
		if !counts.consume(line) {
			return 0, 0, false
		}
	}
	return counts.added, counts.deleted, counts.hunks > 0 && counts.complete()
}

type unifiedCounter struct{ added, deleted, oldLeft, newLeft, hunks, headers int }

func (c *unifiedCounter) complete() bool { return c.oldLeft == 0 && c.newLeft == 0 }

func (c *unifiedCounter) consume(line string) bool {
	if match := unifiedHunk.FindStringSubmatch(line); match != nil {
		if !c.complete() || c.headers == 1 {
			return false
		}
		c.oldLeft, c.newLeft = hunkSize(match[1]), hunkSize(match[2])
		c.hunks++
		return c.oldLeft >= 0 && c.newLeft >= 0
	}
	if c.complete() {
		return c.consumeMetadata(line)
	}
	return c.consumeHunkLine(line)
}

func (c *unifiedCounter) consumeMetadata(line string) bool {
	if c.hunks > 0 {
		// A file's metadata precedes its hunks. Additional sections are unsupported.
		return line == `\ No newline at end of file`
	}
	if strings.HasPrefix(line, "--- ") && c.headers == 0 {
		c.headers = 1
		return true
	}
	if strings.HasPrefix(line, "+++ ") && c.headers == 1 {
		c.headers = 2
		return true
	}
	return c.headers == 0 && isUnifiedMetadata(line)
}

func (c *unifiedCounter) consumeHunkLine(line string) bool {
	if line == `\ No newline at end of file` {
		return true
	}
	if line == "" {
		return false
	}
	switch line[0] {
	case '+':
		c.added++
		c.newLeft--
	case '-':
		c.deleted++
		c.oldLeft--
	case ' ':
		c.oldLeft--
		c.newLeft--
	default:
		return false
	}
	return c.oldLeft >= 0 && c.newLeft >= 0
}

func hunkSize(text string) int {
	if text == "" {
		return 1
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return -1
	}
	return n
}

func isUnifiedMetadata(line string) bool {
	for _, prefix := range []string{"diff --git ", "index ", "new file mode ", "deleted file mode ", "old mode ", "new mode ", "rename from ", "rename to ", "similarity index "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
