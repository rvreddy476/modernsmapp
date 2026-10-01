//go:build integration

package http

// MStore engagement (shop-engagement contract §1-4), over the real routes,
// the real store and commerce_it_test.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run EngagementIT -v

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// egCourier: 999999 is not serviceable, 110011 is an unreachable carrier,
// everything else is 3 days in transit.
type egCourier struct{ ctCourier }

func (egCourier) CheckServiceability(_ context.Context, req courier.ServiceabilityRequest) (*courier.ServiceabilityResult, error) {
	switch req.DropPincode {
	case "999999":
		return &courier.ServiceabilityResult{Serviceable: false, Courier: "stub", Reason: "pincode not serviceable"}, nil
	case "110011":
		return nil, errors.New("carrier timed out")
	}
	return &courier.ServiceabilityResult{Serviceable: true, Courier: "stub", EstimatedDays: 3, ShippingChargeMinor: 4900}, nil
}

type egRig struct {
	r   *gin.Engine
	mu  sync.Mutex
	now time.Time
}

func (rg *egRig) setNow(t time.Time) {
	rg.mu.Lock()
	rg.now = t
	rg.mu.Unlock()
}

func newEgRig(t *testing.T) *egRig {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("engagement-salt!"))
	if err != nil {
		t.Fatal(err)
	}
	rg := &egRig{now: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	svc := service.New(postgres.New(edgePool), nil, "").WithPII(cipher).WithCourier(egCourier{}).
		WithClock(func() time.Time { rg.mu.Lock(); defer rg.mu.Unlock(); return rg.now })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc).WithInternalKey(integrationInternalKey)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	rg.r = r
	return rg
}

type egShop struct {
	sellerID, sellerUser uuid.UUID
	live, hidden         uuid.UUID // a live product, and the same seller's draft
	liveVariant          uuid.UUID
}

func egExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := edgePool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

// seedEgShop: an approved seller with a pickup address at 560001, an
// optional dispatch SLA, one live product and one draft.
func seedEgShop(t *testing.T, slaHours int) egShop {
	t.Helper()
	s := egShop{sellerID: uuid.New(), sellerUser: uuid.New(), live: uuid.New(), hidden: uuid.New(), liveVariant: uuid.New()}
	egExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,postal_code,status,store_status)
	      VALUES ($1,$2,'EG Store',$3,'eg@example.test','KA','560001','approved','active')`,
		s.sellerID, s.sellerUser, "eg-"+s.sellerID.String()[:8])
	egExec(t, `INSERT INTO seller_addresses (seller_id,address_type,contact_name,phone,address_line_1,city,state,postal_code,is_default)
	      VALUES ($1,'pickup','Desk','9000000001','1 Road','Bengaluru','KA','560001',TRUE)`, s.sellerID)
	if slaHours > 0 {
		egExec(t, `INSERT INTO seller_fulfillment_settings (seller_id,dispatch_sla_hours) VALUES ($1,$2)`, s.sellerID, slaHours)
	}
	for _, p := range []struct {
		id     uuid.UUID
		status string
	}{{s.live, "active"}, {s.hidden, "draft"}} {
		approval := "approved"
		if p.status == "draft" {
			approval = "draft"
		}
		egExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status,visibility,return_policy_type,weight_grams,published_at)
		      VALUES ($1,$2,$3,$4,$5,$6,'public','7_days',350,NOW())`,
			p.id, s.sellerID, "EG "+p.id.String()[:8], "eg-"+p.id.String()[:8], p.status, approval)
		v := uuid.New()
		if p.id == s.live {
			v = s.liveVariant
		}
		egExec(t, `INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor)
		      VALUES ($1,$2,$3,1000,500,100000,50000)`, v, p.id, "EG-"+v.String()[:8])
		egExec(t, `INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty) VALUES ($1,$2,10,0)`, v, s.sellerID)
	}
	seedOfferFor(t, s.live, s.hidden)
	return s
}

func egJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env struct {
		Data  map[string]any `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
	if env.Data == nil {
		env.Data = map[string]any{"error_code": env.Error.Code}
	}
	return env.Data
}

func egWant(t *testing.T, what string, code int, body []byte, wantCode int, wantErr string) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("%s: %d %s, want %d %s", what, code, body, wantCode, wantErr)
	}
	if wantErr != "" && !strings.Contains(string(body), `"code":"`+wantErr+`"`) {
		t.Fatalf("%s: body %s does not carry %s", what, body, wantErr)
	}
}

// ─── §1 Delivery estimate ────────────────────────────────────────────────

func TestEngagementITDeliveryEstimate(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 49) // 49 h rounds UP to 3 dispatch days
	path := "/v1/commerce/products/" + shop.live.String() + "/delivery-estimate"

	// Wed 30 Sep 2026 15:30 IST. Dispatch Thu, Fri, Sat; +3 → Tue 6 Oct.
	// Earliest (one working day sooner): Fri 2 Oct + 3 → Mon 5 Oct.
	w := call(t, rg.r, http.MethodGet, path+"?pincode=500081", uuid.Nil, nil)
	egWant(t, "query pincode", w.Code, w.Body.Bytes(), 200, "")
	d := egJSON(t, w.Body.Bytes())
	if d["deliver_by"] != "2026-10-06" || d["max_days"] != 6.0 || d["min_days"] != 5.0 || d["dispatch_days"] != 3.0 ||
		d["serviceable"] != true || d["pincode"] != "500081" || d["pincode_source"] != "query" || d["courier"] != "stub" {
		t.Fatalf("estimate: %v", d)
	}

	// The IST date boundary: 18:29:59Z is still Wed 30 Sep in India;
	// 18:30Z is Thu 1 Oct, which moves every date by a day.
	rg.setNow(time.Date(2026, 9, 30, 18, 29, 59, 0, time.UTC))
	if d := egJSON(t, call(t, rg.r, http.MethodGet, path+"?pincode=500081", uuid.Nil, nil).Body.Bytes()); d["deliver_by"] != "2026-10-06" {
		t.Fatalf("23:59 IST Wed: %v", d)
	}
	rg.setNow(time.Date(2026, 9, 30, 18, 30, 0, 0, time.UTC))
	// Thu 1 Oct: dispatch Fri, Sat, (Sun skipped) Mon 5; +3 → Thu 8 Oct.
	if d := egJSON(t, call(t, rg.r, http.MethodGet, path+"?pincode=500081", uuid.Nil, nil).Body.Bytes()); d["deliver_by"] != "2026-10-08" || d["max_days"] != 7.0 {
		t.Fatalf("00:00 IST Thu (Sunday skipped in dispatch): %v", d)
	}
	rg.setNow(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))

	// Not serviceable: 200, serviceable false, no date keys at all.
	w = call(t, rg.r, http.MethodGet, path+"?pincode=999999", uuid.Nil, nil)
	egWant(t, "not serviceable", w.Code, w.Body.Bytes(), 200, "")
	for _, k := range []string{`"deliver_by"`, `"min_days"`, `"max_days"`, `"dispatch_days"`} {
		if strings.Contains(w.Body.String(), k) {
			t.Fatalf("not serviceable carries %s: %s", k, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `"serviceable":false`) {
		t.Fatalf("not serviceable: %s", w.Body.String())
	}

	// Invalid pincodes.
	for _, bad := range []string{"05008", "050081", "5000811", "50008a", "abcdef"} {
		w = call(t, rg.r, http.MethodGet, path+"?pincode="+bad, uuid.Nil, nil)
		egWant(t, "pincode "+bad, w.Code, w.Body.Bytes(), 400, "INVALID_PINCODE")
	}

	// Signed out, no pincode — even when some row in the address book is
	// filed under the all-zeroes id: an anonymous caller must never be
	// resolved to an address.
	nilAddr := uuid.New()
	egExec(t, `INSERT INTO customer_addresses (id,user_id,label,contact_name,phone,address_line_1,city,state,postal_code,is_default)
	      VALUES ($1,$2,'Home','N','9111111111','1 St','Hyderabad','TG','500081',TRUE)`, nilAddr, uuid.Nil)
	t.Cleanup(func() { _, _ = edgePool.Exec(context.Background(), `DELETE FROM customer_addresses WHERE id=$1`, nilAddr) })
	w = call(t, rg.r, http.MethodGet, path, uuid.Nil, nil)
	egWant(t, "signed out, no pincode", w.Code, w.Body.Bytes(), 400, "PINCODE_REQUIRED")
	// A saved address with a malformed pincode is refused like a typed one.
	odd := uuid.New()
	egExec(t, `INSERT INTO customer_addresses (user_id,label,contact_name,phone,address_line_1,city,state,postal_code,is_default)
	      VALUES ($1,'Home','O','9111111111','1 St','Hyderabad','TG','05008',TRUE)`, odd)
	w = call(t, rg.r, http.MethodGet, path, odd, nil)
	egWant(t, "saved malformed pincode", w.Code, w.Body.Bytes(), 400, "INVALID_PINCODE")

	// Signed in with no address: still required.
	w = call(t, rg.r, http.MethodGet, path, uuid.New(), nil)
	egWant(t, "signed in, no address", w.Code, w.Body.Bytes(), 400, "PINCODE_REQUIRED")

	// Signed in: the DEFAULT address's pincode, not the newer non-default one.
	buyer := uuid.New()
	egExec(t, `INSERT INTO customer_addresses (user_id,label,contact_name,phone,address_line_1,city,state,postal_code,is_default,created_at)
	      VALUES ($1,'Home','B','9111111111','1 St','Hyderabad','TG','500081',TRUE, NOW() - interval '1 day'),
	             ($1,'Work','B','9111111111','2 St','Mumbai','MH','400001',FALSE, NOW())`, buyer)
	w = call(t, rg.r, http.MethodGet, path, buyer, nil)
	egWant(t, "default address", w.Code, w.Body.Bytes(), 200, "")
	if d := egJSON(t, w.Body.Bytes()); d["pincode"] != "500081" || d["pincode_source"] != "default_address" {
		t.Fatalf("default address: %v", d)
	}
	// An explicit pincode wins over the address book.
	if d := egJSON(t, call(t, rg.r, http.MethodGet, path+"?pincode=400001", buyer, nil).Body.Bytes()); d["pincode"] != "400001" || d["pincode_source"] != "query" {
		t.Fatalf("explicit pincode: %v", d)
	}

	// The carrier is down: 503, retryable.
	w = call(t, rg.r, http.MethodGet, path+"?pincode=110011", uuid.Nil, nil)
	egWant(t, "courier down", w.Code, w.Body.Bytes(), 503, "COURIER_UNAVAILABLE")

	// A seller with nowhere to ship from: not serviceable, the carrier is
	// never asked about an empty pickup pincode.
	nowhere := seedEgShop(t, 0)
	egExec(t, `DELETE FROM seller_addresses WHERE seller_id=$1`, nowhere.sellerID)
	egExec(t, `UPDATE sellers SET postal_code=NULL WHERE id=$1`, nowhere.sellerID)
	w = call(t, rg.r, http.MethodGet, "/v1/commerce/products/"+nowhere.live.String()+"/delivery-estimate?pincode=500081", uuid.Nil, nil)
	egWant(t, "no pickup address", w.Code, w.Body.Bytes(), 200, "")
	if !strings.Contains(w.Body.String(), `"serviceable":false`) || strings.Contains(w.Body.String(), `"deliver_by"`) {
		t.Fatalf("no pickup address: %s", w.Body.String())
	}

	// A seller who never saved fulfilment settings: the default 2 days.
	plain := seedEgShop(t, 0)
	if d := egJSON(t, call(t, rg.r, http.MethodGet, "/v1/commerce/products/"+plain.live.String()+"/delivery-estimate?pincode=500081", uuid.Nil, nil).Body.Bytes()); d["dispatch_days"] != 2.0 || d["deliver_by"] != "2026-10-05" {
		t.Fatalf("default SLA: %v", d)
	}
}

// ─── Every new route 404s for a product a buyer may not see ──────────────

func TestEngagementITHiddenProductIs404Everywhere(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	buyer := uuid.New()
	reviewID := seedEgReview(t, shop, uuid.New())
	// Hide the live product's whole seller after the review exists.
	hiddenSeller := seedEgShop(t, 0)
	hiddenReview := seedEgReview(t, hiddenSeller, uuid.New())
	egExec(t, `UPDATE sellers SET status='suspended' WHERE id=$1`, hiddenSeller.sellerID)

	for _, tc := range []struct {
		name, method, path string
		actor              uuid.UUID
		body               any
		code               string
	}{
		{"estimate on a draft", http.MethodGet, "/v1/commerce/products/" + shop.hidden.String() + "/delivery-estimate?pincode=500081", buyer, nil, "PRODUCT_NOT_FOUND"},
		{"estimate, suspended seller", http.MethodGet, "/v1/commerce/products/" + hiddenSeller.live.String() + "/delivery-estimate?pincode=500081", buyer, nil, "PRODUCT_NOT_FOUND"},
		{"estimate, unknown id", http.MethodGet, "/v1/commerce/products/" + uuid.NewString() + "/delivery-estimate?pincode=500081", buyer, nil, "PRODUCT_NOT_FOUND"},
		{"like a draft", http.MethodPut, "/v1/commerce/products/" + shop.hidden.String() + "/reaction", buyer, map[string]any{"kind": "like"}, "PRODUCT_NOT_FOUND"},
		{"owner likes own draft", http.MethodPut, "/v1/commerce/products/" + shop.hidden.String() + "/reaction", shop.sellerUser, map[string]any{"kind": "like"}, "PRODUCT_NOT_FOUND"},
		{"like, suspended seller", http.MethodPut, "/v1/commerce/products/" + hiddenSeller.live.String() + "/reaction", buyer, map[string]any{"kind": "like"}, "PRODUCT_NOT_FOUND"},
		{"unreact on a draft", http.MethodDelete, "/v1/commerce/products/" + shop.hidden.String() + "/reaction", buyer, nil, "PRODUCT_NOT_FOUND"},
		{"share a draft", http.MethodPost, "/v1/commerce/products/" + shop.hidden.String() + "/share", buyer, map[string]any{"channel": "whatsapp"}, "PRODUCT_NOT_FOUND"},
		{"share, suspended seller", http.MethodPost, "/v1/commerce/products/" + hiddenSeller.live.String() + "/share", uuid.Nil, map[string]any{"channel": "native"}, "PRODUCT_NOT_FOUND"},
		{"vote, suspended seller", http.MethodPut, "/v1/commerce/reviews/" + hiddenReview.String() + "/vote", buyer, map[string]any{"vote": "helpful"}, "REVIEW_NOT_FOUND"},
		{"unvote, suspended seller", http.MethodDelete, "/v1/commerce/reviews/" + hiddenReview.String() + "/vote", buyer, nil, "REVIEW_NOT_FOUND"},
		{"vote, unknown review", http.MethodPut, "/v1/commerce/reviews/" + uuid.NewString() + "/vote", buyer, map[string]any{"vote": "helpful"}, "REVIEW_NOT_FOUND"},
	} {
		w := call(t, rg.r, tc.method, tc.path, tc.actor, tc.body)
		egWant(t, tc.name, w.Code, w.Body.Bytes(), 404, tc.code)
	}
	// Nothing was counted on the hidden products.
	var likes, shares int64
	_ = edgePool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(like_count),0), COALESCE(SUM(share_count),0) FROM products WHERE id = ANY($1)`,
		[]uuid.UUID{shop.hidden, hiddenSeller.live}).Scan(&likes, &shares)
	if likes != 0 || shares != 0 {
		t.Fatalf("hidden products were counted: likes=%d shares=%d", likes, shares)
	}
	// And the visible review is still votable (the control).
	w := call(t, rg.r, http.MethodPut, "/v1/commerce/reviews/"+reviewID.String()+"/vote", buyer, map[string]any{"vote": "helpful"})
	egWant(t, "control vote", w.Code, w.Body.Bytes(), 200, "")
}

