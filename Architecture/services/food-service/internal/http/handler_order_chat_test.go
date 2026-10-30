package http

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit M3 and L6 (2026-10-30): order chat, support tickets and batch detail
// take no authority from X-Scopes; admins use the admin-service token family;
// mark-read requires party membership of the message's order; message bodies
// are capped.

type chatFakeStore struct {
	service.Store
	orderID    uuid.UUID
	customer   uuid.UUID
	owner      uuid.UUID
	rider      uuid.UUID // a party only while riderParty
	riderParty bool

	messageID    uuid.UUID // a message on orderID
	otherMessage uuid.UUID // a message on another order

	ticketID    uuid.UUID
	ticketOwner uuid.UUID

	appended   []postgres.OrderMessage
	listed     int
	marked     []string // role stamped
	ticketMsgs []postgres.TicketMessage
	batch      *postgres.DeliveryBatch
}

func (f *chatFakeStore) OrderPartyMembership(_ context.Context, orderID, userID uuid.UUID) (*postgres.OrderPartyMembership, error) {
	m := &postgres.OrderPartyMembership{}
	if orderID != f.orderID {
		return m, nil
	}
	m.IsCustomer = userID == f.customer
	m.IsRestaurantOwner = userID == f.owner
	m.IsDeliveryPartner = userID == f.rider && f.riderParty
	return m, nil
}

func (f *chatFakeStore) AppendOrderMessage(_ context.Context, orderID, authorID uuid.UUID, role, body string) (*postgres.OrderMessage, error) {
	m := postgres.OrderMessage{ID: uuid.New(), OrderID: orderID, AuthorID: authorID, AuthorRole: role, Body: body}
	f.appended = append(f.appended, m)
	return &m, nil
}

func (f *chatFakeStore) ListOrderMessages(context.Context, uuid.UUID) ([]postgres.OrderMessage, error) {
	f.listed++
	return []postgres.OrderMessage{{ID: f.messageID, Body: "gate code 4321"}}, nil
}

func (f *chatFakeStore) OrderMessageOrderID(_ context.Context, messageID uuid.UUID) (uuid.UUID, error) {
	switch messageID {
	case f.messageID:
		return f.orderID, nil
	case f.otherMessage:
		return uuid.New(), nil
	}
	return uuid.Nil, pgx.ErrNoRows
}

func (f *chatFakeStore) MarkMessageRead(_ context.Context, _, _, _ uuid.UUID, role string) error {
	f.marked = append(f.marked, role)
	return nil
}

func (f *chatFakeStore) GetTicketWithMessages(_ context.Context, ticketID uuid.UUID) (*postgres.Ticket, []postgres.TicketMessage, error) {
	if ticketID != f.ticketID {
		return nil, nil, pgx.ErrNoRows
	}
	return &postgres.Ticket{ID: f.ticketID, CustomerID: f.ticketOwner, Subject: "my address is ..."}, f.ticketMsgs, nil
}

func (f *chatFakeStore) AppendTicketMessage(_ context.Context, ticketID, authorID uuid.UUID, isAdmin bool, body string) (*postgres.TicketMessage, error) {
	m := postgres.TicketMessage{ID: uuid.New(), TicketID: ticketID, AuthorID: authorID, IsAdmin: isAdmin, Body: body}
	f.ticketMsgs = append(f.ticketMsgs, m)
	return &m, nil
}

func (f *chatFakeStore) GetBatchForOrder(_ context.Context, orderID uuid.UUID) (*postgres.DeliveryBatch, error) {
	if orderID != f.orderID {
		return nil, pgx.ErrNoRows
	}
	return f.batch, nil
}

func (f *chatFakeStore) GetBatchForOrderForPartner(context.Context, uuid.UUID, uuid.UUID) (*postgres.DeliveryBatch, error) {
	return nil, pgx.ErrNoRows
}

