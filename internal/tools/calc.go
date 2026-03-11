package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type CalcTool struct{}

func NewCalcTool() *CalcTool { return &CalcTool{} }

func (t *CalcTool) Name() string { return "calc" }

func (t *CalcTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx
	raw, _ := pickString(args, "expr", "input", "text")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	v, err := evalExpr(raw)
	if err != nil {
		return nil, err
	}
	return strconv.FormatFloat(v, 'g', -1, 64), nil
}

func pickString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			return x, true
		default:
			return fmt.Sprint(x), true
		}
	}
	return "", false
}

type tokenKind int

const (
	tokNum tokenKind = iota
	tokOp
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
	num  float64
}

func evalExpr(s string) (float64, error) {
	toks, err := tokenize(s)
	if err != nil {
		return 0, err
	}
	rpn, err := toRPN(toks)
	if err != nil {
		return 0, err
	}
	return evalRPN(rpn)
}

func tokenize(s string) ([]token, error) {
	var out []token
	i := 0
	expectValue := true

	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}

		switch c {
		case '(':
			out = append(out, token{kind: tokLParen, text: "("})
			i++
			expectValue = true
			continue
		case ')':
			out = append(out, token{kind: tokRParen, text: ")"})
			i++
			expectValue = false
			continue
		case '+', '-', '*', '/':
			op := string(c)
			if expectValue {
				if c == '+' {
					i++
					continue
				}
				if c == '-' {
					out = append(out, token{kind: tokNum, text: "0", num: 0})
					out = append(out, token{kind: tokOp, text: "-"})
					i++
					expectValue = true
					continue
				}
				return nil, fmt.Errorf("unexpected operator %q", op)
			}
			out = append(out, token{kind: tokOp, text: op})
			i++
			expectValue = true
			continue
		default:
		}

		if (c >= '0' && c <= '9') || c == '.' {
			start := i
			dot := 0
			for i < len(s) {
				cc := s[i]
				if cc == '.' {
					dot++
					if dot > 1 {
						break
					}
					i++
					continue
				}
				if cc >= '0' && cc <= '9' {
					i++
					continue
				}
				break
			}
			txt := s[start:i]
			v, err := strconv.ParseFloat(txt, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", txt)
			}
			out = append(out, token{kind: tokNum, text: txt, num: v})
			expectValue = false
			continue
		}

		return nil, fmt.Errorf("invalid character %q", string(c))
	}

	if expectValue && len(out) > 0 {
		last := out[len(out)-1]
		if last.kind == tokOp {
			return nil, fmt.Errorf("trailing operator %q", last.text)
		}
	}

	return out, nil
}

func precedence(op string) int {
	switch op {
	case "*", "/":
		return 2
	case "+", "-":
		return 1
	default:
		return 0
	}
}

func toRPN(toks []token) ([]token, error) {
	var out []token
	var stack []token

	for _, t := range toks {
		switch t.kind {
		case tokNum:
			out = append(out, t)
		case tokOp:
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.kind != tokOp {
					break
				}
				if precedence(top.text) >= precedence(t.text) {
					out = append(out, top)
					stack = stack[:len(stack)-1]
					continue
				}
				break
			}
			stack = append(stack, t)
		case tokLParen:
			stack = append(stack, t)
		case tokRParen:
			found := false
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.kind == tokLParen {
					found = true
					break
				}
				out = append(out, top)
			}
			if !found {
				return nil, fmt.Errorf("mismatched parentheses")
			}
		default:
			return nil, fmt.Errorf("unknown token")
		}
	}

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.kind == tokLParen || top.kind == tokRParen {
			return nil, fmt.Errorf("mismatched parentheses")
		}
		out = append(out, top)
	}

	return out, nil
}

func evalRPN(rpn []token) (float64, error) {
	var st []float64
	for _, t := range rpn {
		switch t.kind {
		case tokNum:
			st = append(st, t.num)
		case tokOp:
			if len(st) < 2 {
				return 0, fmt.Errorf("invalid expression")
			}
			b := st[len(st)-1]
			a := st[len(st)-2]
			st = st[:len(st)-2]
			var v float64
			switch t.text {
			case "+":
				v = a + b
			case "-":
				v = a - b
			case "*":
				v = a * b
			case "/":
				if b == 0 {
					return 0, fmt.Errorf("division by zero")
				}
				v = a / b
			default:
				return 0, fmt.Errorf("unknown operator %q", t.text)
			}
			st = append(st, v)
		default:
			return 0, fmt.Errorf("invalid expression")
		}
	}
	if len(st) != 1 {
		return 0, fmt.Errorf("invalid expression")
	}
	return st[0], nil
}
