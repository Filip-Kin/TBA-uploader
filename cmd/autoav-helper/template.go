package main

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultTitleTemplate       = "{video_prefix} {match_level} Match {match_number}{play_suffix}"
	defaultDescriptionTemplate = "{title}\n\nRed Alliance:\n- {red[0].number} {red[0].name}\n- {red[1].number} {red[1].name}\n- {red[2].number} {red[2].name}\n\nBlue Alliance:\n- {blue[0].number} {blue[0].name}\n- {blue[1].number} {blue[1].name}\n- {blue[2].number} {blue[2].name}"
)

// templateContext is everything available to a template render.
type templateContext struct {
	VideoPrefix string
	EventName   string
	EventYear   string
	MatchLevel  string
	MatchNumber int
	MatchLabel  string
	Play        int
	PlaySuffix  string
	Title       string // populated for description template after rendering title
	Alliances   map[string][]allianceTeam
}

var placeholderRe = regexp.MustCompile(`\{([a-zA-Z_]+(?:\[\d+\]\.[a-zA-Z_]+)?)\}`)

// resolvePlaceholder returns (value, ok). ok=false signals "undefined", which
// triggers the line-drop rule for the description template.
func (c *templateContext) resolvePlaceholder(name string) (string, bool) {
	// Array form: red[0].number, blue[2].name
	if i := strings.IndexByte(name, '['); i >= 0 {
		color := name[:i]
		rest := name[i+1:]
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return "", false
		}
		idx, err := strconv.Atoi(rest[:end])
		if err != nil {
			return "", false
		}
		field := rest[end+1:]
		field = strings.TrimPrefix(field, ".")
		teams, ok := c.Alliances[color]
		if !ok || idx < 0 || idx >= len(teams) {
			return "", false
		}
		t := teams[idx]
		switch field {
		case "number":
			if t.Number == 0 {
				return "", true // defined but empty
			}
			return strconv.Itoa(t.Number), true
		case "name":
			return t.Name, true // empty name is defined-as-empty
		}
		return "", false
	}

	switch name {
	case "video_prefix":
		return c.VideoPrefix, c.VideoPrefix != ""
	case "event_name":
		return c.EventName, c.EventName != ""
	case "event_year":
		return c.EventYear, c.EventYear != ""
	case "match_level":
		return c.MatchLevel, c.MatchLevel != ""
	case "match_number":
		return strconv.Itoa(c.MatchNumber), c.MatchNumber != 0
	case "match_label":
		return c.MatchLabel, c.MatchLabel != ""
	case "play":
		return strconv.Itoa(c.Play), c.Play != 0
	case "play_suffix":
		return c.PlaySuffix, true // empty is a defined value here
	case "title":
		return c.Title, c.Title != ""
	}
	return "", false
}

// renderTitle replaces placeholders on a single line; missing values render as
// empty strings (title is always single-line so the line-drop rule doesn't
// apply).
func renderTitle(tmpl string, ctx *templateContext) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[1 : len(match)-1]
		val, _ := ctx.resolvePlaceholder(name)
		return val
	})
}

// renderDescription walks the template line by line. Any line containing a
// placeholder that resolves to undefined is dropped. Lines with no
// placeholders are kept verbatim.
func renderDescription(tmpl string, ctx *templateContext) string {
	lines := strings.Split(tmpl, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered, drop := renderLine(line, ctx)
		if drop {
			continue
		}
		out = append(out, rendered)
	}
	// Collapse runs of blank lines that result from drops, so the output
	// doesn't have weird vertical gaps.
	out = collapseBlankRuns(out)
	return strings.Join(out, "\n")
}

func renderLine(line string, ctx *templateContext) (string, bool) {
	drop := false
	rendered := placeholderRe.ReplaceAllStringFunc(line, func(match string) string {
		name := match[1 : len(match)-1]
		val, ok := ctx.resolvePlaceholder(name)
		if !ok {
			drop = true
		}
		return val
	})
	// Trim trailing whitespace so empty values (e.g. blank team names) don't
	// leave stray spaces on the line.
	rendered = strings.TrimRight(rendered, " \t")
	return rendered, drop
}

func collapseBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, l := range lines {
		blank := strings.TrimSpace(l) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, l)
		prevBlank = blank
	}
	// Strip leading/trailing blank lines.
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// buildTemplateContext folds together the parsed filename, optional meta from
// /api/rename, and the event-level config.
func buildTemplateContext(p parsedFilename, meta *videoMeta, cfg eventConfig) *templateContext {
	ctx := &templateContext{
		VideoPrefix: p.VideoPrefix,
		EventName:   cfg.EventName,
		MatchLevel:  p.Level,
		MatchNumber: p.MatchNumber,
		MatchLabel:  p.matchLabel(),
		Play:        p.Play,
		PlaySuffix:  p.playSuffix(),
		Alliances:   map[string][]allianceTeam{},
	}
	// Event year is the first whitespace-separated token of VideoPrefix if it
	// looks like a 4-digit year. Cheap heuristic, no real parsing needed.
	if i := strings.IndexByte(p.VideoPrefix, ' '); i == 4 {
		if _, err := strconv.Atoi(p.VideoPrefix[:i]); err == nil {
			ctx.EventYear = p.VideoPrefix[:i]
		}
	}
	if meta != nil {
		if meta.MatchLevel != "" {
			ctx.MatchLevel = meta.MatchLevel
		}
		if meta.MatchNumber != 0 {
			ctx.MatchNumber = meta.MatchNumber
		}
		if meta.MatchLabel != "" {
			ctx.MatchLabel = meta.MatchLabel
		}
		if meta.Play != 0 {
			ctx.Play = meta.Play
			ctx.PlaySuffix = ""
			if meta.Play > 1 {
				ctx.PlaySuffix = " Play " + strconv.Itoa(meta.Play)
			}
		}
		for color, teams := range meta.Alliances {
			ctx.Alliances[color] = teams
		}
	}
	return ctx
}
