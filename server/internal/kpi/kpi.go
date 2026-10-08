// Package kpi evaluates derived values such as "{meter-1.kwh} / {line-a.units}".
// The grammar is deliberately tiny and has no function calls or variables other
// than point references, so a stored expression cannot execute code or loop:
//
//	expr   = term { ("+" | "-") term }
//	term   = factor { ("*" | "/") factor }
//	factor = number | "{" device "." point "}" | "(" expr ")" | "-" factor | call
//	cond   = "if" "(" expr cmp expr "," expr "," expr ")"   cmp: > < >= <= == !=  (a call, only inside if)
//	call   = name "(" expr { "," expr } ")"   name: abs round sqrt min max clamp (fixed list)
package kpi

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	MaxLen  = 300
	MaxRefs = 10
	maxDeep = 32
)

type Ref struct{ Device, Point string }

func (r Ref) String() string { return r.Device + "." + r.Point }

type node interface {
	eval(v map[string]float64) (float64, error)
}

type Expr struct {
	root node
	Refs []Ref
}

var ErrDivZero = errors.New("division by zero")

type num float64
type ref Ref
type neg struct{ x node }
type bin struct {
	op   byte
	l, r node
}

func (n num) eval(map[string]float64) (float64, error) { return float64(n), nil }
func (r ref) eval(v map[string]float64) (float64, error) {
	x, ok := v[Ref(r).String()]
	if !ok {
		return 0, fmt.Errorf("no value for %s", Ref(r))
	}
	return x, nil
}
func (n neg) eval(v map[string]float64) (float64, error) {
	x, err := n.x.eval(v)
	return -x, err
}
func (b bin) eval(v map[string]float64) (float64, error) {
	l, err := b.l.eval(v)
	if err != nil {
		return 0, err
	}
	r, err := b.r.eval(v)
	if err != nil {
		return 0, err
	}
	switch b.op {
	case '+':
		return l + r, nil
	case '-':
		return l - r, nil
	case '*':
		return l * r, nil
	}
	if r == 0 {
		return 0, ErrDivZero
	}
	return l / r, nil
}

type call struct {
	name string
	args []node
}

// funcs is the whole function list: pure, total (or erroring) numeric functions. Each entry is the
// accepted argument count and the implementation.
var funcs = map[string]struct {
	n  int
	fn func(a []float64) (float64, error)
}{
	"abs":   {1, func(a []float64) (float64, error) { return math.Abs(a[0]), nil }},
	"round": {1, func(a []float64) (float64, error) { return math.Round(a[0]), nil }},
	"sqrt": {1, func(a []float64) (float64, error) {
		if a[0] < 0 {
			return 0, errors.New("sqrt of a negative number")
		}
		return math.Sqrt(a[0]), nil
	}},
	"min": {2, func(a []float64) (float64, error) { return math.Min(a[0], a[1]), nil }},
	"max": {2, func(a []float64) (float64, error) { return math.Max(a[0], a[1]), nil }},
	"clamp": {3, func(a []float64) (float64, error) {
		if a[1] > a[2] {
			return 0, errors.New("clamp: low is above high")
		}
		return math.Min(math.Max(a[0], a[1]), a[2]), nil
	}},
}

func (c call) eval(v map[string]float64) (float64, error) {
	args := make([]float64, len(c.args))
	for i, a := range c.args {
		x, err := a.eval(v)
		if err != nil {
			return 0, err
		}
		args[i] = x
	}
	return funcs[c.name].fn(args)
}

type cond struct {
	op            string
	l, r, yes, no node
}

func (c cond) eval(v map[string]float64) (float64, error) {
	l, err := c.l.eval(v)
	if err != nil {
		return 0, err
	}
	r, err := c.r.eval(v)
	if err != nil {
		return 0, err
	}
	var t bool
	switch c.op {
	case ">":
		t = l > r
	case "<":
		t = l < r
	case ">=":
		t = l >= r
	case "<=":
		t = l <= r
	case "==":
		t = l == r
	default:
		t = l != r
	}
	if t { // only the chosen branch is evaluated, so the other may divide by zero safely
		return c.yes.eval(v)
	}
	return c.no.eval(v)
}

type parser struct {
	s     string
	i     int
	depth int
	refs  []Ref
	seen  map[string]bool
}

func Parse(src string) (*Expr, error) {
	if len(src) == 0 || len(src) > MaxLen {
		return nil, fmt.Errorf("expression must be 1-%d characters", MaxLen)
	}
	p := &parser{s: src, seen: map[string]bool{}}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("unexpected %q at %d", p.s[p.i:p.i+1], p.i)
	}
	return &Expr{root: n, Refs: p.refs}, nil
}

