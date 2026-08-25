package http

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// entitledSubscribeFrames are the frames whose Redis Subscribe is authorized
// by an OWNER-ISSUED entitlement (chat-shared/roomauth) rather than the
// EnableScopedRooms beta gate: message-service validates active membership
// and signs the room token; the gateway only verifies. Every other
// client-selected subscribe must still be classified by isScopedRoomFrame.
var entitledSubscribeFrames = map[string]bool{
	"conversation.subscribe": true,
}

// TestEveryClientSelectedSubscriptionCaseIsGated inspects the real read-loop
// switch. A future case that calls Redis Subscribe must be either classified
// by isScopedRoomFrame (rejected in beta) or listed as an ENTITLED frame
// whose case verifiably calls roomauth.Verify; otherwise clients could
// select an arbitrary room.
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
			if entitledSubscribeFrames[messageType] {
				if !containsRoomauthVerify(clause) {
					t.Errorf("entitled subscription %q does not verify its entitlement", messageType)
				}
				if !containsDenyMarkerCheck(clause) {
					t.Errorf("entitled subscription %q does not consult the revocation deny marker (P0-4: "+
						"a removed member could replay a still-valid token until expiry)", messageType)
				}
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

// containsRoomauthVerify reports whether the clause calls roomauth.Verify —
// the load-bearing check for an entitled subscription.
func containsRoomauthVerify(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Verify" {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "roomauth" {
			found = true
			return false
		}
		return true
	})
	return found
}

// containsDenyMarkerCheck reports whether the clause consults
// roomauth.DenyKey — the durable revocation lookup that closes token replay
// after removal (P0-4).
func containsDenyMarkerCheck(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "DenyKey" {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "roomauth" {
			found = true
			return false
		}
		return true
	})
	return found
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
