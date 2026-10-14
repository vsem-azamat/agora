package avatars

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/vsem-azamat/agora/internal/store"
)

// utf8BOM may start an SVG file; it is not text outside the root element.
var utf8BOM = []byte("\xef\xbb\xbf")

const (
	svgNS   = "http://www.w3.org/2000/svg"
	xlinkNS = "http://www.w3.org/1999/xlink"
)

// svgElements are the elements an SVG avatar may hold: shapes, text, grouping and gradients.
// Nothing that runs code, embeds another document, animates or loads anything is on it, and
// nothing that draws other elements again (use, symbol, pattern, mask, clipPath), since nested
// copies let a few bytes make a picture that takes a browser minutes to draw.
var svgElements = set("svg", "g", "defs", "title", "desc", "style",
	"path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan",
	"linearGradient", "radialGradient", "stop")

// svgProperties are the presentation properties a style (the style element, the style
// attribute) may set. A costly property (text-shadow, filter) in one rule would apply to every
// element the rule matches, so a small image could take seconds to draw.
var svgProperties = []string{
	"transform",
	"fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity", "stroke-linecap",
	"stroke-linejoin", "stroke-miterlimit", "stroke-dasharray", "stroke-dashoffset", "opacity", "color",
	"display", "visibility", "clip-rule", "shape-rendering", "vector-effect", "paint-order",
	"stop-color", "stop-opacity",
	"font-family", "font-size", "font-weight", "font-style", "text-anchor", "dominant-baseline", "letter-spacing",
}

// svgAttributes are the attributes an SVG avatar may use: geometry and presentation. Event
// handlers are not on it.
var svgAttributes = set(append([]string{
	"id", "class", "style", "version",
	"d", "x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "fx", "fy", "fr", "dx", "dy",
	"width", "height", "viewBox", "preserveAspectRatio", "points", "pathLength",
	"offset", "gradientUnits", "gradientTransform", "spreadMethod", "href",
}, svgProperties...)...)

var styleProperties = set(svgProperties...)

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// cssFunction finds every function in a value, with the first character of its argument, so a
// url( can be checked to point inside the image.
var cssFunction = regexp.MustCompile(`(?i)([a-z-]*)\s*\(\s*['"]?\s*(.?)`)

// cssSkipped finds, from left to right, unquoted urls, strings and comments, as a browser reads
// them: a comment is read as nothing, so it cannot hide a property, and a url or a string as an
// empty one, so comment marks or quotes in it hide nothing. An unquoted url runs to its `)`, a
// comment left open to the end, a string to the end of its line; escapes are refused.
var cssSkipped = regexp.MustCompile(`(?is)url\(\s*[^\s"')][^)]*\)?|"[^"\r\n\f]*"?|'[^'\r\n\f]*'?|/\*.*?(\*/|$)`)

func skipped(m string) string {
	switch {
	case strings.HasPrefix(m, "/*"):
		return ""
	case strings.HasPrefix(m, `"`), strings.HasPrefix(m, "'"):
		return `""`
	}
	return "url()"
}

// cssProperty finds every name followed by a colon in a style: every property it sets, and
// (refused with them) the element names before a pseudo-class.
var cssProperty = regexp.MustCompile(`([-\w\x{80}-\x{10FFFF}]+)\s*:`)

// svgFunctions are the functions a value may use: colors, transforms, calc, and url(#…).
var svgFunctions = set("url", "rgb", "rgba", "hsl", "hsla", "calc",
	"matrix", "translate", "translatex", "translatey", "scale", "scalex", "scaley", "rotate", "skewx", "skewy")

func refuseSVG(format string, a ...any) error {
	return store.Refuse(store.ErrInvalid, "the SVG is not accepted: "+format, a...)
}

