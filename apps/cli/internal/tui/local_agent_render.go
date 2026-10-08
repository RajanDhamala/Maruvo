package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
)

type renderedAgentLine struct {
	text string
	rows []string
}

type localAgentRender struct {
	width    int
	renderer *glamour.TermRenderer
	lines    map[int]renderedAgentLine
}

func (r *localAgentRender) markdown(index int, text string, width int) []string {
	if r.width != width || r.lines == nil {
		style := styles.DarkStyleConfig
		margin, indent, cyan := uint(0), uint(2), "6"
		style.Document.Margin = &margin
		style.Heading.Color = &cyan
		style.H1, style.H2, style.H3 = style.Heading, style.Heading, style.Heading
		style.H4, style.H5, style.H6 = style.Heading, style.Heading, style.Heading
		style.List.LevelIndent = 4
		style.Code.Color, style.Code.BackgroundColor = &cyan, nil
		style.Code.Prefix, style.Code.Suffix = "", ""
		style.CodeBlock.Margin, style.CodeBlock.Indent = &margin, &indent
		style.CodeBlock.Chroma = nil
		style.CodeBlock.Theme = "monokai"
		style.CodeBlock.BlockPrefix, style.CodeBlock.BlockSuffix = "╭─ Code\n", "\n╰─\n"
		r.renderer, _ = glamour.NewTermRenderer(glamour.WithStyles(style),
			glamour.WithWordWrap(max(20, width-8)), glamour.WithTableWrap(true))
		r.width, r.lines = width, make(map[int]renderedAgentLine)
	}

	if cached, ok := r.lines[index]; ok && cached.text == text {
		return cached.rows
	}

	rows := agentStreamingRows(text, width)
	if r.renderer != nil {
		if rendered, err := r.renderer.Render(ansi.Strip(text)); err == nil {
			rows = agentMarkdownRows(rendered, width)
		}
	}

	r.lines[index] = renderedAgentLine{text: text, rows: rows}

	return rows
}

// Commit complete paragraphs and fences; the unfinished tail stays plain while it grows.
func stableAgentMarkdown(text string) (string, string) {
	boundary, offset, fence := 0, 0, ""

	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if fence == "" {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				length := strings.IndexFunc(trimmed, func(r rune) bool { return r != rune(trimmed[0]) })
				if length < 0 {
					length = len(trimmed)
				}

				fence = trimmed[:length]
			} else if trimmed == "" {
				boundary = offset + len(line)
			}
		} else if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, string(fence[0])) == "" {
			fence = ""

			if strings.HasSuffix(line, "\n") {
				boundary = offset + len(line)
			}
		}

		offset += len(line)
	}

	return text[:boundary], text[boundary:]
}

func agentStreamingRows(text string, width int) []string {
	if text == "" {
		return nil
	}

	return strings.Split(ansi.Wrap(ansi.Strip(strings.TrimRight(text, "\n")), max(20, width-8), ""), "\n")
}
