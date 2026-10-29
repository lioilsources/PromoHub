package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Grammar of a `when` expression:
//
//	expr    := and ("or" and)*
//	and     := unary ("and" unary)*
//	unary   := "not" unary | "(" expr ")" | cond
//	cond    := ident op literal
//	         | ident "matches" /regex/flags
//	         | ident "contains" "string"
//	         | ident "crosses" [num, ...]
//	op      := < <= > >= == !=
//
// `||`, `&&` and `!` are accepted as aliases. A condition on a missing variable is false.

// Vars is what an expression is evaluated against. Prev holds the previous value of numeric
// variables and is consulted only by `crosses`.
type Vars struct {
	Num  map[string]float64
	Str  map[string]string
	Prev map[string]float64
}

type node interface{ eval(v Vars) bool }

type orNode struct{ l, r node }
type andNode struct{ l, r node }
type notNode struct{ x node }

func (n orNode) eval(v Vars) bool  { return n.l.eval(v) || n.r.eval(v) }
func (n andNode) eval(v Vars) bool { return n.l.eval(v) && n.r.eval(v) }
func (n notNode) eval(v Vars) bool { return !n.x.eval(v) }

type trueNode struct{}

func (trueNode) eval(Vars) bool { return true }

type cmpNode struct {
	ident string
	op    string
	num   *float64
	str   *string
}

func (n cmpNode) eval(v Vars) bool {
	if n.num != nil {
		x, ok := v.Num[n.ident]
		if !ok {
			return false
		}
		y := *n.num
		switch n.op {
		case "<":
			return x < y
		case "<=":
			return x <= y
		case ">":
			return x > y
		case ">=":
			return x >= y
		case "==":
			return x == y
		case "!=":
			return x != y
		}
		return false
	}
	x, ok := v.Str[n.ident]
	if !ok {
		return false
	}
	switch n.op {
	case "==":
		return strings.EqualFold(x, *n.str)
	case "!=":
		return !strings.EqualFold(x, *n.str)
	}
	return false
}

type matchNode struct {
	ident string
	re    *regexp.Regexp
}

func (n matchNode) eval(v Vars) bool {
	s, ok := v.Str[n.ident]
	return ok && n.re.MatchString(s)
}

type containsNode struct{ ident, sub string }

func (n containsNode) eval(v Vars) bool {
	s, ok := v.Str[n.ident]
	return ok && strings.Contains(strings.ToLower(s), strings.ToLower(n.sub))
}

// crossesNode is true when the value moved up past any threshold since the previous snapshot.
// Without a previous value it is false, so the first snapshot never fires a milestone.
type crossesNode struct {
	ident      string
	thresholds []float64
}

func (n crossesNode) eval(v Vars) bool {
	cur, ok := v.Num[n.ident]
	if !ok {
		return false
	}
	prev, ok := v.Prev[n.ident]
	if !ok {
		return false
	}
	for _, t := range n.thresholds {
		if prev < t && cur >= t {
			return true
		}
	}
	return false
}

// Compile parses an expression. An empty expression always matches.
func Compile(src string) (node, error) {
	if strings.TrimSpace(src) == "" {
		return trueNode{}, nil
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	if !p.done() {
		return nil, fmt.Errorf("unexpected %q", p.peek().text)
	}
	return n, nil
}

type tokKind int

const (
	tIdent tokKind = iota
	tNum
	tStr
	tRegex
	tOp
	tLParen
	tRParen
	tLBrack
	tRBrack
	tComma
	tEOF
)

type token struct {
	kind  tokKind
	text  string
	flags string // regex flags
}

func lex(s string) ([]token, error) {
	var out []token
	r := []rune(s)
	// A '/' starts a regex only right after `matches`; elsewhere it is not valid.
	lastIdent := func() string {
		if len(out) == 0 || out[len(out)-1].kind != tIdent {
			return ""
		}
		return out[len(out)-1].text
	}
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(':
			out = append(out, token{kind: tLParen, text: "("})
			i++
		case c == ')':
			out = append(out, token{kind: tRParen, text: ")"})
			i++
		case c == '[':
			out = append(out, token{kind: tLBrack, text: "["})
			i++
		case c == ']':
			out = append(out, token{kind: tRBrack, text: "]"})
			i++
		case c == ',':
			out = append(out, token{kind: tComma, text: ","})
			i++
		case c == '"' || c == '\'':
			j := i + 1
			var b strings.Builder
			for ; j < len(r) && r[j] != c; j++ {
				if r[j] == '\\' && j+1 < len(r) {
					j++
				}
				b.WriteRune(r[j])
			}
			if j >= len(r) {
				return nil, fmt.Errorf("unterminated string")
			}
			out = append(out, token{kind: tStr, text: b.String()})
			i = j + 1
		case c == '/' && lastIdent() == "matches":
			j := i + 1
			var b strings.Builder
			for ; j < len(r) && r[j] != '/'; j++ {
				if r[j] == '\\' && j+1 < len(r) && r[j+1] == '/' {
					j++
				}
				b.WriteRune(r[j])
			}
			if j >= len(r) {
				return nil, fmt.Errorf("unterminated regex")
			}
			j++
			k := j
			for k < len(r) && unicode.IsLetter(r[k]) {
				k++
			}
			out = append(out, token{kind: tRegex, text: b.String(), flags: string(r[j:k])})
			i = k
		case strings.ContainsRune("<>=!&|", c):
			j := i + 1
			if j < len(r) && strings.ContainsRune("=&|", r[j]) {
				j++
			}
			op := string(r[i:j])
			switch op {
			case "&&":
				out = append(out, token{kind: tIdent, text: "and"})
			case "||":
				out = append(out, token{kind: tIdent, text: "or"})
			case "!":
				out = append(out, token{kind: tIdent, text: "not"})
			case "<", "<=", ">", ">=", "==", "!=":
				out = append(out, token{kind: tOp, text: op})
			case "=":
				out = append(out, token{kind: tOp, text: "=="})
			default:
				return nil, fmt.Errorf("bad operator %q", op)
			}
			i = j
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(r) && unicode.IsDigit(r[i+1])):
			j := i + 1
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.' || r[j] == '_') {
				j++
			}
			out = append(out, token{kind: tNum, text: strings.ReplaceAll(string(r[i:j]), "_", "")})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i + 1
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_' || r[j] == '.') {
				j++
			}
			out = append(out, token{kind: tIdent, text: strings.ToLower(string(r[i:j]))})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return out, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) done() bool { return p.pos >= len(p.toks) }