// ─── §2 Reactions ────────────────────────────────────────────────────────

func egLikeCountInDB(t *testing.T, product uuid.UUID) (column, counted int64) {
	t.Helper()
	if err := edgePool.QueryRow(context.Background(), `
		SELECT p.like_count, (SELECT COUNT(*) FROM product_reactions r WHERE r.product_id=p.id AND r.kind='like')
		  FROM products p WHERE p.id=$1`, product).Scan(&column, &counted); err != nil {
		t.Fatal(err)
	}
	return column, counted
}

func TestEngagementITReactions(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	path := "/v1/commerce/products/" + shop.live.String() + "/reaction"
	u1, u2 := uuid.New(), uuid.New()

	step := func(name, method string, actor uuid.UUID, body any, wantReaction any, wantLikes float64) {
		t.Helper()
		w := call(t, rg.r, method, path, actor, body)
		egWant(t, name, w.Code, w.Body.Bytes(), 200, "")
		d := egJSON(t, w.Body.Bytes())
		if d["viewer_reaction"] != wantReaction || d["like_count"] != wantLikes {
			t.Fatalf("%s: %v, want reaction=%v likes=%v", name, d, wantReaction, wantLikes)
		}
		if len(d) != 2 {
			t.Fatalf("%s: the reply carries more than viewer_reaction and like_count: %v", name, d)
		}
		if col, n := egLikeCountInDB(t, shop.live); col != int64(wantLikes) || n != col {
			t.Fatalf("%s: products.like_count=%d, likes counted=%d, want %v", name, col, n, wantLikes)
		}
	}
	step("u1 likes", http.MethodPut, u1, map[string]any{"kind": "like"}, "like", 1)
	step("u1 likes again (idempotent)", http.MethodPut, u1, map[string]any{"kind": "like"}, "like", 1)
	step("u2 dislikes (not counted)", http.MethodPut, u2, map[string]any{"kind": "dislike"}, "dislike", 1)
	step("u2 dislikes again", http.MethodPut, u2, map[string]any{"kind": "dislike"}, "dislike", 1)
	step("u1 switches to dislike", http.MethodPut, u1, map[string]any{"kind": "dislike"}, "dislike", 0)
	step("u2 switches to like", http.MethodPut, u2, map[string]any{"kind": "like"}, "like", 1)
	step("u1 switches back to like", http.MethodPut, u1, map[string]any{"kind": "like"}, "like", 2)
	step("u1 clears", http.MethodDelete, u1, nil, nil, 1)
	step("u1 clears again", http.MethodDelete, u1, nil, nil, 1)
	step("u1 dislikes then", http.MethodPut, u1, map[string]any{"kind": "dislike"}, "dislike", 1)

	// One row per buyer.
	var rows int
	_ = edgePool.QueryRow(context.Background(), `SELECT COUNT(*) FROM product_reactions WHERE product_id=$1`, shop.live).Scan(&rows)
	if rows != 2 {
		t.Fatalf("%d reaction rows for two buyers", rows)
	}

	// Refusals.
	w := call(t, rg.r, http.MethodPut, path, u1, map[string]any{"kind": "love"})
	egWant(t, "bad kind", w.Code, w.Body.Bytes(), 400, "INVALID_BODY")
	w = call(t, rg.r, http.MethodPut, path, uuid.Nil, map[string]any{"kind": "like"})
	egWant(t, "signed out", w.Code, w.Body.Bytes(), 401, "UNAUTHORIZED")

	// GET /products/:id: like_count for all, viewer_reaction for a
	// signed-in caller only, and no dislike count anywhere.
	get := "/v1/commerce/products/" + shop.live.String()
	w = call(t, rg.r, http.MethodGet, get, u1, nil)
	egWant(t, "detail u1", w.Code, w.Body.Bytes(), 200, "")
	if !strings.Contains(w.Body.String(), `"viewer_reaction":"dislike"`) || !strings.Contains(w.Body.String(), `"like_count":1`) ||
		!strings.Contains(w.Body.String(), `"share_count":0`) {
		t.Fatalf("detail u1: %s", w.Body.String())
	}
	w = call(t, rg.r, http.MethodGet, get, uuid.New(), nil)
	if !strings.Contains(w.Body.String(), `"viewer_reaction":null`) {
		t.Fatalf("detail, signed in without a reaction: %s", w.Body.String())
	}
	w = call(t, rg.r, http.MethodGet, get, uuid.Nil, nil)
	if strings.Contains(w.Body.String(), `"viewer_reaction"`) || !strings.Contains(w.Body.String(), `"like_count":1`) {
		t.Fatalf("detail signed out: %s", w.Body.String())
	}
	// A summary row (the seller's public shop) carries like_count too.
	w = call(t, rg.r, http.MethodGet, "/v1/commerce/sellers/"+shop.sellerID.String()+"/products", uuid.Nil, nil)
	egWant(t, "seller shop", w.Code, w.Body.Bytes(), 200, "")
	if !strings.Contains(w.Body.String(), `"like_count":1`) {
		t.Fatalf("summary row without like_count: %s", w.Body.String())
	}
	for _, body := range []string{w.Body.String()} {
		if strings.Contains(strings.ToLower(body), "dislike_count") {
			t.Fatalf("a dislike count leaked: %s", body)
		}
	}
}

