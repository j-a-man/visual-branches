// Command termshot renders colored terminal output as an SVG image of a
// terminal window, for README screenshots that stay crisp at any size.
//
//	CLICOLOR_FORCE=1 COLORTERM=truecolor vb | go run ./tools/termshot -title "vb" > docs/assets/tree.svg
package main

import (
	"bufio"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	fontSize  = 13.0
	lineH     = 19.0
	charW     = 7.82
	padX      = 20.0
	padTop    = 48.0
	padBottom = 18.0
	bg        = "#232136"
	fg        = "#e0def4"
	chrome    = "#2a273f"
)

type style struct {
	fg    string
	bold  bool
	faint bool
}

type span struct {
	text string
	st   style
}

func main() {
	title := flag.String("title", "", "window title")
	minCols := flag.Int("cols", 0, "minimum width in columns")
	flag.Parse()
	lines := parse(os.Stdin)
	cols := *minCols
	for _, l := range lines {
		n := 0
		for _, s := range l {
			n += utf8.RuneCountInString(s.text)
		}
		cols = max(cols, n)
	}
	w := float64(cols)*charW + padX*2
	h := padTop + float64(len(lines))*lineH + padBottom
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`+"\n", w, h, w, h)
	fmt.Fprintf(out, `<rect width="100%%" height="100%%" rx="10" fill="%s"/>`+"\n", bg)
	fmt.Fprintf(out, `<rect width="100%%" height="34" rx="10" fill="%s"/><rect y="24" width="100%%" height="10" fill="%s"/>`+"\n", chrome, chrome)
	for i, c := range []string{"#eb6f92", "#f6c177", "#9ccfd8"} {
		fmt.Fprintf(out, `<circle cx="%d" cy="17" r="6" fill="%s"/>`+"\n", 20+i*20, c)
	}
	if *title != "" {
		fmt.Fprintf(out, `<text x="%.0f" y="21.5" text-anchor="middle" font-family="ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif" font-size="12" fill="#908caa">%s</text>`+"\n", w/2, html.EscapeString(*title))
	}
	fmt.Fprintf(out, `<g font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace" font-size="%.0f" fill="%s" xml:space="preserve" style="white-space:pre">`+"\n", fontSize, fg)
	for i, l := range lines {
		y := padTop + float64(i)*lineH + fontSize
		fmt.Fprintf(out, `<text y="%.1f">`, y)
		col := 0
		for _, s := range l {
			attrs := ""
			if s.st.fg != "" {
				attrs += fmt.Sprintf(` fill="%s"`, s.st.fg)
			}
			if s.st.bold {
				attrs += ` font-weight="700"`
			}
			if s.st.faint {
				attrs += ` opacity="0.6"`
			}
			// Every run of text gets an explicit x from its column, so
			// alignment never depends on how a renderer treats spaces.
			for _, seg := range segments(s.text) {
				x := padX + float64(col+seg.col)*charW
				// textLength pins the run's width, so fonts narrower or wider
				// than 0.6em (Consolas, DejaVu) still line up.
				n := utf8.RuneCountInString(seg.text)
				length := ""
				if n > 1 {
					length = fmt.Sprintf(` textLength="%.1f" lengthAdjust="spacing"`, float64(n)*charW)
				}
				fmt.Fprintf(out, `<tspan x="%.1f"%s%s>%s</tspan>`, x, length, attrs, html.EscapeString(seg.text))
			}
			col += utf8.RuneCountInString(s.text)
		}
		fmt.Fprint(out, "</text>\n")
	}
	fmt.Fprint(out, "</g>\n</svg>\n")
}

type segment struct {
	col  int
	text string
}

// segments splits text at leading spaces and runs of two or more spaces,
// keeping each piece's column offset. Single spaces stay inside a piece.
func segments(text string) []segment {
	var out []segment
	runes := []rune(text)
	i := 0
	for i < len(runes) {
		for i < len(runes) && runes[i] == ' ' {
			i++
		}
		if i >= len(runes) {
			break
		}
		start := i
		for i < len(runes) {
			if runes[i] == ' ' && (i+1 >= len(runes) || runes[i+1] == ' ') {
				break
			}
			i++
		}
		out = append(out, segment{col: start, text: string(runes[start:i])})
	}
	return out
}

// parse splits ANSI-colored text into styled spans per line.
func parse(r io.Reader) [][]span {
	data, err := io.ReadAll(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	var lines [][]span
	var cur style
	for _, raw := range strings.Split(text, "\n") {
		var line []span
		var buf strings.Builder
		flush := func() {
			if buf.Len() > 0 {
				line = append(line, span{text: buf.String(), st: cur})
				buf.Reset()
			}
		}
		for i := 0; i < len(raw); i++ {
			if raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '[' {
				j := i + 2
				for j < len(raw) && (raw[j] < 0x40 || raw[j] > 0x7e) {
					j++
				}
				if j < len(raw) && raw[j] == 'm' {
					flush()
					cur = apply(cur, raw[i+2:j])
				}
				i = j
				continue
			}
			buf.WriteByte(raw[i])
		}
		flush()
		lines = append(lines, line)
	}
	return lines
}

var basic = []string{"#393552", "#eb6f92", "#9ccfd8", "#f6c177", "#3e8fb0", "#c4a7e7", "#9ccfd8", "#e0def4"}

func apply(st style, params string) style {
	if params == "" {
		return style{}
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		switch {
		case n == 0:
			st = style{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.faint = true
		case n == 22:
			st.bold, st.faint = false, false
		case n == 39:
			st.fg = ""
		case n >= 30 && n <= 37:
			st.fg = basic[n-30]
		case n >= 90 && n <= 97:
			st.fg = basic[n-90]
		case n == 38 && i+4 < len(parts) && parts[i+1] == "2":
			r, _ := strconv.Atoi(parts[i+2])
			g, _ := strconv.Atoi(parts[i+3])
			b, _ := strconv.Atoi(parts[i+4])
			st.fg = fmt.Sprintf("#%02x%02x%02x", r, g, b)
			i += 4
		case n == 38 && i+2 < len(parts) && parts[i+1] == "5":
			i += 2
		}
	}
	return st
}