func (p *parser) peek() token {
	if p.done() {
		return token{kind: tEOF, text: "<end>"}
	}
	return p.toks[p.pos]
}

func (p *parser) next() token {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) isWord(w string) bool {
	t := p.peek()
	return !p.done() && t.kind == tIdent && t.text == w
}

func (p *parser) expr() (node, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.isWord("or") {
		p.next()
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = orNode{l, r}
	}
	return l, nil
}

func (p *parser) and() (node, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.isWord("and") {
		p.next()
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		l = andNode{l, r}
	}
	return l, nil
}

func (p *parser) unary() (node, error) {
	if p.isWord("not") {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return notNode{x}, nil
	}
	if p.peek().kind == tLParen && !p.done() {
		p.next()
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		if t := p.next(); t.kind != tRParen {
			return nil, fmt.Errorf("expected ')', got %q", t.text)
		}
		return x, nil
	}
	return p.cond()
}

func (p *parser) cond() (node, error) {
	id := p.next()
	if id.kind != tIdent {
		return nil, fmt.Errorf("expected variable, got %q", id.text)
	}
	op := p.next()
	switch {
	case op.kind == tOp:
		lit := p.next()
		switch lit.kind {
		case tNum:
			f, err := strconv.ParseFloat(lit.text, 64)
			if err != nil {
				return nil, fmt.Errorf("bad number %q", lit.text)
			}
			return cmpNode{ident: id.text, op: op.text, num: &f}, nil
		case tStr:
			if op.text != "==" && op.text != "!=" {
				return nil, fmt.Errorf("operator %s needs a number", op.text)
			}
			s := lit.text
			return cmpNode{ident: id.text, op: op.text, str: &s}, nil
		}
		return nil, fmt.Errorf("expected number or string after %s, got %q", op.text, lit.text)
	case op.kind == tIdent && op.text == "matches":
		lit := p.next()
		if lit.kind != tRegex && lit.kind != tStr {
			return nil, fmt.Errorf("matches needs /regex/")
		}
		pat := lit.text
		for _, f := range lit.flags {
			switch f {
			case 'i', 's', 'm':
				pat = "(?" + string(f) + ")" + pat
			default:
				return nil, fmt.Errorf("unsupported regex flag %q", f)
			}
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, err
		}
		return matchNode{ident: id.text, re: re}, nil
	case op.kind == tIdent && op.text == "contains":
		lit := p.next()
		if lit.kind != tStr {
			return nil, fmt.Errorf("contains needs a string")
		}
		return containsNode{ident: id.text, sub: lit.text}, nil
	case op.kind == tIdent && op.text == "crosses":
		if t := p.next(); t.kind != tLBrack {
			return nil, fmt.Errorf("crosses needs [n, ...]")
		}
		var th []float64
		for {
			t := p.next()
			if t.kind == tRBrack && len(th) > 0 {
				break
			}
			if t.kind != tNum {
				return nil, fmt.Errorf("crosses: expected number, got %q", t.text)
			}
			f, err := strconv.ParseFloat(t.text, 64)
			if err != nil {
				return nil, err
			}
			th = append(th, f)
			if p.peek().kind == tComma {
				p.next()
			}
		}
		return crossesNode{ident: id.text, thresholds: th}, nil
	}
	return nil, fmt.Errorf("expected operator after %q, got %q", id.text, op.text)
}