// checkValue refuses a value that uses escapes, a function off the list, or a url( that refers
// to anything outside the image; a style (the style element's text, the style attribute) may
// not use @ rules either, and may set only the presentation properties.
func checkValue(what, v string, style bool) error {
	if strings.Contains(v, `\`) {
		return refuseSVG("%s uses escapes", what)
	}
	if style && strings.Contains(v, "@") {
		return refuseSVG("%s uses @ rules", what)
	}
	if style {
		for _, m := range cssProperty.FindAllStringSubmatch(cssSkipped.ReplaceAllStringFunc(v, skipped), -1) {
			if !styleProperties[strings.ToLower(m[1])] {
				return refuseSVG("%s uses the property %s", what, m[1])
			}
		}
	}
	for _, m := range cssFunction.FindAllStringSubmatch(v, -1) {
		fn := strings.ToLower(m[1])
		switch {
		case !svgFunctions[fn]:
			return refuseSVG("%s uses %s()", what, m[1])
		case fn == "url" && m[2] != "#":
			return refuseSVG("%s refers outside the image", what)
		}
	}
	return nil
}

// sanitize checks an SVG image against the allowlist and writes it anew from what it checked:
// the elements with their allowed attributes and text, without comments, the XML declaration or
// namespace declarations of its own.
func sanitize(data []byte) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, utf8BOM)))
	dec.Strict = true
	dec.Entity = nil // only the five predefined entities
	var out bytes.Buffer
	var open []string
	var style strings.Builder // the text of the style element, checked as a whole at its end
	root := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, refuseSVG("it is not well-formed XML: %v", err)
		}
		switch t := tok.(type) {
		case xml.ProcInst:
			if t.Target != "xml" || root {
				return nil, refuseSVG("processing instructions are not allowed")
			}
		case xml.Directive:
			return nil, refuseSVG("document type declarations are not allowed")
		case xml.Comment:
		case xml.StartElement:
			if root && len(open) == 0 {
				return nil, refuseSVG("there is more than one root element")
			}
			if t.Name.Space != svgNS && t.Name.Space != "" {
				return nil, refuseSVG("the element %s is not SVG", t.Name.Local)
			}
			name := t.Name.Local
			if !svgElements[name] {
				return nil, refuseSVG("the element %s is not allowed", name)
			}
			if len(open) > 0 && open[len(open)-1] == "style" {
				return nil, refuseSVG("the style element holds an element")
			}
			if !root && name != "svg" {
				return nil, refuseSVG("the root element is %s, not svg", name)
			}
			out.WriteString("<" + name)
			if !root {
				out.WriteString(` xmlns="` + svgNS + `"`)
				root = true
			}
			if err := writeAttrs(&out, name, t.Attr); err != nil {
				return nil, err
			}
			out.WriteString(">")
			open = append(open, name)
		case xml.EndElement:
			if t.Name.Local == "style" {
				if err := checkValue("the style element", style.String(), true); err != nil {
					return nil, err
				}
				style.Reset()
			}
			out.WriteString("</" + t.Name.Local + ">")
			open = open[:len(open)-1]
		case xml.CharData:
			if len(open) == 0 {
				if len(bytes.TrimSpace(t)) > 0 {
					return nil, refuseSVG("there is text outside the root element")
				}
				continue
			}
			if open[len(open)-1] == "style" {
				style.Write(t)
			}
			out.WriteString(textEscaper.Replace(string(t)))
		}
	}
	if !root {
		return nil, refuseSVG("there is no svg element")
	}
	return out.Bytes(), nil
}

// writeAttrs writes an element's allowed attributes; namespace declarations are dropped (the
// root gets the SVG one) and xlink:href is written as href.
func writeAttrs(out *bytes.Buffer, element string, attrs []xml.Attr) error {
	written := map[string]bool{}
	for _, a := range attrs {
		name := a.Name.Local
		switch {
		case a.Name.Space == "xmlns" || (a.Name.Space == "" && name == "xmlns"):
			if a.Value != svgNS && a.Value != xlinkNS {
				return refuseSVG("the namespace %s is not allowed", a.Value)
			}
			continue
		case a.Name.Space == xlinkNS && name == "href":
		case a.Name.Space != "" || !svgAttributes[name]:
			return refuseSVG("the attribute %s on %s is not allowed", qualified(a.Name), element)
		}
		if written[name] {
			return refuseSVG("the attribute %s is given twice on %s", name, element)
		}
		written[name] = true
		if name == "href" && !strings.HasPrefix(a.Value, "#") {
			return refuseSVG("the href on %s refers outside the image", element)
		}
		if err := checkValue("the attribute "+name+" on "+element, a.Value, name == "style"); err != nil {
			return err
		}
		out.WriteString(" " + name + `="` + attrEscaper.Replace(a.Value) + `"`)
	}
	return nil
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
)

func qualified(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}