// Concurrent likes and switches from many buyers keep the counter equal to
// the rows it counts.
func TestEngagementITReactionsConcurrent(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	path := "/v1/commerce/products/" + shop.live.String() + "/reaction"
	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := uuid.New()
			_ = call(t, rg.r, http.MethodPut, path, u, map[string]any{"kind": "like"})
			if i%3 == 0 {
				_ = call(t, rg.r, http.MethodPut, path, u, map[string]any{"kind": "dislike"})
			}
		}(i)
	}
	wg.Wait()
	col, counted := egLikeCountInDB(t, shop.live)
	if col != counted || col != n-n/3 {
		t.Fatalf("like_count=%d, like rows=%d, want %d", col, counted, n-n/3)
	}

	// ONE buyer double-tapping from several devices at once: one row, one
	// like. Without the product-row lock every request reads "no previous
	// reaction" and adds one.
	other := seedEgShop(t, 0)
	same := uuid.New()
	opath := "/v1/commerce/products/" + other.live.String() + "/reaction"
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kind := "like"
			if i%4 == 3 {
				kind = "dislike"
			}
			_ = call(t, rg.r, http.MethodPut, opath, same, map[string]any{"kind": kind})
		}(i)
	}
	wg.Wait()
	_ = call(t, rg.r, http.MethodPut, opath, same, map[string]any{"kind": "like"})
	if col, counted := egLikeCountInDB(t, other.live); col != 1 || counted != 1 {
		t.Fatalf("one buyer's concurrent reactions: like_count=%d, like rows=%d, want 1/1", col, counted)
	}
}

