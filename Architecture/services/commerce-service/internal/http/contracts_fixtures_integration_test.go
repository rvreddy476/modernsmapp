//go:build integration

package http

// The seeded world behind the golden fixtures, and the fixtures themselves.
// See contracts_integration_test.go for the rules.
//
// Every id below is fixed. The block is 00000000-0000-4000-8000-0000000cXXXX
// (users, sellers, addresses, products, orders) and …0000000dXXXX (media).
// Timestamps are fixed to September 2026 so the stabiliser leaves them alone.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/google/uuid"
)

// ─── Fixed identities ────────────────────────────────────────────────────

var (
	ctBuyer        = uuid.MustParse("00000000-0000-4000-8000-0000000c0001")
	ctSellerUser   = uuid.MustParse("00000000-0000-4000-8000-0000000c0002")
	ctStranger     = uuid.MustParse("00000000-0000-4000-8000-0000000c0003")
	ctDraftUser    = uuid.MustParse("00000000-0000-4000-8000-0000000c0004")
	ctOtherSellerU = uuid.MustParse("00000000-0000-4000-8000-0000000c0005")

	ctSeller      = uuid.MustParse("00000000-0000-4000-8000-0000000c0010")
	ctDraftSeller = uuid.MustParse("00000000-0000-4000-8000-0000000c0011")
	ctOtherSeller = uuid.MustParse("00000000-0000-4000-8000-0000000c0012")

	ctPickupAddr   = uuid.MustParse("00000000-0000-4000-8000-0000000c0020")
	ctBuyerAddr    = uuid.MustParse("00000000-0000-4000-8000-0000000c0021")
	ctBuyerAddrFar = uuid.MustParse("00000000-0000-4000-8000-0000000c0022")

	// P1: the hero product — two variants, gallery, attributes, a review.
	ctP1  = uuid.MustParse("00000000-0000-4000-8000-0000000c0100")
	ctV1  = uuid.MustParse("00000000-0000-4000-8000-0000000c0101")
	ctV1b = uuid.MustParse("00000000-0000-4000-8000-0000000c0102")
	// P2: the cart line whose price moved after it was added.
	ctP2 = uuid.MustParse("00000000-0000-4000-8000-0000000c0110")
	ctV2 = uuid.MustParse("00000000-0000-4000-8000-0000000c0111")
	// P3: paused after it was added — the unsellable cart line.
	ctP3 = uuid.MustParse("00000000-0000-4000-8000-0000000c0120")
	ctV3 = uuid.MustParse("00000000-0000-4000-8000-0000000c0121")
	// P4: the seller's draft.
	ctP4 = uuid.MustParse("00000000-0000-4000-8000-0000000c0130")
	ctV4 = uuid.MustParse("00000000-0000-4000-8000-0000000c0131")
	// P5: another seller's live product (the MULTIPLE_SELLERS line).
	ctP5 = uuid.MustParse("00000000-0000-4000-8000-0000000c0140")
	ctV5 = uuid.MustParse("00000000-0000-4000-8000-0000000c0141")

	ctUnknownProduct = uuid.MustParse("00000000-0000-4000-8000-0000000c0fff")

	// Orders, oldest first by created_at.
	ctOConfirmed  = uuid.MustParse("00000000-0000-4000-8000-0000000c0200")
	ctIConfirmed  = uuid.MustParse("00000000-0000-4000-8000-0000000c0201")
	ctODelivered  = uuid.MustParse("00000000-0000-4000-8000-0000000c0210")
	ctShipment    = uuid.MustParse("00000000-0000-4000-8000-0000000c0211")
	ctInvoice     = uuid.MustParse("00000000-0000-4000-8000-0000000c0212")
	ctIDelivered  = uuid.MustParse("00000000-0000-4000-8000-0000000c0213")
	ctIDelivered2 = uuid.MustParse("00000000-0000-4000-8000-0000000c0214")
	ctOFailed     = uuid.MustParse("00000000-0000-4000-8000-0000000c0220")
	ctIFailed     = uuid.MustParse("00000000-0000-4000-8000-0000000c0221")
	ctORefund     = uuid.MustParse("00000000-0000-4000-8000-0000000c0230")
	ctIRefund     = uuid.MustParse("00000000-0000-4000-8000-0000000c0231")
	ctOPending    = uuid.MustParse("00000000-0000-4000-8000-0000000c0240")
	ctIPending    = uuid.MustParse("00000000-0000-4000-8000-0000000c0241")

	ctReview = uuid.MustParse("00000000-0000-4000-8000-0000000c0300")
	ctCart   = uuid.MustParse("00000000-0000-4000-8000-0000000c0400")

	ctTax0  = uuid.MustParse("00000000-0000-4000-8000-0000000c0e00")
	ctTax5  = uuid.MustParse("00000000-0000-4000-8000-0000000c0e05")
	ctTax12 = uuid.MustParse("00000000-0000-4000-8000-0000000c0e12")
	ctTax18 = uuid.MustParse("00000000-0000-4000-8000-0000000c0e18")
	ctTax28 = uuid.MustParse("00000000-0000-4000-8000-0000000c0e28")

	ctCatElectronics = uuid.MustParse("00000000-0000-4000-8000-00000000c001")
	ctCatFashion     = uuid.MustParse("00000000-0000-4000-8000-00000000c002")
	ctCatHome        = uuid.MustParse("00000000-0000-4000-8000-00000000c003")

	ctMedia1   = uuid.MustParse("00000000-0000-4000-8000-0000000d0001")
	ctMedia2   = uuid.MustParse("00000000-0000-4000-8000-0000000d0002")
	ctMediaNew = uuid.MustParse("00000000-0000-4000-8000-0000000d0003")
	ctMediaKYC = uuid.MustParse("00000000-0000-4000-8000-0000000d0004")

	// Coupons (migration 038) and the admin who manages them.
	ctAdmin       = uuid.MustParse("00000000-0000-4000-8000-0000000c0006")
	ctCpnPct      = uuid.MustParse("00000000-0000-4000-8000-0000000c0501") // MOMENTUM10: 10%, cap ₹200, public
	ctCpnEarbuds  = uuid.MustParse("00000000-0000-4000-8000-0000000c0502") // EARBUDS150: ₹150 off P1 only
	ctCpnSecret   = uuid.MustParse("00000000-0000-4000-8000-0000000c0503") // VIPSECRET: not public
	ctCpnBigSpend = uuid.MustParse("00000000-0000-4000-8000-0000000c0504") // BIGSPEND: minimum ₹5,000
	ctCpnExpired  = uuid.MustParse("00000000-0000-4000-8000-0000000c0505") // EXPIRED5
	ctCpnUsedUp   = uuid.MustParse("00000000-0000-4000-8000-0000000c0506") // USEDUP: 1 of 1 used
	ctCpnOther    = uuid.MustParse("00000000-0000-4000-8000-0000000c0507") // KETTLE20: the other shop's
	ctCpnPlatform = uuid.MustParse("00000000-0000-4000-8000-0000000c0508") // MSTORE50: platform-funded
	ctCpnLastUse  = uuid.MustParse("00000000-0000-4000-8000-0000000c0509") // ONEUSELEFT: 0 of 1 used
	ctOOffer      = uuid.MustParse("00000000-0000-4000-8000-0000000c0250") // paid with a coupon and a bank offer
	ctIOffer      = uuid.MustParse("00000000-0000-4000-8000-0000000c0251")
	ctBankOffer   = uuid.MustParse("00000000-0000-4000-8000-0000000c0d01")
)

