package http

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestEveryClientSelectedSubscriptionCaseIsGated inspects the real read-loop
// switch. A future case that calls Redis Subscribe must also be classified by
// isScopedRoomFrame; otherwise beta clients could select an arbitrary room.
func TestEveryClientSelectedSubscriptionCaseIsGated(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundSubscribeCase := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok || !containsRedisSubscribe(clause) {
			return true
		}
		foundSubscribeCase = true
		for _, expression := range clause.List {
			literal, ok := expression.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Errorf("client-selected Redis subscription has a non-literal case")
				continue
			}
			messageType, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Errorf("decode subscription case %s: %v", literal.Value, err)
				continue
			}
			if !isScopedRoomFrame(messageType) {
				t.Errorf("client-selected Redis subscription %q bypasses the beta entitlement gate", messageType)
			}
		}
		return true
	})
	if !foundSubscribeCase {
		t.Fatal("source guard found no client-selected Redis subscription cases; parser or read loop changed")
	}
}

func containsRedisSubscribe(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Subscribe" {
			found = true
			return false
		}
		return true
	})
	return found
}