func newChatRig(t *testing.T) (*adminTokenRig, *chatFakeStore) {
	t.Helper()
	rg := newAdminTokenRig(t)
	st := &chatFakeStore{
		orderID: uuid.New(), customer: uuid.New(), owner: uuid.New(), rider: uuid.New(),
		messageID: uuid.New(), otherMessage: uuid.New(),
		ticketID: uuid.New(), ticketOwner: uuid.New(),
		batch: &postgres.DeliveryBatch{ID: uuid.New(), Status: "assigned"},
	}
	rg.r = adminTokenRouter(st, rg.v)
	return rg, st
}

// consumer is a consumer-session request: key, user id and whatever scopes
// identity minted into the session.
func consumer(user uuid.UUID, scopes string) map[string]string {
	return legacyAdmin(user, scopes)
}

func TestOrderChat_XScopesGrantNothing(t *testing.T) {
	rg, st := newChatRig(t)
	msgs := "/v1/food/orders/" + st.orderID.String() + "/messages"
	for _, scopes := range []string{"admin", "superadmin", "moderator", "profile admin moderator"} {
		stranger := uuid.New()
		w := adminServe(rg.r, http.MethodGet, msgs, "", consumer(stranger, scopes))
		if w.Code != http.StatusForbidden || errorCode(t, w) != "FOOD_ORDER_CHAT_FORBIDDEN" {
			t.Fatalf("GET with X-Scopes %q: status=%d code=%q, want 403 FOOD_ORDER_CHAT_FORBIDDEN", scopes, w.Code, errorCode(t, w))
		}
		w = adminServe(rg.r, http.MethodPost, msgs, `{"body":"hello"}`, consumer(stranger, scopes))
		if w.Code != http.StatusForbidden || errorCode(t, w) != "FOOD_ORDER_CHAT_FORBIDDEN" {
			t.Fatalf("POST with X-Scopes %q: status=%d code=%q, want 403", scopes, w.Code, errorCode(t, w))
		}
	}
	if st.listed != 0 || len(st.appended) != 0 {
		t.Fatalf("a stranger with admin scopes reached the thread: listed=%d appended=%d", st.listed, len(st.appended))
	}
}

func TestOrderChat_PartiesAndRiderWindow(t *testing.T) {
	rg, st := newChatRig(t)
	msgs := "/v1/food/orders/" + st.orderID.String() + "/messages"
	for name, user := range map[string]uuid.UUID{"customer": st.customer, "restaurant": st.owner} {
		w := adminServe(rg.r, http.MethodPost, msgs, `{"body":"on my way"}`, consumer(user, ""))
		if w.Code != http.StatusCreated {
			t.Fatalf("%s post: status=%d body=%s", name, w.Code, w.Body.String())
		}
		if got := st.appended[len(st.appended)-1].AuthorRole; got != name {
			t.Fatalf("%s posted as role %q", name, got)
		}
	}
	// Outside the window the rider is a stranger.
	st.riderParty = false
	if w := adminServe(rg.r, http.MethodGet, msgs, "", consumer(st.rider, "")); w.Code != http.StatusForbidden {
		t.Fatalf("rider outside the window read the chat: %d", w.Code)
	}
	st.riderParty = true
	if w := adminServe(rg.r, http.MethodGet, msgs, "", consumer(st.rider, "")); w.Code != http.StatusOK {
		t.Fatalf("rider inside the window: %d %s", w.Code, w.Body.String())
	}
}