// ─── The seed ────────────────────────────────────────────────────────────

func (e *contractEnv) seed() {
	t := e.t
	t.Helper()

	// Every id above is "known": the stabiliser leaves it alone. The
	// categories and banners from migrations 023/024 are fixed too.
	for _, id := range []uuid.UUID{
		ctBuyer, ctSellerUser, ctStranger, ctDraftUser, ctOtherSellerU,
		ctSeller, ctDraftSeller, ctOtherSeller, ctPickupAddr, ctBuyerAddr, ctBuyerAddrFar,
		ctP1, ctV1, ctV1b, ctP2, ctV2, ctP3, ctV3, ctP4, ctV4, ctP5, ctV5, ctUnknownProduct,
		ctOConfirmed, ctIConfirmed, ctODelivered, ctShipment, ctInvoice, ctIDelivered, ctIDelivered2,
		ctOFailed, ctIFailed, ctORefund, ctIRefund, ctOPending, ctIPending, ctReview, ctCart,
		ctTax0, ctTax5, ctTax12, ctTax18, ctTax28, ctMedia1, ctMedia2, ctMediaNew, ctMediaKYC,
		ctAdmin, ctCpnPct, ctCpnEarbuds, ctCpnSecret, ctCpnBigSpend, ctCpnExpired, ctCpnUsedUp, ctCpnOther,
		ctCpnPlatform, ctCpnLastUse, ctOOffer, ctIOffer, ctBankOffer,
		uuid.MustParse("00000000-0000-4000-8000-0000000c0d02"), uuid.MustParse("00000000-0000-4000-8000-0000000c0d03"),
	} {
		e.knownIDs[id.String()] = true
	}
	rows, err := e.pool.Query(context.Background(),
		`SELECT id::text FROM product_categories UNION ALL SELECT id::text FROM commerce_banners`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		e.knownIDs[id] = true
	}
	rows.Close()

	// Tax classes are seeded by setup.sql with gen_random_uuid(); pin them
	// before anything references them.
	for name, id := range map[string]uuid.UUID{
		"GST 0%": ctTax0, "GST 5%": ctTax5, "GST 12%": ctTax12, "GST 18%": ctTax18, "GST 28%": ctTax28,
	} {
		e.exec(`UPDATE tax_classes SET id = $1 WHERE name = $2`, id, name)
	}
	e.exec(`UPDATE tax_classes SET created_at = '2026-09-01T00:00:00Z'`)
	e.exec(`UPDATE product_categories SET created_at = '2026-09-01T00:00:00Z', updated_at = '2026-09-01T00:00:00Z'`)
	e.exec(`UPDATE commerce_banners SET created_at = '2026-09-01T00:00:00Z', updated_at = '2026-09-01T00:00:00Z'`)

	// ── Sellers ──────────────────────────────────────────────────────────
	e.exec(`INSERT INTO sellers (id,user_id,seller_type,store_name,slug,description,email,phone,state,city,postal_code,
	           status,onboarding_step,approved_at,created_at,updated_at)
	        VALUES ($1,$2,'business','Momentum Electronics','momentum-electronics','Phones, audio and accessories',
	           'shop@momentum-electronics.test','9000000001','KA','Bengaluru','560001',
	           'approved',7,'2026-09-01T09:00:00Z','2026-09-01T09:00:00Z','2026-09-01T09:00:00Z')`, ctSeller, ctSellerUser)
	e.exec(`INSERT INTO seller_addresses (id,seller_id,address_type,contact_name,phone,address_line_1,city,state,postal_code,is_default,created_at)
	        VALUES ($1,$2,'pickup','Warehouse desk','9000000001','1 Warehouse Road','Bengaluru','KA','560001',TRUE,'2026-09-01T09:00:00Z')`,
		ctPickupAddr, ctSeller)
	// Sealed shape (035): no plaintext number, only its last four digits.
	e.exec(`INSERT INTO seller_payout_accounts (seller_id,account_holder_name,bank_name,account_number,account_number_last4,
	           ifsc_code,upi_id,verification_status,is_primary,created_at,updated_at)
	        VALUES ($1,'Momentum Electronics','HDFC Bank','','4321','HDFC0001234','momentum@upi','verified',TRUE,
	           '2026-09-01T09:00:00Z','2026-09-01T09:00:00Z')`, ctSeller)
	e.exec(`INSERT INTO seller_documents (seller_id,document_type,media_id,verification_status,uploaded_at)
	        VALUES ($1,'pan_card',$2,'verified','2026-09-01T09:00:00Z')`, ctSeller, ctMediaKYC)

	// A shop that has only started the wizard: nothing else is filled in.
	e.exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,status,onboarding_step,created_at,updated_at)
	        VALUES ($1,$2,'Half Started','half-started','','draft',1,'2026-09-02T09:00:00Z','2026-09-02T09:00:00Z')`,
		ctDraftSeller, ctDraftUser)

	e.exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status,created_at,updated_at)
	        VALUES ($1,$2,'Other Shop','other-shop','other@shop.test','MH','approved','2026-09-01T09:00:00Z','2026-09-01T09:00:00Z')`,
		ctOtherSeller, ctOtherSellerU)

	// ── Products ─────────────────────────────────────────────────────────
	product := func(id, seller, category, tax uuid.UUID, title, slug, status, approval string, published bool, created string, image string) {
		pub := "NULL"
		if published {
			pub = "'" + created + "'"
		}
		e.exec(`INSERT INTO products (id,seller_id,category_id,tax_class_id,title,slug,description,status,approval_status,
		           return_policy_type,return_policy_days,hsn_code,weight_grams,source_image_url,created_at,updated_at,published_at)
		        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'7_days',7,'8518',350,$10,$11,$11,`+pub+`)`,
			id, seller, category, tax, title, slug, title+" — the description.", status, approval, image, created)
	}
	product(ctP1, ctSeller, ctCatElectronics, ctTax18, "Momentum Wireless Earbuds", "momentum-wireless-earbuds",
		"active", "approved", true, "2026-09-03T10:00:00Z", "https://img.example.test/earbuds.jpg")
	product(ctP2, ctSeller, ctCatFashion, ctTax5, "Momentum Canvas Cap", "momentum-canvas-cap",
		"active", "approved", true, "2026-09-04T10:00:00Z", "https://img.example.test/cap.jpg")
	product(ctP3, ctSeller, ctCatHome, ctTax12, "Momentum Steel Bottle", "momentum-steel-bottle",
		"paused", "approved", true, "2026-09-05T10:00:00Z", "https://img.example.test/bottle.jpg")
	product(ctP4, ctSeller, ctCatElectronics, ctTax18, "Momentum Desk Lamp (draft)", "momentum-desk-lamp",
		"draft", "draft", false, "2026-09-06T10:00:00Z", "")
	product(ctP5, ctOtherSeller, ctCatHome, ctTax18, "Other Shop Kettle", "other-shop-kettle",
		"active", "approved", true, "2026-09-02T10:00:00Z", "https://img.example.test/kettle.jpg")

	variant := func(id, product uuid.UUID, sku string, o1n, o1v string, mrp, price int64, stock int, seller uuid.UUID, created string) {
		e.exec(`INSERT INTO product_variants (id,product_id,sku,option_1_name,option_1_value,mrp,selling_price,mrp_minor,selling_price_minor,
		           weight_grams,created_at,updated_at)
		        VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6::numeric/100,$7::numeric/100,$6,$7,350,$8,$8)`,
			id, product, sku, o1n, o1v, mrp, price, created)
		e.exec(`INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty,updated_at)
		        VALUES ($1,$2,$3,0,$4)`, id, seller, stock, created)
	}
	variant(ctV1, ctP1, "MOM-EB-BLK", "Colour", "Black", 149900, 129900, 25, ctSeller, "2026-09-03T10:00:00Z")
	variant(ctV1b, ctP1, "MOM-EB-WHT", "Colour", "White", 149900, 129900, 0, ctSeller, "2026-09-03T10:00:01Z")
	variant(ctV2, ctP2, "MOM-CAP-OS", "Size", "One size", 59900, 49900, 40, ctSeller, "2026-09-04T10:00:00Z")
	variant(ctV3, ctP3, "MOM-BTL-750", "Capacity", "750 ml", 99900, 79900, 12, ctSeller, "2026-09-05T10:00:00Z")
	variant(ctV4, ctP4, "MOM-LAMP-1", "", "", 249900, 199900, 0, ctSeller, "2026-09-06T10:00:00Z")
	variant(ctV5, ctP5, "OTH-KTL-1", "", "", 189900, 159900, 7, ctOtherSeller, "2026-09-02T10:00:00Z")

	// Migration 027's offers: the seller's copy of the lifecycle, which the
	// summary projection joins on.
	e.exec(`INSERT INTO product_offers (product_id, seller_id, status, visibility, approval_status, rejection_reason,
	                                    published_at, condition, created_at, updated_at)
	        SELECT p.id, p.seller_id, p.status, p.visibility, p.approval_status, p.rejection_reason,
	               p.published_at, p.condition, p.created_at, p.updated_at
	          FROM products p`)
	e.exec(`UPDATE product_variants v SET offer_id = o.id
	          FROM product_offers o WHERE o.product_id = v.product_id AND v.offer_id IS NULL`)

	e.exec(`INSERT INTO product_media (product_id,media_id,media_type,sort_order,created_at) VALUES
	        ($1,$2,'image',0,'2026-09-03T10:00:00Z'), ($1,$3,'image',1,'2026-09-03T10:00:01Z')`, ctP1, ctMedia1, ctMedia2)
	e.exec(`INSERT INTO product_attributes (product_id,name,value,unit,sort_order) VALUES
	        ($1,'Battery life','24','hours',0), ($1,'Bluetooth','5.3',NULL,1), ($1,'Water resistance','IPX4',NULL,2)`, ctP1)
	e.exec(`UPDATE products SET avg_rating = 4.5, review_count = 1, order_count = 3 WHERE id = $1`, ctP1)
	// Migration 037: one like and one dislike from two other shoppers. The
	// counter shows the like only — a dislike is counted nowhere.
	e.exec(`INSERT INTO product_reactions (product_id,user_id,kind,created_at) VALUES
	        ($1,$2,'like','2026-09-07T09:00:00Z'), ($1,$3,'dislike','2026-09-07T09:05:00Z')`, ctP1, ctStranger, ctOtherSellerU)
	e.exec(`UPDATE products SET like_count = 1 WHERE id = $1`, ctP1)

	// ── Buyer ────────────────────────────────────────────────────────────
	e.exec(`INSERT INTO customer_addresses (id,user_id,label,contact_name,phone,address_line_1,address_line_2,landmark,city,state,postal_code,
	           address_type,is_default,created_at,updated_at)
	        VALUES ($1,$2,'Home','Asha Buyer','9111111111','5 Main Street','Flat 2B','Near the park','Bengaluru','KA','560002',
	           'home',TRUE,'2026-09-02T08:00:00Z','2026-09-02T08:00:00Z')`, ctBuyerAddr, ctBuyer)
	e.exec(`INSERT INTO customer_addresses (id,user_id,label,contact_name,phone,address_line_1,city,state,postal_code,
	           address_type,is_default,created_at,updated_at)
	        VALUES ($1,$2,'Work','Asha Buyer','9111111111','Remote Outpost 9','Leh','LA','999999',
	           'work',FALSE,'2026-09-02T08:05:00Z','2026-09-02T08:05:00Z')`, ctBuyerAddrFar, ctBuyer)
	e.exec(`INSERT INTO commerce_favourites (user_id,product_id,created_at) VALUES ($1,$2,'2026-09-07T08:00:00Z')`, ctBuyer, ctP1)

	// The bag: one line whose price moved (P2 was ₹459 when added, ₹499
	// now) and one whose product was paused after it was added (P3).
	e.exec(`INSERT INTO carts (id,user_id,updated_at) VALUES ($1,$2,'2026-09-08T08:00:00Z')`, ctCart, ctBuyer)
	e.exec(`INSERT INTO cart_items (cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor,added_at)
	        VALUES ($1,$2,$3,1,459.00,45900,'2026-09-08T08:00:00Z')`, ctCart, ctV2, ctP2)
	e.exec(`INSERT INTO cart_items (cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor,added_at)
	        VALUES ($1,$2,$3,1,799.00,79900,'2026-09-08T08:00:01Z')`, ctCart, ctV3, ctP3)

	// ── Orders ───────────────────────────────────────────────────────────
	snapshot := `{"contact_name":"Asha Buyer","phone":"9111111111","address_line_1":"5 Main Street","address_line_2":"Flat 2B",` +
		`"landmark":"Near the park","city":"Bengaluru","state":"KA","postal_code":"560002","country":"IN"}`
	order := func(id uuid.UUID, number, status, payStatus string, unit, tax, total int64, created string) {
		e.exec(`INSERT INTO orders (id,customer_user_id,order_number,subtotal,shipping_charges,tax_amount,final_amount,
		           subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor,
		           payment_method,payment_status,status,delivery_address_id,delivery_address_snapshot,
		           place_of_supply_state,cgst_minor,sgst_minor,igst_minor,created_at,updated_at)
		        VALUES ($1,$2,$3,$4::numeric/100,49.00,$5::numeric/100,$6::numeric/100,
		           $4,4900,$5,$6,
		           'upi',$7,$8,$9,$10::jsonb,
		           'KA',$5/2,$5/2,0,$11,$11)`,
			id, ctBuyer, number, unit, tax, total, payStatus, status, ctBuyerAddr, snapshot, created)
	}
	item := func(id, order uuid.UUID, product, variant uuid.UUID, title, sku string, mrp, unit, tax int64, status string, deliveredAt string, created string) {
		del := "NULL"
		if deliveredAt != "" {
			del = "'" + deliveredAt + "'"
		}
		e.exec(`INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,product_title,variant_details,sku,quantity,
		           unit_mrp,unit_price,tax_amount,final_price,unit_mrp_minor,unit_price_minor,tax_amount_minor,final_price_minor,
		           status,delivered_at,tax_class_id,hsn_code,created_at)
		        VALUES ($1,$2,$3,$4,$5,$6,'{"option_1_name":"Colour","option_1_value":"Black"}',$7,1,
		           $8::numeric/100,$9::numeric/100,$10::numeric/100,$9::numeric/100,$8,$9,$10,$9,
		           $11,`+del+`,$12,'8518',$13)`,
			id, order, product, variant, ctSeller, title, sku, mrp, unit, tax, status, ctTax18, created)
	}
	// ₹1,299 inclusive of 18% GST → tax 19815, plus ₹49 shipping → 134800.
	order(ctOConfirmed, "ORD-2026-010001", "confirmed", "paid", 129900, 19815, 134800, "2026-09-10T10:00:00Z")
	item(ctIConfirmed, ctOConfirmed, ctP1, ctV1, "Momentum Wireless Earbuds", "MOM-EB-BLK", 149900, 129900, 19815, "confirmed", "", "2026-09-10T10:00:00Z")
	e.exec(`INSERT INTO order_status_history (order_id,from_status,to_status,actor_type,notes,created_at) VALUES
	        ($1,NULL,'payment_pending','customer','checkout','2026-09-10T10:00:00Z'),
	        ($1,'payment_pending','confirmed','system','payment.succeeded','2026-09-10T10:01:00Z')`, ctOConfirmed)

	order(ctODelivered, "ORD-2026-010002", "delivered", "paid", 129900, 19815, 134800, "2026-09-11T10:00:00Z")
	item(ctIDelivered, ctODelivered, ctP1, ctV1, "Momentum Wireless Earbuds", "MOM-EB-BLK", 149900, 129900, 19815, "delivered", "2026-09-14T15:00:00Z", "2026-09-11T10:00:00Z")
	item(ctIDelivered2, ctODelivered, ctP1, ctV1b, "Momentum Wireless Earbuds", "MOM-EB-WHT", 149900, 129900, 19815, "delivered", "2026-09-14T15:00:00Z", "2026-09-11T10:00:01Z")
	e.exec(`INSERT INTO shipments (id,order_id,seller_id,courier,tracking_number,courier_order_id,label_url,tracking_url,status,eta,
	           shipped_at,delivered_at,last_event_at,created_at,updated_at)
	        VALUES ($1,$2,$3,'stub','CT0C0210','stub-CT0C0210','https://example.test/labels/CT0C0210.pdf',
	           'https://example.test/track/CT0C0210','delivered','2026-09-14T10:00:00Z',
	           '2026-09-11T12:00:00Z','2026-09-14T15:00:00Z','2026-09-14T15:00:00Z','2026-09-11T12:00:00Z','2026-09-14T15:00:00Z')`,
		ctShipment, ctODelivered, ctSeller)
	e.exec(`INSERT INTO shipment_events (shipment_id,status,location,remark,occurred_at,created_at) VALUES
	        ($1,'booked','Bengaluru','booked with stub','2026-09-11T12:00:00Z','2026-09-11T12:00:00Z'),
	        ($1,'in_transit','Bengaluru hub','left origin','2026-09-12T09:00:00Z','2026-09-12T09:00:00Z'),
	        ($1,'delivered','Bengaluru','handed to the customer','2026-09-14T15:00:00Z','2026-09-14T15:00:00Z')`, ctShipment)
	e.exec(`UPDATE order_items SET shipment_id = $2, tracking_number = 'CT0C0210' WHERE order_id = $1`, ctODelivered, ctShipment)
	e.exec(`INSERT INTO invoices (id,order_id,invoice_number,financial_year,sequence,seller_id,buyer_user_id,grand_total,
	           is_interstate,cgst_total,sgst_total,igst_total,html_media_key,issued_at,created_at)
	        VALUES ($1,$2,'INV-26-27-000002','2026-27',2,$3,$4,1348.00,FALSE,99.08,99.07,0,
	           'invoices/2026-27/INV-26-27-000002.html','2026-09-11T10:02:00Z','2026-09-11T10:02:00Z')`,
		ctInvoice, ctODelivered, ctSeller, ctBuyer)
	e.exec(`INSERT INTO reviews (id,product_id,seller_id,order_item_id,reviewer_id,rating,title,body,is_verified_purchase,created_at,updated_at)
	        VALUES ($1,$2,$3,$4,$5,5,'Great sound','Battery lasts all day.',TRUE,'2026-09-15T10:00:00Z','2026-09-15T10:00:00Z')`,
		ctReview, ctP1, ctSeller, ctIDelivered2, ctBuyer)
	// One helpful vote already on it, from another shopper.
	e.exec(`INSERT INTO review_votes (review_id,user_id,is_helpful,created_at) VALUES ($1,$2,TRUE,'2026-09-16T10:00:00Z')`,
		ctReview, ctOtherSellerU)
	e.exec(`UPDATE reviews SET helpful_count = 1 WHERE id = $1`, ctReview)
	e.exec(`INSERT INTO order_status_history (order_id,from_status,to_status,actor_type,notes,created_at) VALUES
	        ($1,NULL,'payment_pending','customer','checkout','2026-09-11T10:00:00Z'),
	        ($1,'payment_pending','confirmed','system','payment.succeeded','2026-09-11T10:01:00Z'),
	        ($1,'confirmed','shipped','system','shipment booked','2026-09-11T12:00:00Z'),
	        ($1,'shipped','delivered','system','all shipments delivered','2026-09-14T15:00:00Z')`, ctODelivered)

	order(ctOFailed, "ORD-2026-010003", "payment_failed", "failed", 129900, 19815, 134800, "2026-09-12T10:00:00Z")
	item(ctIFailed, ctOFailed, ctP1, ctV1, "Momentum Wireless Earbuds", "MOM-EB-BLK", 149900, 129900, 19815, "confirmed", "", "2026-09-12T10:00:00Z")

	order(ctORefund, "ORD-2026-010004", "cancelled", "refund_pending", 129900, 19815, 134800, "2026-09-13T10:00:00Z")
	item(ctIRefund, ctORefund, ctP1, ctV1, "Momentum Wireless Earbuds", "MOM-EB-BLK", 149900, 129900, 19815, "cancelled", "", "2026-09-13T10:00:00Z")
	e.exec(`UPDATE orders SET cancellation_reason = 'changed my mind', cancelled_by = 'customer' WHERE id = $1`, ctORefund)

	order(ctOPending, "ORD-2026-010005", "payment_pending", "pending", 129900, 19815, 134800, "2026-09-14T10:00:00Z")
	item(ctIPending, ctOPending, ctP1, ctV1, "Momentum Wireless Earbuds", "MOM-EB-BLK", 149900, 129900, 19815, "confirmed", "", "2026-09-14T10:00:00Z")
	e.exec(`INSERT INTO inventory_reservations (variant_id,order_id,user_id,quantity,type,expires_at,created_at)
	        VALUES ($1,$2,$3,1,'checkout','2026-09-14T10:20:00Z','2026-09-14T10:00:00Z')`, ctV1, ctOPending, ctBuyer)
	e.exec(`UPDATE inventory_items SET reserved_qty = 1 WHERE variant_id = $1`, ctV1)

	// ── Coupons (migration 038) ──────────────────────────────────────────
	// Fixed ids, codes and timestamps. created_at descends in seed order so
	// the seller's list (newest first) is stable.
	coupon := func(id uuid.UUID, seller *uuid.UUID, code, desc, typ string, bps, value, maxDisc, minOrder *int64,
		maxUses *int, uses int, scope string, ids []uuid.UUID, expires string, public bool, created string) {
		var bpsV, valV *int64
		legacy := 0.0
		if typ == "percentage" {
			bpsV, legacy = bps, float64(*bps)/100
		} else {
			valV, legacy = value, float64(*value)/100
		}
		var exp any
		if expires != "" {
			exp = expires
		}
		var d any
		if desc != "" {
			d = desc
		}
		if ids == nil {
			ids = []uuid.UUID{}
		}
		minO := int64(0)
		if minOrder != nil {
			minO = *minOrder
		}
		e.exec(`INSERT INTO coupons (id, seller_id, code, description, discount_type, discount_value,
		           discount_basis_points, discount_value_minor, max_discount_amount_minor, min_order_amount_minor,
		           max_uses, uses_count, max_uses_per_user, applicable_to, applicable_ids, is_active, is_public,
		           starts_at, expires_at, created_at, updated_at)
		        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,$13,$14,TRUE,$15,
		           '2026-09-01T00:00:00Z',$16::timestamptz,$17::timestamptz,$17::timestamptz)`,
			id, seller, code, d, typ, legacy, bpsV, valV, maxDisc, minO, maxUses, uses, scope, ids, public, exp, created)
	}
	i64 := func(v int64) *int64 { return &v }
	one := 1
	coupon(ctCpnPct, &ctSeller, "MOMENTUM10", "10% off everything at Momentum Electronics", "percentage",
		i64(1000), nil, i64(20000), nil, nil, 0, "all", nil, "2027-03-31T18:29:59Z", true, "2026-09-20T10:09:00Z")
	coupon(ctCpnEarbuds, &ctSeller, "EARBUDS150", "", "flat", nil, i64(15000), nil, nil, nil, 0,
		"product", []uuid.UUID{ctP1}, "", true, "2026-09-20T10:08:00Z")
	coupon(ctCpnSecret, &ctSeller, "VIPSECRET", "For our loyal customers", "flat", nil, i64(50000), nil, nil, nil, 0,
		"all", nil, "", false, "2026-09-20T10:07:00Z")
	coupon(ctCpnBigSpend, &ctSeller, "BIGSPEND", "", "flat", nil, i64(30000), nil, i64(500000), nil, 0,
		"all", nil, "", true, "2026-09-20T10:06:00Z")
	coupon(ctCpnExpired, &ctSeller, "EXPIRED5", "", "percentage", i64(500), nil, nil, nil, nil, 0,
		"all", nil, "2026-09-15T18:29:59Z", true, "2026-09-20T10:05:00Z")
	coupon(ctCpnUsedUp, &ctSeller, "USEDUP", "", "flat", nil, i64(1000), nil, nil, &one, 1,
		"all", nil, "", true, "2026-09-20T10:04:00Z")
	coupon(ctCpnLastUse, &ctSeller, "ONEUSELEFT", "", "flat", nil, i64(2000), nil, nil, &one, 0,
		"all", nil, "", true, "2026-09-20T10:03:00Z")
	coupon(ctCpnOther, &ctOtherSeller, "KETTLE20", "", "percentage", i64(2000), nil, nil, nil, nil, 0,
		"all", nil, "", true, "2026-09-20T10:02:00Z")
	coupon(ctCpnPlatform, nil, "MSTORE50", "₹50 off your first MStore order", "flat", nil, i64(5000), nil, nil, nil, 0,
		"all", nil, "", true, "2026-09-20T10:01:00Z")

	// A paid order that carried a coupon AND a bank offer: ₹1,299 − ₹129.90
	// (MOMENTUM10) + ₹49 shipping = ₹1,218.10, of which the buyer paid
	// ₹1,096.29 after a 10% HDFC bank offer of ₹121.81. GST is on the
	// coupon-discounted value; the bank offer does not touch it.
	e.exec(`INSERT INTO orders (id,customer_user_id,order_number,subtotal,shipping_charges,tax_amount,final_amount,coupon_discount,
	           subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor,coupon_code,coupon_discount_minor,
	           payment_method,payment_status,status,delivery_address_id,delivery_address_snapshot,
	           place_of_supply_state,cgst_minor,sgst_minor,igst_minor,
	           offer_id,offer_title,offer_discount_minor,offer_funded_by,captured_minor,created_at,updated_at)
	        VALUES ($1,$2,'ORD-2026-009999',1299.00,49.00,185.82,1218.10,129.90,
	           129900,4900,18582,121810,'MOMENTUM10',12990,
	           'card','paid','confirmed',$3,$4::jsonb,
	           'KA',9291,9291,0,
	           $5,'10% off with HDFC Bank credit cards',12181,'bank',109629,
	           '2026-09-09T10:00:00Z','2026-09-09T10:01:00Z')`,
		ctOOffer, ctBuyer, ctBuyerAddr, snapshot, ctBankOffer)
	e.exec(`INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,product_title,variant_details,sku,quantity,
	           unit_mrp,unit_price,tax_amount,final_price,unit_mrp_minor,unit_price_minor,tax_amount_minor,final_price_minor,
	           allocated_discount_minor,allocated_shipping_minor,net_inclusive_minor,taxable_minor,
	           cgst_minor,sgst_minor,igst_minor,tax_rate_bp,status,tax_class_id,hsn_code,created_at)
	        VALUES ($1,$2,$3,$4,$5,'Momentum Wireless Earbuds','{"option_1_name":"Colour","option_1_value":"Black"}','MOM-EB-BLK',1,
	           1499.00,1299.00,185.82,1218.10,149900,129900,18582,121810,
	           12990,4900,121810,103228,
	           9291,9291,0,1800,'confirmed',$6,'8518','2026-09-09T10:00:00Z')`,
		ctIOffer, ctOOffer, ctP1, ctV1, ctSeller, ctTax18)

	// Fresh orders continue the seeded series.
	e.exec(`ALTER SEQUENCE order_number_seq RESTART WITH 10006`)
}

