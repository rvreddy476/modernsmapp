# food-service scripts

## dev-seed-feast.sh — dev-only Feast seeder

Seeds the local dev stack so Feast Kitchen and Feast Rider can be tested on
phones without going through onboarding by hand. It talks to food-service
directly (`http://localhost:8113`) with the internal key and the identity
headers the api-gateway would inject (`X-User-Id`, and `X-Scopes: admin` on
admin routes). It never logs in, never registers accounts and never prints a
key, token, DigiLocker code/state or the webhook signature.

It refuses to run unless food-service is on localhost and the container's
`ENV` is `local`, `dev` or `development`.

### What it does

1. **Restaurant** "Feast Test Kitchen" for the owner (created once, reused on
   every later run): Bengaluru location with a 10 km radius, open all day every
   day, `RESTAURANT_STANDALONE` compliance with the synthetic PAN `ZZZPZ0000Z`,
   FSSAI `10099999000000` with an expiry one year out, a synthetic payout
   account, 2 categories and 4 dishes (Chicken Biryani has Half/Full variants
   and a Sides add-on group; Paneer Butter Masala has a Breads add-on group).
   It submits, approves the FSSAI document and the restaurant through the admin
   routes, and ends ACTIVE and accepting orders.
2. **Rider** profile for the rider: motorcycle, DigiLocker through the dev mock
   (start → dev authorize → callback), a DL and RC with synthetic numbers, a
   selfie document and a payout account; admin approves the documents and the
   partner, then sets the partner ACTIVE. The rider is **not** put online.
3. **Order** for the customer: a delivery address ~2.5 km away, a cart with two
   dishes (Biryani Full + Raita, and Paneer Tikka), an order paid by `upi`, a
   payment intent, then a signed Razorpay test-mode `payment.captured` webhook
   to payments-service. payments-service publishes `payment.succeeded` on
   `social.events.v1` and food-service's consumer marks the order paid and
   CONFIRMED. The script polls `GET /v1/food/orders/:id/payment` until `paid`.
4. Prints the restaurant, rider and order summary and the next manual steps.

### Parked refunds from earlier runs

The capture is simulated. Razorpay never saw the payment, so a refund of a
seeded order can never succeed. A seeded order the kitchen does not accept
within the accept window (900 s for the seeded kitchen) is auto-rejected. The
paid order then requests a refund, and payments-service parks it as
`needs_attention`, which fires `PaymentsRefundNeedsAttention`.

So on **every** run (full, `--order-only` and `--cleanup-only`), before placing
a new order, the script lists payments' parked refunds for `food_order`
references (`GET /v1/payments/internal/refunds/needs-attention`). It resolves
the ones that are its own as `test_data`, with the note
`dev seed: simulated payment` (`POST …/refunds/:commandId/resolve`). A parked
refund counts as the seeder's only when **all** of these hold:

- the intent's payer is `--customer`;
- the food order (read as that customer) belongs to the seeded "Feast Test Kitchen";
- the captured payment id is the seeder's tag: `pay_devseed` followed by 7 hex characters, which is what every run since this change sends. The older form was 14 lowercase hex characters, which a real Razorpay id (mixed-case base62) practically never is.

Anything else is left alone and counted as "not this seeder's". The payments
internal key is read from the payments container inside the script and never
printed. It refuses to touch refunds unless payments-service is on Razorpay
test keys. The resolving actor (`X-User-Id`) is `--admin`.

The FSSAI document and the selfie point at an existing `public.media_assets`
row (looked up read-only); food-service only checks the id is a UUID.

### Usage

```bash
cd Architecture/services/food-service
bash scripts/dev-seed-feast.sh                 # full seed (idempotent)
bash scripts/dev-seed-feast.sh --order-only    # just a fresh paid order
bash scripts/dev-seed-feast.sh --cleanup-only  # just resolve this seeder's parked refunds
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--owner ID` | `call_a` (`2d598287-…`) | restaurant owner user id |
| `--rider ID` | `call_b` (`66668bc2-…`) | delivery partner user id |
| `--customer ID` | a `msgtest_*` user (`c3294f15-…`) | customer user id |
| `--admin ID` | another `msgtest_*` user (`d74639c3-…`) | actor recorded on admin approvals |
| `--order-only` | off | skip restaurant and rider; place a new order on the seeded restaurant |
| `--cleanup-only` | off | only resolve this seeder's parked refunds (see above), then exit |
| `--food-url URL` | `http://localhost:8113` | must be localhost |
| `--payments-url URL` | `http://localhost:8102` | must be localhost |

Requirements: Git Bash or any bash with `curl`, `openssl` and `docker`; the
`atpost_stack` containers running. No `jq` or Python needed (JSON is parsed by
the stack's `psql`, with no table access).

### Timing

An order is only held for the restaurant's accept SLA (`sla_accept_seconds`)
after it is paid. The script sets it to 900 s on the seeded kitchen (the
default is 180 s). The SLA job then auto-rejects the order, and its refund is
parked (see "Parked refunds from earlier runs"). The next run cleans it up. Run
`--order-only` right before testing acceptance on the Kitchen phone.

To run against the local stack:

```bash
cd /c/workspace/modernsmapp/Architecture/services/food-service
bash scripts/dev-seed-feast.sh --cleanup-only   # resolve earlier runs' parked refunds
bash scripts/dev-seed-feast.sh --order-only     # clean up, then place a fresh paid order
```
