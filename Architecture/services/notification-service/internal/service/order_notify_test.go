package service

import (
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestOrderTypesAreRegisteredInTheOrdersCategory(t *testing.T) {
	for _, ty := range OrderNotificationTypes {
		tpl, ok := Templates[ty]
		if !ok {
			t.Errorf("%s not in Templates", ty)
			continue
		}
		if !tpl.PushEligible || tpl.TitleTemplate == "" || tpl.BodyTemplate == "" {
			t.Errorf("%s template incomplete: %+v", ty, tpl)
		}
		if categoryForEvent(ty) != catOrders {
			t.Errorf("%s is not in the orders category", ty)
		}
	}
}

func TestOrdersPreferencesGateOrderNotices(t *testing.T) {
	prefs := postgres.DefaultNotificationPreferences("u")
	if !prefs.PushOrders || !prefs.InappOrders {
		t.Fatal("orders toggles must default on")
	}
	for _, ty := range OrderNotificationTypes {
		d := resolveDecision(ty, prefs, false)
		if !d.CreateInbox || !d.SendPush {
			t.Errorf("%s with defaults: %+v", ty, d)
		}
	}
	off := postgres.DefaultNotificationPreferences("u")
	off.PushOrders = false
	if d := resolveDecision(OrderTypeShipped, off, false); d.SendPush || !d.CreateInbox {
		t.Errorf("push_orders=false: %+v, want inbox only", d)
	}
	off = postgres.DefaultNotificationPreferences("u")
	off.InappOrders = false
	if d := resolveDecision(OrderTypeShipped, off, false); d.CreateInbox || !d.SendPush {
		t.Errorf("inapp_orders=false: %+v, want push only", d)
	}
	// The Feast toggle does not silence MStore.
	food := postgres.DefaultNotificationPreferences("u")
	food.PushFoodOrders = false
	if d := resolveDecision(OrderTypeShipped, food, false); !d.SendPush {
		t.Error("push_food_orders=false silenced an MStore order notice")
	}
}

func TestRenderOrderNotification(t *testing.T) {
	order, buyer := uuid.New(), uuid.New()
	n := OrderNotification{RecipientID: buyer, Type: OrderTypePaymentFailed, OrderID: order,
		OrderNumber: "ORD-2026-010005", CreatedAt: time.Now()}
	r := RenderOrderNotification(n)
	if r.Title != "Payment failed, try again" ||
		r.Body != "Payment for order #ORD-2026-010005 didn't go through. Tap to try again." {
		t.Fatalf("render = %+v", r)
	}
	if r.CollapseKey != "order:"+order.String()+":"+buyer.String() {
		t.Fatalf("collapse key %q", r.CollapseKey)
	}
	n.Type, n.OrderNumber = OrderTypeSellerNewOrder, ""
	if got := RenderOrderNotification(n).Title; got != "New order" {
		t.Fatalf("seller title without a number = %q", got)
	}
	if n.Identity() != order.String()+":"+buyer.String()+":"+OrderTypeSellerNewOrder {
		t.Fatalf("identity %q", n.Identity())
	}
}

func TestBuildPushDataCarriesTheContractKeys(t *testing.T) {
	user, order := uuid.New(), uuid.New()
	n := OrderNotification{RecipientID: user, Type: OrderTypeDelivered, OrderID: order, OrderNumber: "ORD-1"}
	title, body, data := buildPushData(user, n.Type, OrderEntityType, order, "/shop/orders/"+order.String(),
		RenderOrderNotification(n))
	want := map[string]string{
		"type": OrderTypeDelivered, "entity_type": "order", "entity_id": order.String(),
		"deep_link": "/shop/orders/" + order.String(), "title": "Order delivered",
		"body":         "Order #ORD-1 was delivered. Tap to review it.",
		"collapse_key": "order:" + order.String() + ":" + user.String(),
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data[%q] = %q, want %q", k, data[k], v)
		}
	}
	if title != want["title"] || body != want["body"] {
		t.Errorf("title/body = %q / %q", title, body)
	}
	// Without an override the generic copy and derived collapse key remain.
	title, _, data = buildPushData(user, "follow", "user", order, "/u/x", RenderOverride{})
	if title != "New Follower" || data["title"] != "New Follower" || data["entity_type"] != "user" {
		t.Errorf("generic push: %q %v", title, data)
	}
}