// ─── The fixtures, in the order they must run ────────────────────────────

func (e *contractEnv) fixtures() []ctFixture {
	var (
		quoteID    string
		quoteTotal int64
		freshOrder string
		freshProd  string
		// The coupons the seller and the admin console create.
		freshCoupon, freshPlatformCoupon string
		// The stub intent opened on the seeded payment_pending order.
		stubIntentID, stubProviderRef string
	)
	// quote takes a fresh quote for the buyer's bag and remembers it.
	quote := func(e *contractEnv) *httptest.ResponseRecorder {
		w := e.post("/v1/commerce/checkout/quote", ctBuyer, map[string]any{
			"address_id": ctBuyerAddr.String(), "payment_method": "upi",
		})
		if w.Code == http.StatusOK {
			var env struct {
				Data struct {
					QuoteID    string `json:"quote_id"`
					TotalMinor int64  `json:"total_minor"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			quoteID, quoteTotal = env.Data.QuoteID, env.Data.TotalMinor
		}
		return w
	}
	checkout := func(e *contractEnv, idem string, total int64) *httptest.ResponseRecorder {
		headers := map[string]string{}
		if idem != "" {
			headers["Idempotency-Key"] = idem
		}
		return e.do(ctReq{method: http.MethodPost, path: "/v1/commerce/v2/orders/checkout", user: ctBuyer, headers: headers,
			body: map[string]any{
				"address_id": ctBuyerAddr.String(), "quote_id": quoteID, "payment_method": "upi",
				"expected_total_minor": total,
			}})
	}

	return []ctFixture{
		// ── storefront ─────────────────────────────────────────────────
		{name: "storefront/home_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/home", ctBuyer)
		}},
		{name: "storefront/categories_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/categories", uuid.Nil)
		}},
		{name: "storefront/categories_tree_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/categories?tree=true", uuid.Nil)
		}},
		{name: "storefront/products_list_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products?limit=20", ctBuyer)
		}},
		{name: "storefront/product_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String(), ctBuyer)
		}},
		// Engagement (shop-engagement contract §1, §2, §4). The clock is
		// pinned to Wed 30 Sep 2026 15:30 IST; the seller saved no SLA
		// (2 dispatch days: Thu, Fri) and the courier answers 3 days.
		{name: "storefront/delivery_estimate_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/delivery-estimate?pincode=500081", uuid.Nil)
		}},
		{name: "storefront/delivery_estimate_200_not_serviceable", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/delivery-estimate?pincode=999999", uuid.Nil)
		}},
		{name: "storefront/delivery_estimate_400_pincode_required", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/delivery-estimate", uuid.Nil)
		}},
		{name: "storefront/product_reaction_put_200_like", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPut, path: "/v1/commerce/products/" + ctP1.String() + "/reaction",
				user: ctBuyer, body: map[string]any{"kind": "like"}})
		}},
		{name: "storefront/product_reaction_put_200_dislike", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPut, path: "/v1/commerce/products/" + ctP1.String() + "/reaction",
				user: ctBuyer, body: map[string]any{"kind": "dislike"}})
		}},
		{name: "storefront/product_reaction_delete_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodDelete, path: "/v1/commerce/products/" + ctP1.String() + "/reaction", user: ctBuyer})
		}},
		{name: "storefront/product_share_post_204", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/products/"+ctP1.String()+"/share", uuid.Nil, map[string]any{"channel": "whatsapp"})
		}},
		{name: "storefront/product_get_404", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctUnknownProduct.String(), ctBuyer)
		}},
		{name: "storefront/product_media_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/media", uuid.Nil)
		}},
		{name: "storefront/product_variants_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/variants", uuid.Nil)
		}},
		{name: "storefront/product_attributes_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/attributes", uuid.Nil)
		}},
		{name: "storefront/product_reviews_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+ctP1.String()+"/reviews", uuid.Nil)
		}},
		{name: "storefront/attribute_schema_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/categories/"+ctCatElectronics.String()+"/attribute-schema", uuid.Nil)
		}},
		{name: "storefront/tax_classes_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/tax-classes", uuid.Nil)
		}},
		{name: "storefront/favourites_list_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/favourites", ctBuyer)
		}},
		{name: "storefront/favourite_post_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/favourites", ctBuyer, map[string]any{"product_id": ctP2.String()})
		}},
		// Bank offers for the product page: payments' registry, filtered by
		// the amount, each with what it would save on it.
		{name: "storefront/payment_offers_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/payment-offers?amount_minor=129900", uuid.Nil)
		}},
		{name: "storefront/payment_offers_get_400_amount_required", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/payment-offers", uuid.Nil)
		}},

		// ── bag ────────────────────────────────────────────────────────
		{name: "bag/cart_get_200_empty", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/cart", ctStranger)
		}},
		{name: "bag/cart_item_post_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/cart/items", ctBuyer, map[string]any{"variant_id": ctV1.String(), "quantity": 2})
		}},
		{name: "bag/cart_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/cart", ctBuyer)
		}},
		// The bag's coupons: P1 ×2 and P2 (P3 is paused and skipped), so
		// MOMENTUM10 hits its ₹200 cap, EARBUDS150 applies to P1 only, and
		// BIGSPEND explains its ₹5,000 minimum. Secret, expired, used-up,
		// other-shop and platform coupons are not listed.
		{name: "bag/cart_coupons_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/cart/coupons", ctBuyer)
		}},
		{name: "bag/cart_coupons_get_401_unauthenticated", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/cart/coupons", uuid.Nil)
		}},
		{name: "bag/cart_item_post_409_multiple_sellers", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/cart/items", ctBuyer, map[string]any{"variant_id": ctV5.String(), "quantity": 1})
		}},
		{name: "bag/cart_item_post_409_out_of_stock", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/cart/items", ctBuyer, map[string]any{"variant_id": ctV1b.String(), "quantity": 1})
		}},
		{name: "bag/cart_item_post_409_unavailable", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/cart/items", ctBuyer, map[string]any{"variant_id": ctV3.String(), "quantity": 1})
		}},

		// ── addresses ──────────────────────────────────────────────────
		{name: "addresses/addresses_list_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/addresses", ctBuyer)
		}},
		{name: "addresses/address_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/addresses", ctBuyer, map[string]any{
				"label": "Parents", "contact_name": "Ravi Buyer", "phone": "9222222222",
				"address_line_1": "12 Lake View", "address_line_2": "2nd floor", "landmark": "Opposite the temple",
				"city": "Mysuru", "state": "KA", "postal_code": "570001", "address_type": "other", "is_default": false,
			})
		}},

		// ── orders (seeded; read before the checkout chain adds one) ───
		{name: "orders/orders_list_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders?limit=2", ctBuyer)
		}},
		{name: "orders/order_get_200_confirmed", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOConfirmed.String(), ctBuyer)
		}},
		{name: "orders/order_get_200_delivered", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctODelivered.String(), ctBuyer)
		}},
		{name: "orders/order_get_200_payment_failed", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOFailed.String(), ctBuyer)
		}},
		// A coupon (in discount_minor, coupon_code) and a bank offer
		// (payment_offer, amount_paid_minor) on one paid order.
		{name: "orders/order_get_200_bank_offer", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOOffer.String(), ctBuyer)
		}},
		{name: "orders/order_items_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOConfirmed.String()+"/items", ctBuyer)
		}},
		{name: "orders/order_shipments_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctODelivered.String()+"/shipments", ctBuyer)
		}},
		{name: "orders/order_invoice_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctODelivered.String()+"/invoice", ctBuyer)
		}},

		// ── payment reads on the seeded orders ─────────────────────────
		{name: "payment/order_payment_get_200_confirming", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOPending.String()+"/payment", ctBuyer)
		}},
		{name: "payment/order_payment_get_200_paid", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOConfirmed.String()+"/payment", ctBuyer)
		}},
		{name: "payment/order_payment_get_200_paid_refund_pending", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctORefund.String()+"/payment", ctBuyer)
		}},
		{name: "payment/order_payment_get_200_failed", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOFailed.String()+"/payment", ctBuyer)
		}},
		{name: "payment/order_payment_get_403_not_customer", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOConfirmed.String()+"/payment", ctStranger)
		}},
		{name: "payment/payment_intent_post_200_stub", run: func(e *contractEnv) *httptest.ResponseRecorder {
			e.payments.session = "stub"
			defer func() { e.payments.session = "razorpay" }()
			w := e.post("/v1/commerce/orders/"+ctOPending.String()+"/payment/intent", ctBuyer, nil)
			var env struct {
				Data struct {
					ID          string `json:"payment_intent_id"`
					ProviderRef string `json:"provider_ref"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			stubIntentID, stubProviderRef = env.Data.ID, env.Data.ProviderRef
			return w
		}},
		// Dev only: the stub settlement, with the body the web actually
		// sends (every field bound required). Registered only under
		// PAYMENTS_ALLOW_STUB; settles because payments named the stub.
		{name: "payment/payment_confirm_post_204_stub", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/orders/"+ctOPending.String()+"/payment/confirm", ctBuyer, map[string]any{
				"payment_intent_id": stubIntentID, "razorpay_order_id": stubProviderRef,
				"razorpay_payment_id": "pay_stub_0001", "razorpay_signature": "stub-signature",
				"amount_minor": 134800, "gateway": "stub",
			})
		}},
		{name: "payment/order_payment_get_200_paid_after_stub_confirm", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+ctOPending.String()+"/payment", ctBuyer)
		}},

		// ── reviews ────────────────────────────────────────────────────
		{name: "reviews/review_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/products/"+ctP1.String()+"/reviews", ctBuyer, map[string]any{
				"seller_id": ctSeller.String(), "order_item_id": ctIDelivered.String(),
				"rating": 4, "title": "Solid pair", "body": "Comfortable for long calls.",
			})
		}},
		{name: "reviews/review_post_400_not_delivered", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/products/"+ctP1.String()+"/reviews", ctBuyer, map[string]any{
				"seller_id": ctSeller.String(), "order_item_id": ctIConfirmed.String(), "rating": 3,
			})
		}},
		// Helpful votes (contract §3): another shopper marks the seeded
		// review helpful; its author may not vote on it.
		{name: "reviews/review_vote_put_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPut, path: "/v1/commerce/reviews/" + ctReview.String() + "/vote",
				user: ctStranger, body: map[string]any{"vote": "helpful"}})
		}},
		{name: "reviews/review_vote_put_403_own", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPut, path: "/v1/commerce/reviews/" + ctReview.String() + "/vote",
				user: ctBuyer, body: map[string]any{"vote": "helpful"}})
		}},

		// ── checkout chain ─────────────────────────────────────────────
		// The bag is reduced to the sellable line first: a quote refuses a
		// paused product, and the moved price is exercised separately below.
		{name: "checkout/quote_post_400_payment_method", run: func(e *contractEnv) *httptest.ResponseRecorder {
			e.exec(`DELETE FROM cart_items WHERE cart_id = $1 AND variant_id IN ($2,$3)`, ctCart, ctV2, ctV3)
			return e.post("/v1/commerce/checkout/quote", ctBuyer, map[string]any{
				"address_id": ctBuyerAddr.String(), "payment_method": "cod",
			})
		}},
		{name: "checkout/quote_post_422_not_serviceable", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/checkout/quote", ctBuyer, map[string]any{
				"address_id": ctBuyerAddrFar.String(), "payment_method": "upi",
			})
		}},
		{name: "checkout/quote_post_200", run: quote},
		// ── coupons at the quote (bag: P1 ×2 = ₹2,598) ──────────────────
		// Typed lower-case to show the code is normalised; 10% is ₹259.80,
		// capped at ₹200.
		{name: "checkout/quote_post_200_coupon", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "momentum10")
		}},
		{name: "checkout/quote_post_422_coupon_invalid", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "NOSUCHCODE")
		}},
		{name: "checkout/quote_post_422_coupon_expired", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "EXPIRED5")
		}},
		{name: "checkout/quote_post_422_coupon_min_order", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "BIGSPEND")
		}},
		{name: "checkout/quote_post_422_coupon_used_up", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "USEDUP")
		}},
		{name: "checkout/quote_post_422_coupon_not_applicable", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "KETTLE20")
		}},
		{name: "checkout/quote_post_422_coupon_not_available", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return quoteWithCoupon(e, "MSTORE50")
		}},
		// The claim at checkout answers the same 422 vocabulary: the last use
		// of ONEUSELEFT is taken between this buyer's quote and checkout.
		{name: "checkout/checkout_v2_post_422_coupon_used_up", run: func(e *contractEnv) *httptest.ResponseRecorder {
			w := quoteWithCoupon(e, "ONEUSELEFT")
			var env struct {
				Data struct {
					QuoteID    string `json:"quote_id"`
					TotalMinor int64  `json:"total_minor"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			e.exec(`UPDATE coupons SET uses_count = 1 WHERE id = $1`, ctCpnLastUse)
			return e.do(ctReq{method: http.MethodPost, path: "/v1/commerce/v2/orders/checkout", user: ctBuyer,
				headers: map[string]string{"Idempotency-Key": "contract-checkout-coupon"},
				body: map[string]any{
					"address_id": ctBuyerAddr.String(), "quote_id": env.Data.QuoteID, "payment_method": "upi",
					"coupon_code": "ONEUSELEFT", "expected_total_minor": env.Data.TotalMinor,
				}})
		}},
		{name: "checkout/checkout_v2_post_400_missing_idempotency_key", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return checkout(e, "", quoteTotal)
		}},
		{name: "checkout/checkout_v2_post_409_quote_stale", run: func(e *contractEnv) *httptest.ResponseRecorder {
			// The bag changed after the quote was taken.
			e.exec(`UPDATE cart_items SET quantity = 1 WHERE cart_id = $1 AND variant_id = $2`, ctCart, ctV1)
			return checkout(e, "contract-checkout-stale", quoteTotal)
		}},
		{name: "checkout/checkout_v2_post_409_price_changed", run: func(e *contractEnv) *httptest.ResponseRecorder {
			// The catalogue price moved after the line was added.
			e.exec(`UPDATE cart_items SET price_snapshot_minor = 119900, price_snapshot = 1199 WHERE cart_id = $1 AND variant_id = $2`, ctCart, ctV1)
			quote(e)
			w := checkout(e, "contract-checkout-moved", quoteTotal)
			e.exec(`UPDATE cart_items SET price_snapshot_minor = 129900, price_snapshot = 1299 WHERE cart_id = $1 AND variant_id = $2`, ctCart, ctV1)
			return w
		}},
		{name: "checkout/checkout_v2_post_409_amount_mismatch", run: func(e *contractEnv) *httptest.ResponseRecorder {
			quote(e)
			return checkout(e, "contract-checkout-mismatch", quoteTotal-100)
		}},
		{name: "checkout/checkout_v2_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			quote(e)
			w := checkout(e, "contract-checkout-ok", quoteTotal)
			var env struct {
				Data struct {
					OrderID string `json:"order_id"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			freshOrder = env.Data.OrderID
			return w
		}},
		{name: "payment/payment_intent_post_200_razorpay", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/orders/"+freshOrder+"/payment/intent", ctBuyer, nil)
		}},
		{name: "payment/payment_status_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/orders/"+freshOrder+"/payment/status", ctBuyer)
		}},
		{name: "orders/order_cancel_post_204", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/orders/"+freshOrder+"/cancel", ctBuyer, map[string]any{"reason": "ordered by mistake"})
		}},
		{name: "orders/order_cancel_post_409_not_permitted", run: func(e *contractEnv) *httptest.ResponseRecorder {
			// Delivered: the matrix has no customer row out of it.
			return e.post("/v1/commerce/orders/"+ctODelivered.String()+"/cancel", ctBuyer, map[string]any{"reason": "too late"})
		}},
		{name: "orders/order_cancel_post_404_not_owner", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/orders/"+ctOConfirmed.String()+"/cancel", ctStranger, map[string]any{"reason": "not mine"})
		}},

		// ── seller ─────────────────────────────────────────────────────
		{name: "seller/onboarding_status_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/onboarding/status", ctSellerUser)
		}},
		{name: "seller/onboarding_readiness_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/onboarding/readiness", ctSellerUser)
		}},
		{name: "seller/onboarding_submit_409_incomplete", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/onboarding/submit", ctDraftUser, nil)
		}},
		{name: "seller/sellers_me_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/sellers/me", ctSellerUser)
		}},
		{name: "seller/seller_products_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/products", ctSellerUser)
		}},
		{name: "seller/product_post_400_tax_class_required", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/products", ctSellerUser, map[string]any{
				"title": "Momentum Power Bank", "category_id": ctCatElectronics.String(),
				"variants": []map[string]any{{"sku": "MOM-PB-10K", "mrp_minor": 199900, "selling_price_minor": 149900, "stock_qty": 10}},
			})
		}},
		{name: "seller/product_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			w := e.post("/v1/commerce/products", ctSellerUser, map[string]any{
				"title": "Momentum Power Bank 10000", "description": "10,000 mAh, USB-C in and out.",
				"category_id": ctCatElectronics.String(), "tax_class_id": ctTax18.String(), "hsn_code": "8507",
				"weight_grams": 220, "length_cm": 14, "width_cm": 7, "height_cm": 1.5,
				"primary_image_media_id": ctMediaNew.String(), "return_policy_type": "7_days", "return_policy_days": 7,
				"variants": []map[string]any{
					{"sku": "MOM-PB-10K-BLK", "option_1_name": "Colour", "option_1_value": "Black",
						"mrp_minor": 199900, "selling_price_minor": 149900, "stock_qty": 10},
				},
			})
			var env struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			freshProd = env.Data.ID
			return w
		}},
		{name: "seller/product_readiness_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/products/"+freshProd+"/readiness", ctSellerUser)
		}},
		{name: "seller/product_submit_post_204", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/products/"+freshProd+"/submit", ctSellerUser, nil)
		}},
		{name: "seller/seller_variant_stock_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/variants/"+ctV1.String()+"/stock", ctSellerUser)
		}},
		{name: "seller/seller_orders_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/orders?limit=3", ctSellerUser)
		}},
		{name: "seller/seller_order_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/orders/"+ctOConfirmed.String(), ctSellerUser)
		}},
		{name: "seller/seller_order_history_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/orders/"+ctOConfirmed.String()+"/history", ctSellerUser)
		}},
		{name: "seller/seller_order_pack_post_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/seller/orders/"+ctOConfirmed.String()+"/pack", ctSellerUser, nil)
		}},
		{name: "seller/seller_order_ship_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/seller/orders/"+ctOConfirmed.String()+"/ship", ctSellerUser, map[string]any{})
		}},

		// ── seller coupons ─────────────────────────────────────────────
		{name: "seller/seller_coupons_get_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/coupons", ctSellerUser)
		}},
		{name: "seller/seller_coupons_get_409_not_approved", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.get("/v1/commerce/seller/coupons", ctDraftUser)
		}},
		{name: "seller/seller_coupon_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			w := e.post("/v1/commerce/seller/coupons", ctSellerUser, map[string]any{
				"code": "newyear 25", "description": "25% off the canvas cap", "discount_type": "percentage",
				"discount_value": 2500, "max_discount_minor": 10000, "min_order_minor": 40000,
				"max_uses": 500, "max_uses_per_user": 1, "applicable_to": "product",
				"applicable_ids": []string{ctP2.String()}, "expires_at": "2027-01-15T18:29:59Z", "is_public": true,
			})
			var env struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			freshCoupon = env.Data.ID
			return w
		}},
		{name: "seller/seller_coupon_post_409_code_taken", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/seller/coupons", ctSellerUser, map[string]any{
				"code": "momentum10", "discount_type": "flat", "discount_value": 1000,
			})
		}},
		{name: "seller/seller_coupon_post_422_product_not_owned", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/seller/coupons", ctSellerUser, map[string]any{
				"code": "KETTLEGRAB", "discount_type": "flat", "discount_value": 1000,
				"applicable_to": "product", "applicable_ids": []string{ctP5.String()},
			})
		}},
		{name: "seller/seller_coupon_post_400_invalid_body", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.post("/v1/commerce/seller/coupons", ctSellerUser, map[string]any{
				"code": "AB!", "discount_type": "flat", "discount_value": 1000,
			})
		}},
		{name: "seller/seller_coupon_patch_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPatch, path: "/v1/commerce/seller/coupons/" + freshCoupon,
				user: ctSellerUser, body: map[string]any{"description": "25% off the canvas cap, this winter", "max_uses": 100}})
		}},
		// An explicit null CLEARS: no total limit, no end date.
		{name: "seller/seller_coupon_patch_200_clear", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPatch, path: "/v1/commerce/seller/coupons/" + freshCoupon,
				user: ctSellerUser, body: map[string]any{"max_uses": nil, "expires_at": nil}})
		}},
		{name: "seller/seller_coupon_patch_422_immutable", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPatch, path: "/v1/commerce/seller/coupons/" + freshCoupon,
				user: ctSellerUser, body: map[string]any{"discount_value": 3000}})
		}},
		{name: "seller/seller_coupon_patch_404_not_owner", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.do(ctReq{method: http.MethodPatch, path: "/v1/commerce/seller/coupons/" + ctCpnPct.String(),
				user: ctOtherSellerU, body: map[string]any{"is_active": false}})
		}},

		// ── admin console: coupons (admin-service token) ───────────────
		{name: "admin/coupons_get_200_platform", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodGet, "/v1/commerce/internal/admin/coupons?funded_by=platform&limit=100", PermCouponsManage, nil)
		}},
		{name: "admin/coupons_get_200_seller", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodGet, "/v1/commerce/internal/admin/coupons?funded_by=seller&limit=3", PermCouponsManage, nil)
		}},
		{name: "admin/coupons_get_400_invalid_filter", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodGet, "/v1/commerce/internal/admin/coupons?funded_by=bank", PermCouponsManage, nil)
		}},
		{name: "admin/coupons_get_403_permission", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodGet, "/v1/commerce/internal/admin/coupons", PermStatsRead, nil)
		}},
		{name: "admin/coupon_post_201", run: func(e *contractEnv) *httptest.ResponseRecorder {
			w := e.adminDo(http.MethodPost, "/v1/commerce/internal/admin/coupons", PermCouponsManage, map[string]any{
				"code": "FESTIVE200", "description": "₹200 off orders above ₹1,000", "discount_type": "flat",
				"discount_value": 20000, "min_order_minor": 100000, "max_uses_per_user": 1, "applicable_to": "all",
				"expires_at": "2026-11-15T18:29:59Z", "is_public": true, "is_active": true, "reason": "Diwali campaign",
			})
			var env struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			freshPlatformCoupon = env.Data.ID
			return w
		}},
		{name: "admin/coupon_post_409_code_taken", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodPost, "/v1/commerce/internal/admin/coupons", PermCouponsManage, map[string]any{
				"code": "MSTORE50", "discount_type": "flat", "discount_value": 5000,
			})
		}},
		{name: "admin/coupon_patch_200", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodPatch, "/v1/commerce/internal/admin/coupons/"+freshPlatformCoupon, PermCouponsManage,
				map[string]any{"is_active": false, "reason": "campaign paused"})
		}},
		{name: "admin/coupon_patch_403_seller_read_only", run: func(e *contractEnv) *httptest.ResponseRecorder {
			return e.adminDo(http.MethodPatch, "/v1/commerce/internal/admin/coupons/"+ctCpnPct.String(), PermCouponsManage,
				map[string]any{"is_active": false})
		}},
	}
}

// quoteWithCoupon takes a quote for the buyer's bag with a coupon code, as
// the checkout screen does after "Apply".
func quoteWithCoupon(e *contractEnv, code string) *httptest.ResponseRecorder {
	return e.post("/v1/commerce/checkout/quote", ctBuyer, map[string]any{
		"address_id": ctBuyerAddr.String(), "payment_method": "upi", "coupon_code": code,
	})
}