// The store refuses a kind outside like/dislike by name, before the CHECK.
func TestEngagementITStoreRefusesUnknownReaction(t *testing.T) {
	shop := seedEgShop(t, 0)
	_, err := postgres.New(edgePool).SetProductReaction(context.Background(), shop.live, uuid.New(), "love")
	if !errors.Is(err, postgres.ErrInvalidReaction) {
		t.Fatalf("SetProductReaction(love): %v, want ErrInvalidReaction", err)
	}
}

// Each engagement write is throttled per user (60 a minute); shares per
// actor (20 a minute) with 429 RATE_LIMITED beyond.
func TestEngagementITRateLimits(t *testing.T) {
	shop := seedEgShop(t, 0)
	review := seedEgReview(t, shop, uuid.New())
	for _, tc := range []struct {
		name, method, path string
		body               any
		limit              int
	}{
		{"PUT reaction", http.MethodPut, "/v1/commerce/products/" + shop.live.String() + "/reaction", map[string]any{"kind": "like"}, 60},
		{"DELETE reaction", http.MethodDelete, "/v1/commerce/products/" + shop.live.String() + "/reaction", nil, 60},
		{"PUT vote", http.MethodPut, "/v1/commerce/reviews/" + review.String() + "/vote", map[string]any{"vote": "helpful"}, 60},
		{"DELETE vote", http.MethodDelete, "/v1/commerce/reviews/" + review.String() + "/vote", nil, 60},
		{"POST share", http.MethodPost, "/v1/commerce/products/" + shop.live.String() + "/share", map[string]any{"channel": "native"}, 20},
	} {
		rg := newEgRig(t) // a fresh limiter per route
		u := uuid.New()
		for i := 0; i < tc.limit; i++ {
			if w := call(t, rg.r, tc.method, tc.path, u, tc.body); w.Code >= 400 {
				t.Fatalf("%s: request %d refused: %d %s", tc.name, i+1, w.Code, w.Body.String())
			}
		}
		w := call(t, rg.r, tc.method, tc.path, u, tc.body)
		egWant(t, tc.name+" over the limit", w.Code, w.Body.Bytes(), 429, "RATE_LIMITED")
		// Another user is unaffected.
		if w := call(t, rg.r, tc.method, tc.path, uuid.New(), tc.body); w.Code >= 400 {
			t.Fatalf("%s: another user refused: %d", tc.name, w.Code)
		}
	}
}

