package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A small reader of GraphQL documents, enough for the shop of the kind of Ownfoil that Sphaira
// reads (see netownfoil.go): one query, its variables, fields with aliases, arguments and
// selections. Fragments, directives and mutations are refused.

// gqlField is a field asked for: its name, the key it is answered under (its alias, if any), its
// arguments with the variables put in, and the fields asked of it.
type gqlField struct {
	alias string
	name  string
	args  map[string]any
	sel   []gqlField
}

func (f gqlField) key() string {
	if f.alias != "" {
		return f.alias
	}
	return f.name
}

// gqlEnum is a value written as a name, such as BASE or DESC.
type gqlEnum string

const (
	// the longest document read, and how deep its selections and values go
	gqlMaxQuery = 64 << 10
	gqlMaxDepth = 32
)

type gqlParser struct {
	src       string
	pos       int
	depth     int
	variables map[string]any
}

// parseGraphql reads a query and gives the fields asked at its top.
func parseGraphql(query string, variables map[string]any) ([]gqlField, error) {
	if len(query) > gqlMaxQuery {
		return nil, errors.New("the query is too long")
	}
	p := &gqlParser{src: query, variables: variables}
	p.skip()
	if !p.peek('{') {
		switch name := p.name(); name {
		case "query":
		case "":
			return nil, p.fail("a query was expected")
		default:
			return nil, fmt.Errorf("only queries are answered, not %s", name)
		}
		p.skip()
		// the name of the operation
		if !p.peek('(') && !p.peek('{') {
			p.name()
			p.skip()
		}
		// the variables it declares: their values come with the request
		if p.peek('(') {
			if err := p.skipParens(); err != nil {
				return nil, err
			}
			p.skip()
		}
	}
	fields, err := p.selection()
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.pos < len(p.src) {
		return nil, p.fail("one operation only")
	}
	return fields, nil
}

func (p *gqlParser) fail(message string) error {
	return fmt.Errorf("%s (at %d)", message, p.pos)
}

// skip passes the spaces, the commas and the comments.
func (p *gqlParser) skip() {
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			p.pos++
		case c == '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *gqlParser) peek(c byte) bool {
	return p.pos < len(p.src) && p.src[p.pos] == c
}

func (p *gqlParser) expect(c byte) error {
	p.skip()
	if !p.peek(c) {
		return p.fail(fmt.Sprintf("%q was expected", c))
	}
	p.pos++
	return nil
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (p *gqlParser) name() string {
	start := p.pos
	if p.pos < len(p.src) && isNameStart(p.src[p.pos]) {
		p.pos++
		for p.pos < len(p.src) && (isNameStart(p.src[p.pos]) || (p.src[p.pos] >= '0' && p.src[p.pos] <= '9')) {
			p.pos++
		}
	}
	return p.src[start:p.pos]
}

// skipParens passes the declarations of the variables, strings in them included.
func (p *gqlParser) skipParens() error {
	open := 0
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '(':
			open++
		case ')':
			open--
			if open == 0 {
				p.pos++
				return nil
			}
		case '"':
			if _, err := p.str(); err != nil {
				return err
			}
			continue
		}
		p.pos++
	}
	return p.fail("the variables are not closed")
}

func (p *gqlParser) selection() ([]gqlField, error) {
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	if p.depth++; p.depth > gqlMaxDepth {
		return nil, p.fail("the query goes too deep")
	}
	defer func() { p.depth-- }()
	fields := []gqlField{}
	for {
		p.skip()
		if p.peek('}') {
			p.pos++
			if len(fields) == 0 {
				return nil, p.fail("a selection asks for no field")
			}
			return fields, nil
		}
		if strings.HasPrefix(p.src[p.pos:], "...") {
			return nil, p.fail("fragments are not answered")
		}
		field := gqlField{name: p.name()}
		if field.name == "" {
			return nil, p.fail("a field was expected")
		}
		p.skip()
		if p.peek(':') {
			p.pos++
			p.skip()
			field.alias, field.name = field.name, p.name()
			if field.name == "" {
				return nil, p.fail("a field was expected after its alias")
			}
			p.skip()
		}
		if p.peek('(') {
			p.pos++
			field.args = map[string]any{}
			for {
				p.skip()
				if p.peek(')') {
					p.pos++
					break
				}
				name := p.name()
				if name == "" {
					return nil, p.fail("an argument was expected")
				}
				if err := p.expect(':'); err != nil {
					return nil, err
				}
				value, err := p.value()
				if err != nil {
					return nil, err
				}
				field.args[name] = value
			}
			p.skip()
		}
		if p.peek('@') {
			return nil, p.fail("directives are not answered")
		}
		if p.peek('{') {
			sel, err := p.selection()
			if err != nil {
				return nil, err
			}
			field.sel = sel
		}
		fields = append(fields, field)
	}
}

// value reads a value: a variable, a string, a number, a list, an object, true, false, null or
// a name (an enum).
func (p *gqlParser) value() (any, error) {
	p.skip()
	if p.pos >= len(p.src) {
		return nil, p.fail("a value was expected")
	}
	if p.depth++; p.depth > gqlMaxDepth {
		return nil, p.fail("the query goes too deep")
	}
	defer func() { p.depth-- }()
	switch c := p.src[p.pos]; {
	case c == '$':
		p.pos++
		return p.variables[p.name()], nil
	case c == '"':
		return p.str()
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.pos
		p.pos++
		for p.pos < len(p.src) && strings.IndexByte("0123456789.eE+-", p.src[p.pos]) >= 0 {
			p.pos++
		}
		text := p.src[start:p.pos]
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, nil
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, p.fail("a number was expected")
		}
		return f, nil
	case c == '[':
		p.pos++
		list := []any{}
		for {
			p.skip()
			if p.peek(']') {
				p.pos++
				return list, nil
			}
			value, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
	case c == '{':
		p.pos++
		object := map[string]any{}
		for {
			p.skip()
			if p.peek('}') {
				p.pos++
				return object, nil
			}
			name := p.name()
			if name == "" {
				return nil, p.fail("a field of the object was expected")
			}
			if err := p.expect(':'); err != nil {
				return nil, err
			}
			value, err := p.value()
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
	case isNameStart(c):
		switch name := p.name(); name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		default:
			return gqlEnum(name), nil
		}
	}
	return nil, p.fail("a value was expected")
}

// str reads a string between quotes, with the escapes of JSON, which are those of GraphQL.
func (p *gqlParser) str() (string, error) {
	start := p.pos
	p.pos++
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '\\':
			p.pos += 2
			continue
		case '"':
			p.pos++
			var out string
			if err := json.Unmarshal([]byte(p.src[start:p.pos]), &out); err != nil {
				return "", p.fail("a string is not written well")
			}
			return out, nil
		case '\n':
			return "", p.fail("a string is not closed")
		}
		p.pos++
	}
	return "", p.fail("a string is not closed")
}

// gqlInt is an argument that is a number, or def.
func gqlInt(value any, def int64) int64 {
	switch n := value.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	return def
}

// gqlString is an argument that is a string or an enum.
func gqlString(value any) string {
	switch s := value.(type) {
	case string:
		return s
	case gqlEnum:
		return string(s)
	case json.Number:
		return s.String()
	}
	return ""
}

// gqlStrings is an argument that is a list of strings or enums; one alone counts as a list.
func gqlStrings(value any) ([]string, bool) {
	switch list := value.(type) {
	case nil:
		return nil, false
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, gqlString(item))
		}
		return out, true
	}
	return []string{gqlString(value)}, true
}