func TestOrderChat_AdminTokenRoutes(t *testing.T) {
	rg, st := newChatRig(t)
	path := InternalAdminPrefix + "/orders/" + st.orderID.String() + "/messages"
	read := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermOrdersRead}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodGet, path, "", read); w.Code != http.StatusOK || st.listed != 1 {
		t.Fatalf("admin read: status=%d listed=%d body=%s", w.Code, st.listed, w.Body.String())
	}
	act := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, rg.actor.String()))
	w := adminServe(rg.r, http.MethodPost, path, `{"body":"support here"}`, act)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin post: status=%d body=%s", w.Code, w.Body.String())
	}
	got := st.appended[len(st.appended)-1]
	if got.AuthorRole != "admin" || got.AuthorID != rg.actor {
		t.Fatalf("admin message stored as role=%q author=%s, want admin by the token actor", got.AuthorRole, got.AuthorID)
	}
	// Without a token the admin route is closed, whatever X-Scopes says.
	if w := adminServe(rg.r, http.MethodGet, path, "", consumer(uuid.New(), "admin superadmin")); w.Code != http.StatusUnauthorized {
		t.Fatalf("admin route without a token: %d", w.Code)
	}
}

func TestMarkOrderMessageRead_RequiresPartyOfTheMessagesOrder(t *testing.T) {
	rg, st := newChatRig(t)
	read := func(msg uuid.UUID) string {
		return "/v1/food/orders/" + st.orderID.String() + "/messages/" + msg.String() + "/read"
	}
	// A stranger (even with admin scopes and a forged role) stamps nothing.
	w := adminServe(rg.r, http.MethodPost, read(st.messageID), `{"role":"admin"}`, consumer(uuid.New(), "admin"))
	if w.Code != http.StatusForbidden || errorCode(t, w) != "FOOD_ORDER_CHAT_FORBIDDEN" {
		t.Fatalf("stranger mark-read: status=%d code=%q", w.Code, errorCode(t, w))
	}
	// A message of another order, named under this order's path: 404.
	w = adminServe(rg.r, http.MethodPost, read(st.otherMessage), `{}`, consumer(st.customer, ""))
	if w.Code != http.StatusNotFound || errorCode(t, w) != "FOOD_ORDER_MESSAGE_NOT_FOUND" {
		t.Fatalf("foreign message: status=%d code=%q", w.Code, errorCode(t, w))
	}
	w = adminServe(rg.r, http.MethodPost, read(uuid.New()), `{}`, consumer(st.customer, ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown message: status=%d", w.Code)
	}
	// A rider outside the window: refused.
	if w := adminServe(rg.r, http.MethodPost, read(st.messageID), `{}`, consumer(st.rider, "")); w.Code != http.StatusForbidden {
		t.Fatalf("rider outside window mark-read: %d", w.Code)
	}
	if len(st.marked) != 0 {
		t.Fatalf("refused calls stamped reads: %v", st.marked)
	}
	// The customer, claiming to be the restaurant: stamped as customer.
	w = adminServe(rg.r, http.MethodPost, read(st.messageID), `{"role":"restaurant"}`, consumer(st.customer, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("customer mark-read: status=%d body=%s", w.Code, w.Body.String())
	}
	if len(st.marked) != 1 || st.marked[0] != "customer" {
		t.Fatalf("stamped roles = %v, want [customer]", st.marked)
	}
}

func TestSupportTicket_XScopesGrantNothing_AdminTokenDoes(t *testing.T) {
	rg, st := newChatRig(t)
	ticket := "/v1/food/support/tickets/" + st.ticketID.String()
	for _, scopes := range []string{"admin", "superadmin", "moderator"} {
		if w := adminServe(rg.r, http.MethodGet, ticket, "", consumer(uuid.New(), scopes)); w.Code != http.StatusForbidden {
			t.Fatalf("GET ticket with X-Scopes %q: %d, want 403", scopes, w.Code)
		}
		if w := adminServe(rg.r, http.MethodPost, ticket+"/messages", `{"body":"x"}`, consumer(uuid.New(), scopes)); w.Code != http.StatusForbidden {
			t.Fatalf("POST ticket message with X-Scopes %q: %d, want 403", scopes, w.Code)
		}
	}
	if len(st.ticketMsgs) != 0 {
		t.Fatalf("stranger posted into a ticket: %+v", st.ticketMsgs)
	}
	if w := adminServe(rg.r, http.MethodGet, ticket, "", consumer(st.ticketOwner, "")); w.Code != http.StatusOK {
		t.Fatalf("owner GET ticket: %d", w.Code)
	}

	admin := InternalAdminPrefix + "/support/tickets/" + st.ticketID.String()
	tok := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodGet, admin, "", tok); w.Code != http.StatusOK {
		t.Fatalf("admin GET ticket: %d %s", w.Code, w.Body.String())
	}
	if w := adminServe(rg.r, http.MethodPost, admin+"/messages", `{"body":"we are on it"}`, tok); w.Code != http.StatusCreated {
		t.Fatalf("admin POST ticket message: %d %s", w.Code, w.Body.String())
	}
	got := st.ticketMsgs[len(st.ticketMsgs)-1]
	if !got.IsAdmin || got.AuthorID != rg.actor {
		t.Fatalf("admin ticket message = %+v, want is_admin by the token actor", got)
	}
}

func TestAdminGetBatchForOrder_TokenOnly(t *testing.T) {
	rg, st := newChatRig(t)
	path := InternalAdminPrefix + "/orders/" + st.orderID.String() + "/batch"
	tok := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermOrdersRead}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodGet, path, "", tok); w.Code != http.StatusOK {
		t.Fatalf("admin batch: %d %s", w.Code, w.Body.String())
	}
	if w := adminServe(rg.r, http.MethodGet, "/v1/food/delivery/orders/"+st.orderID.String()+"/batch", "", consumer(uuid.New(), "admin")); w.Code != http.StatusNotFound {
		t.Fatalf("consumer batch with X-Scopes admin: %d, want 404", w.Code)
	}
}

