// SPDX-License-Identifier: GPL-3.0-only
package calculate

import (
	"errors"
	"math/big"
	"reflect"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/vm/runtime"
)

// Patch before optimization so constant folding cannot hide an overflow.
type checkedArithmeticPatcher struct{}

func (checkedArithmeticPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.BinaryNode:
		if n.Operator == "+" || n.Operator == "-" || n.Operator == "*" {
			ast.Patch(node, arithmeticCall(n.Operator, n.Left, n.Right))
		}
	case *ast.UnaryNode:
		if n.Operator == "-" {
			ast.Patch(node, arithmeticCall("-", &ast.IntegerNode{Value: 0}, n.Node))
		}
	case *ast.BuiltinNode:
		switch n.Name {
		case "abs":
			ast.Patch(node, &ast.CallNode{Callee: &ast.IdentifierNode{Value: "__ally_abs"}, Arguments: n.Arguments})
		case "sum":
			if len(n.Arguments) < 1 || len(n.Arguments) > 2 {
				return // Let the expression checker report invalid arguments.
			}
			// sum's internal accumulator otherwise bypasses checked addition.
			collection := n.Arguments[0]
			if len(n.Arguments) == 2 {
				collection = &ast.BuiltinNode{Name: "map", Arguments: n.Arguments}
			}
			ast.Patch(node, &ast.BuiltinNode{Name: "reduce", Arguments: []ast.Node{
				collection,
				&ast.PredicateNode{Node: arithmeticCall("+", &ast.PointerNode{Name: "acc"}, &ast.PointerNode{})},
				&ast.IntegerNode{Value: 0},
			}})
		}
	}
}

func arithmeticCall(op string, left, right ast.Node) ast.Node {
	return &ast.CallNode{Callee: &ast.IdentifierNode{Value: "__ally_arithmetic"}, Arguments: []ast.Node{
		&ast.StringNode{Value: op}, left, right,
	}}
}

func checkedArithmetic(args ...any) (any, error) {
	if len(args) != 3 {
		return nil, errors.New("invalid arithmetic arguments")
	}
	var result any
	switch args[0] {
	case "+":
		result = runtime.Add(args[1], args[2])
	case "-":
		result = runtime.Subtract(args[1], args[2])
	case "*":
		result = runtime.Multiply(args[1], args[2])
	default:
		return nil, errors.New("invalid arithmetic operator")
	}
	left, right := exactInteger(args[1]), exactInteger(args[2])
	if left != nil && right != nil {
		switch args[0] {
		case "+":
			left.Add(left, right)
		case "-":
			left.Sub(left, right)
		case "*":
			left.Mul(left, right)
		}
		actual := exactInteger(result)
		if actual == nil || left.Cmp(actual) != 0 {
			return nil, errors.New("integer overflow: result exceeds the supported integer range; use an arbitrary-precision calculator for exact large integers")
		}
	}
	return result, nil
}

func checkedAbs(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, errors.New("abs expects one argument")
	}
	if integer := exactInteger(args[0]); integer != nil && integer.Sign() < 0 {
		return checkedArithmetic("-", 0, args[0])
	}
	return builtin.Abs(args[0]), nil
}

func exactInteger(value any) *big.Int {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return big.NewInt(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return new(big.Int).SetUint64(v.Uint())
	}
	return nil
}