// ─── §4 Share ────────────────────────────────────────────────────────────

func TestEngagementITShare(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	path := "/v1/commerce/products/" + shop.live.String() + "/share"
	shares := func() int64 {
		var n int64
		_ = edgePool.QueryRow(context.Background(), `SELECT share_count FROM products WHERE id=$1`, shop.live).Scan(&n)
		return n
	}
	for i, ch := range []string{"native", "whatsapp", "copy_link", "other"} {
		w := call(t, rg.r, http.MethodPost, path, uuid.New(), map[string]any{"channel": ch})
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("share %s: %d %q", ch, w.Code, w.Body.String())
		}
		if got := shares(); got != int64(i+1) {
			t.Fatalf("after %d shares share_count=%d", i+1, got)
		}
	}
	// Signed out counts too (by client IP).
	if w := call(t, rg.r, http.MethodPost, path, uuid.Nil, map[string]any{"channel": "native"}); w.Code != 204 || shares() != 5 {
		t.Fatalf("signed-out share: %d, count %d", w.Code, shares())
	}
	// The same actor sharing the same product again inside the window: 204,
	// not counted again.
	u := uuid.New()
	call(t, rg.r, http.MethodPost, path, u, map[string]any{"channel": "native"})
	if w := call(t, rg.r, http.MethodPost, path, u, map[string]any{"channel": "whatsapp"}); w.Code != 204 || shares() != 6 {
		t.Fatalf("repeat share: %d, count %d (want 6)", w.Code, shares())
	}
	// Bad channel.
	w := call(t, rg.r, http.MethodPost, path, uuid.New(), map[string]any{"channel": "telegram"})
	egWant(t, "bad channel", w.Code, w.Body.Bytes(), 400, "INVALID_BODY")
	// GET /products/:id gains share_count.
	w = call(t, rg.r, http.MethodGet, "/v1/commerce/products/"+shop.live.String(), uuid.Nil, nil)
	if !strings.Contains(w.Body.String(), `"share_count":6`) {
		t.Fatalf("detail share_count: %s", w.Body.String())
	}
}

// ─── §3 Review helpful votes ─────────────────────────────────────────────

// seedEgReview puts a delivered order line and a published review by
// author on shop's live product. Returns the review id.
func seedEgReview(t *testing.T, shop egShop, author uuid.UUID) uuid.UUID {
	t.Helper()
	return seedEgReviewAt(t, shop, author, time.Now().UTC())
}

