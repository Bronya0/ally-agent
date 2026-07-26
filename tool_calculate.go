package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func calculateExpression(req CalculateRequest) (CalculateResult, error) {
	expr := strings.TrimSpace(req.Expression)
	if expr == "" {
		return CalculateResult{}, errors.New("expression is required")
	}
	p := &mathParser{s: expr}
	value, err := p.parseExpression()
	if err != nil {
		return CalculateResult{}, err
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		return CalculateResult{}, fmt.Errorf("unexpected token at position %d", p.pos+1)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return CalculateResult{}, errors.New("expression result is not finite")
	}
	return CalculateResult{
		Expression: expr,
		Value:      value,
		Text:       strconv.FormatFloat(value, 'g', -1, 64),
	}, nil
}

type mathParser struct {
	s     string
	pos   int
	depth int
}

func (p *mathParser) parseExpression() (float64, error) {
	return p.parseAddSub()
}

func (p *mathParser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.match('+') {
			right, err := p.parseMulDiv()
			if err != nil {
				return 0, err
			}
			left += right
		} else if p.match('-') {
			right, err := p.parseMulDiv()
			if err != nil {
				return 0, err
			}
			left -= right
		} else {
			return left, nil
		}
	}
}

func (p *mathParser) parseMulDiv() (float64, error) {
	left, err := p.parsePower()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.match('*') {
			right, err := p.parsePower()
			if err != nil {
				return 0, err
			}
			left *= right
		} else if p.match('/') {
			right, err := p.parsePower()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			left /= right
		} else if p.match('%') {
			right, err := p.parsePower()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, errors.New("modulo by zero")
			}
			left = math.Mod(left, right)
		} else {
			return left, nil
		}
	}
}

func (p *mathParser) parsePower() (float64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.match('^') {
		right, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		left = math.Pow(left, right)
	}
	return left, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	p.skipSpace()
	if p.match('+') {
		return p.parseUnary()
	}
	if p.match('-') {
		v, err := p.parseUnary()
		return -v, err
	}
	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (float64, error) {
	p.skipSpace()
	if p.match('(') {
		p.depth++
		if p.depth > 200 {
			return 0, errors.New("expression nesting too deep")
		}
		v, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if !p.match(')') {
			return 0, errors.New("missing closing parenthesis")
		}
		p.depth--
		return v, nil
	}
	if p.pos < len(p.s) && (isAlpha(p.s[p.pos]) || p.s[p.pos] == '_') {
		ident := p.parseIdentifier()
		p.skipSpace()
		if p.match('(') {
			args, err := p.parseArguments()
			if err != nil {
				return 0, err
			}
			return applyMathFunction(ident, args)
		}
		switch strings.ToLower(ident) {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		default:
			return 0, fmt.Errorf("unknown identifier: %s", ident)
		}
	}
	return p.parseNumber()
}

func (p *mathParser) parseArguments() ([]float64, error) {
	var args []float64
	p.skipSpace()
	if p.match(')') {
		return args, nil
	}
	for {
		v, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		p.skipSpace()
		if p.match(')') {
			return args, nil
		}
		if !p.match(',') {
			return nil, errors.New("expected comma or closing parenthesis")
		}
	}
}

func (p *mathParser) parseIdentifier() string {
	start := p.pos
	for p.pos < len(p.s) && (isAlpha(p.s[p.pos]) || isDigit(p.s[p.pos]) || p.s[p.pos] == '_') {
		p.pos++
	}
	return p.s[start:p.pos]
}

func (p *mathParser) parseNumber() (float64, error) {
	start := p.pos
	for p.pos < len(p.s) && (isDigit(p.s[p.pos]) || p.s[p.pos] == '.') {
		p.pos++
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
			p.pos++
		}
		for p.pos < len(p.s) && isDigit(p.s[p.pos]) {
			p.pos++
		}
	}
	if start == p.pos {
		return 0, fmt.Errorf("expected number at position %d", p.pos+1)
	}
	v, err := strconv.ParseFloat(p.s[start:p.pos], 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func (p *mathParser) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}

func (p *mathParser) match(ch byte) bool {
	if p.pos < len(p.s) && p.s[p.pos] == ch {
		p.pos++
		return true
	}
	return false
}

func applyMathFunction(name string, args []float64) (float64, error) {
	name = strings.ToLower(name)
	unary := func(fn func(float64) float64) (float64, error) {
		if len(args) != 1 {
			return 0, fmt.Errorf("%s expects 1 argument", name)
		}
		return fn(args[0]), nil
	}
	switch name {
	case "sqrt":
		return unary(math.Sqrt)
	case "abs":
		return unary(math.Abs)
	case "sin":
		return unary(math.Sin)
	case "cos":
		return unary(math.Cos)
	case "tan":
		return unary(math.Tan)
	case "asin":
		return unary(math.Asin)
	case "acos":
		return unary(math.Acos)
	case "atan":
		return unary(math.Atan)
	case "log", "ln":
		return unary(math.Log)
	case "log10":
		return unary(math.Log10)
	case "exp":
		return unary(math.Exp)
	case "floor":
		return unary(math.Floor)
	case "ceil":
		return unary(math.Ceil)
	case "round":
		return unary(math.Round)
	case "min":
		if len(args) == 0 {
			return 0, errors.New("min expects at least 1 argument")
		}
		v := args[0]
		for _, arg := range args[1:] {
			v = math.Min(v, arg)
		}
		return v, nil
	case "max":
		if len(args) == 0 {
			return 0, errors.New("max expects at least 1 argument")
		}
		v := args[0]
		for _, arg := range args[1:] {
			v = math.Max(v, arg)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("unknown function: %s", name)
	}
}

func isAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