func (p *parser) ws() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *parser) expr() (node, error) {
	l, err := p.term()
	if err != nil {
		return nil, err
	}
	for {
		p.ws()
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			op := p.s[p.i]
			p.i++
			r, err := p.term()
			if err != nil {
				return nil, err
			}
			l = bin{op, l, r}
			continue
		}
		return l, nil
	}
}

func (p *parser) term() (node, error) {
	l, err := p.factor()
	if err != nil {
		return nil, err
	}
	for {
		p.ws()
		if p.i < len(p.s) && (p.s[p.i] == '*' || p.s[p.i] == '/') {
			op := p.s[p.i]
			p.i++
			r, err := p.factor()
			if err != nil {
				return nil, err
			}
			l = bin{op, l, r}
			continue
		}
		return l, nil
	}
}

func (p *parser) factor() (node, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDeep {
		return nil, errors.New("expression nested too deeply")
	}
	p.ws()
	if p.i >= len(p.s) {
		return nil, errors.New("unexpected end of expression")
	}
	switch c := p.s[p.i]; {
	case c == '-':
		p.i++
		x, err := p.factor()
		return neg{x}, err
	case c == '(':
		p.i++
		n, err := p.expr()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return nil, errors.New("missing )")
		}
		p.i++
		return n, nil
	case c == '{':
		end := strings.IndexByte(p.s[p.i:], '}')
		if end < 0 {
			return nil, errors.New("missing }")
		}
		body := p.s[p.i+1 : p.i+end]
		p.i += end + 1
		dev, pt, ok := strings.Cut(body, ".")
		if !ok || dev == "" || pt == "" || !validIdent(dev) || !validIdent(pt) {
			return nil, fmt.Errorf("bad reference {%s}: use {device.point}", body)
		}
		r := Ref{dev, pt}
		if !p.seen[r.String()] {
			if len(p.refs) >= MaxRefs {
				return nil, fmt.Errorf("at most %d distinct references", MaxRefs)
			}
			p.seen[r.String()] = true
			p.refs = append(p.refs, r)
		}
		return ref(r), nil
	case c >= '0' && c <= '9' || c == '.':
		j := p.i
		for j < len(p.s) && (p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == '.') {
			j++
		}
		f, err := strconv.ParseFloat(p.s[p.i:j], 64)
		if err != nil {
			return nil, fmt.Errorf("bad number %q", p.s[p.i:j])
		}
		p.i = j
		return num(f), nil
	case c >= 'a' && c <= 'z':
		j := p.i
		for j < len(p.s) && p.s[j] >= 'a' && p.s[j] <= 'z' {
			j++
		}
		name := p.s[p.i:j]
		if name == "if" && j < len(p.s) && p.s[j] == '(' {
			p.i = j + 1
			return p.ifCall()
		}
		f, ok := funcs[name]
		if !ok || j >= len(p.s) || p.s[j] != '(' {
			return nil, fmt.Errorf("unknown function or word %q at %d", name, p.i)
		}
		p.i = j + 1
		var args []node
		for {
			a, err := p.expr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			p.ws()
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			break
		}
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return nil, errors.New("missing ) after function arguments")
		}
		p.i++
		if len(args) != f.n {
			return nil, fmt.Errorf("%s takes %d argument(s), got %d", name, f.n, len(args))
		}
		return call{name, args}, nil
	default:
		return nil, fmt.Errorf("unexpected %q at %d", string(c), p.i)
	}
}

func (p *parser) ifCall() (node, error) {
	l, err := p.expr()
	if err != nil {
		return nil, err
	}
	p.ws()
	op := ""
	for _, c := range []string{">=", "<=", "==", "!=", ">", "<"} {
		if strings.HasPrefix(p.s[p.i:], c) {
			op = c
			break
		}
	}
	if op == "" {
		return nil, errors.New("if needs a comparison: > < >= <= == !=")
	}
	p.i += len(op)
	r, err := p.expr()
	if err != nil {
		return nil, err
	}
	var parts [2]node
	for i := range parts {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ',' {
			return nil, errors.New("if takes (condition, then, else)")
		}
		p.i++
		if parts[i], err = p.expr(); err != nil {
			return nil, err
		}
	}
	p.ws()
	if p.i >= len(p.s) || p.s[p.i] != ')' {
		return nil, errors.New("missing ) after if arguments")
	}
	p.i++
	return cond{op, l, r, parts[0], parts[1]}, nil
}

// validIdent allows the characters device and point ids use in practice.
func validIdent(s string) bool {
	if len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':') {
			return false
		}
	}
	return true
}

// Eval computes the expression from latest values keyed "device.point".
func (e *Expr) Eval(values map[string]float64) (float64, error) {
	v, err := e.root.eval(values)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("result is not finite")
	}
	return v, nil
}