func TestMessageBodies_AreCapped(t *testing.T) {
	rg, st := newChatRig(t)
	msgs := "/v1/food/orders/" + st.orderID.String() + "/messages"
	ticket := "/v1/food/support/tickets/" + st.ticketID.String() + "/messages"
	atCap := strings.Repeat("é", service.MaxMessageBodyRunes) // runes, not bytes
	overCap := atCap + "x"
	for _, tc := range []struct {
		path, user string
		owner      uuid.UUID
	}{{msgs, "customer", st.customer}, {ticket, "ticket owner", st.ticketOwner}} {
		w := adminServe(rg.r, http.MethodPost, tc.path, `{"body":"`+overCap+`"}`, consumer(tc.owner, ""))
		if w.Code != http.StatusBadRequest || errorCode(t, w) != "FOOD_MESSAGE_BODY_INVALID" {
			t.Fatalf("%s over cap: status=%d code=%q", tc.user, w.Code, errorCode(t, w))
		}
		if w := adminServe(rg.r, http.MethodPost, tc.path, `{"body":"   "}`, consumer(tc.owner, "")); w.Code != http.StatusBadRequest {
			t.Fatalf("%s blank body: %d", tc.user, w.Code)
		}
		if w := adminServe(rg.r, http.MethodPost, tc.path, `{"body":"`+atCap+`"}`, consumer(tc.owner, "")); w.Code != http.StatusCreated {
			t.Fatalf("%s at cap: %d %s", tc.user, w.Code, w.Body.String())
		}
	}
	// The admin token routes take the same cap.
	tok := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/orders/"+st.orderID.String()+"/messages", `{"body":"`+overCap+`"}`, tok); w.Code != http.StatusBadRequest {
		t.Fatalf("admin over cap: %d", w.Code)
	}
}

func TestValidateMessageBody(t *testing.T) {
	if err := service.ValidateMessageBody(strings.Repeat("a", service.MaxMessageBodyRunes)); err != nil {
		t.Fatalf("at cap refused: %v", err)
	}
	if err := service.ValidateMessageBody(strings.Repeat("a", service.MaxMessageBodyRunes+1)); err == nil {
		t.Fatal("over cap accepted")
	}
	if err := service.ValidateMessageBody(" \n\t"); err == nil {
		t.Fatal("blank accepted")
	}
}