func seedEgReviewAt(t *testing.T, shop egShop, author uuid.UUID, at time.Time) uuid.UUID {
	t.Helper()
	orderID, itemID, reviewID := uuid.New(), uuid.New(), uuid.New()
	egExec(t, `INSERT INTO orders (id,customer_user_id,order_number,status,payment_status,
	         payment_method,currency_code,
	         subtotal,shipping_charges,tax_amount,final_amount,
	         subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor)
	      VALUES ($1,$2,$3,'delivered','paid','upi','INR',500,0,0,500,50000,0,0,50000)`,
		orderID, author, "ORD-EG-"+orderID.String()[:8])
	egExec(t, `INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,product_title,sku,quantity,
	         unit_mrp,unit_price,tax_amount,final_price,unit_mrp_minor,unit_price_minor,tax_amount_minor,final_price_minor,status,delivered_at)
	      VALUES ($1,$2,$3,$4,$5,'EG item','EG-SKU',1,1000,500,0,500,100000,50000,0,50000,'delivered',NOW())`,
		itemID, orderID, shop.live, shop.liveVariant, shop.sellerID)
	egExec(t, `INSERT INTO reviews (id,product_id,seller_id,order_item_id,reviewer_id,rating,title,body,created_at,updated_at)
	      VALUES ($1,$2,$3,$4,$5,4,'EG','EG review',$6,$6)`, reviewID, shop.live, shop.sellerID, itemID, author, at)
	return reviewID
}

func TestEngagementITReviewVotes(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	author, v1, v2 := uuid.New(), uuid.New(), uuid.New()
	review := seedEgReview(t, shop, author)
	path := "/v1/commerce/reviews/" + review.String() + "/vote"
	helpfulInDB := func() (col, counted int) {
		_ = edgePool.QueryRow(context.Background(), `
			SELECT r.helpful_count, (SELECT COUNT(*) FROM review_votes v WHERE v.review_id=r.id AND v.is_helpful)
			  FROM reviews r WHERE r.id=$1`, review).Scan(&col, &counted)
		return
	}
	step := func(name, method string, actor uuid.UUID, body any, wantVote any, wantHelpful float64) {
		t.Helper()
		w := call(t, rg.r, method, path, actor, body)
		egWant(t, name, w.Code, w.Body.Bytes(), 200, "")
		d := egJSON(t, w.Body.Bytes())
		if d["viewer_vote"] != wantVote || d["helpful_count"] != wantHelpful || len(d) != 2 {
			t.Fatalf("%s: %v, want vote=%v helpful=%v", name, d, wantVote, wantHelpful)
		}
		if col, n := helpfulInDB(); col != int(wantHelpful) || n != col {
			t.Fatalf("%s: helpful_count=%d, helpful rows=%d", name, col, n)
		}
	}
	step("v1 helpful", http.MethodPut, v1, map[string]any{"vote": "helpful"}, "helpful", 1)
	step("v1 helpful again", http.MethodPut, v1, map[string]any{"vote": "helpful"}, "helpful", 1)
	step("v2 not helpful (not counted)", http.MethodPut, v2, map[string]any{"vote": "not_helpful"}, "not_helpful", 1)
	step("v1 switches to not helpful", http.MethodPut, v1, map[string]any{"vote": "not_helpful"}, "not_helpful", 0)
	step("v2 switches to helpful", http.MethodPut, v2, map[string]any{"vote": "helpful"}, "helpful", 1)
	step("v2 clears", http.MethodDelete, v2, nil, nil, 0)
	step("v2 clears again", http.MethodDelete, v2, nil, nil, 0)

	// The author may not vote on their own review, either way.
	for _, v := range []string{"helpful", "not_helpful"} {
		w := call(t, rg.r, http.MethodPut, path, author, map[string]any{"vote": v})
		egWant(t, "own review "+v, w.Code, w.Body.Bytes(), 403, "CANNOT_VOTE_OWN_REVIEW")
	}
	var authorRows int
	_ = edgePool.QueryRow(context.Background(), `SELECT COUNT(*) FROM review_votes WHERE review_id=$1 AND user_id=$2`, review, author).Scan(&authorRows)
	if authorRows != 0 {
		t.Fatalf("the author's own vote was stored")
	}
	w := call(t, rg.r, http.MethodPut, path, v1, map[string]any{"vote": "meh"})
	egWant(t, "bad vote", w.Code, w.Body.Bytes(), 400, "INVALID_BODY")
	w = call(t, rg.r, http.MethodPut, path, uuid.Nil, map[string]any{"vote": "helpful"})
	egWant(t, "signed out", w.Code, w.Body.Bytes(), 401, "UNAUTHORIZED")

	// A rejected review cannot be voted on.
	egExec(t, `UPDATE reviews SET moderation_status='rejected' WHERE id=$1`, review)
	w = call(t, rg.r, http.MethodPut, path, v1, map[string]any{"vote": "helpful"})
	egWant(t, "rejected review", w.Code, w.Body.Bytes(), 404, "REVIEW_NOT_FOUND")
}

// One voter, many concurrent taps: one row, and the count follows it.
func TestEngagementITReviewVotesConcurrent(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	review := seedEgReview(t, shop, uuid.New())
	path := "/v1/commerce/reviews/" + review.String() + "/vote"
	voter := uuid.New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := "helpful"
			if i%4 == 3 {
				v = "not_helpful"
			}
			_ = call(t, rg.r, http.MethodPut, path, voter, map[string]any{"vote": v})
		}(i)
	}
	wg.Wait()
	_ = call(t, rg.r, http.MethodPut, path, voter, map[string]any{"vote": "helpful"})
	var col, rows int
	_ = edgePool.QueryRow(context.Background(), `
		SELECT r.helpful_count, (SELECT COUNT(*) FROM review_votes v WHERE v.review_id=r.id AND v.is_helpful)
		  FROM reviews r WHERE r.id=$1`, review).Scan(&col, &rows)
	if col != 1 || rows != 1 {
		t.Fatalf("one voter's concurrent votes: helpful_count=%d, helpful rows=%d, want 1/1", col, rows)
	}
}

// GET /products/:id/reviews: helpful_count and viewer_vote on every row, and
// sort=helpful (default) | recent.
func TestEngagementITReviewSort(t *testing.T) {
	rg := newEgRig(t)
	shop := seedEgShop(t, 0)
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	// old: 2 helpful; mid: 0; new: 1 helpful + 3 not-helpful (not counted).
	old := seedEgReviewAt(t, shop, uuid.New(), base)
	mid := seedEgReviewAt(t, shop, uuid.New(), base.Add(time.Hour))
	newest := seedEgReviewAt(t, shop, uuid.New(), base.Add(2*time.Hour))
	viewer := uuid.New()
	vote := func(review, who uuid.UUID, v string) {
		t.Helper()
		w := call(t, rg.r, http.MethodPut, "/v1/commerce/reviews/"+review.String()+"/vote", who, map[string]any{"vote": v})
		egWant(t, "vote", w.Code, w.Body.Bytes(), 200, "")
	}
	vote(old, viewer, "helpful")
	vote(old, uuid.New(), "helpful")
	vote(newest, uuid.New(), "helpful")
	for i := 0; i < 3; i++ {
		vote(newest, uuid.New(), "not_helpful")
	}
	vote(mid, viewer, "not_helpful")

	type row struct {
		ID           string  `json:"id"`
		HelpfulCount *int    `json:"helpful_count"`
		ViewerVote   *string `json:"viewer_vote"`
	}
	list := func(q string, who uuid.UUID) ([]row, string) {
		t.Helper()
		w := call(t, rg.r, http.MethodGet, "/v1/commerce/products/"+shop.live.String()+"/reviews"+q, who, nil)
		egWant(t, "reviews "+q, w.Code, w.Body.Bytes(), 200, "")
		var env struct {
			Data struct {
				Reviews []row `json:"reviews"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data.Reviews, w.Body.String()
	}
	ids := func(rs []row) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}

	for _, q := range []string{"", "?sort=helpful", "?sort=bogus"} {
		rs, _ := list(q, viewer)
		if want := strings.Join([]string{old.String(), newest.String(), mid.String()}, ","); ids(rs) != want {
			t.Fatalf("sort %q: %s, want %s", q, ids(rs), want)
		}
		if *rs[0].HelpfulCount != 2 || *rs[1].HelpfulCount != 1 || *rs[2].HelpfulCount != 0 {
			t.Fatalf("helpful counts: %d %d %d", *rs[0].HelpfulCount, *rs[1].HelpfulCount, *rs[2].HelpfulCount)
		}
		if rs[0].ViewerVote == nil || *rs[0].ViewerVote != "helpful" || rs[1].ViewerVote != nil ||
			rs[2].ViewerVote == nil || *rs[2].ViewerVote != "not_helpful" {
			t.Fatalf("viewer votes: %+v", rs)
		}
	}
	rs, _ := list("?sort=recent", viewer)
	if want := strings.Join([]string{newest.String(), mid.String(), old.String()}, ","); ids(rs) != want {
		t.Fatalf("sort recent: %s, want %s", ids(rs), want)
	}
	// Signed out: viewer_vote present and null on every row; helpful_count
	// present even when zero.
	rs, body := list("", uuid.Nil)
	for _, r := range rs {
		if r.ViewerVote != nil || r.HelpfulCount == nil {
			t.Fatalf("signed out row: %+v", r)
		}
	}
	if strings.Count(body, `"viewer_vote":null`) != 3 || strings.Count(body, `"helpful_count":`) != 3 {
		t.Fatalf("signed-out rows: %s", body)
	}
	if strings.Contains(body, "not_helpful_count") {
		t.Fatalf("a not-helpful count leaked: %s", body)
	}
}
