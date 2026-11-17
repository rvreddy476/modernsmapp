# Commerce launch gate

**Verdict: NO-GO.** Gate lines 1, 2, 3, 7 and 8 fail, and any fail on lines 1 to 8 means NO-GO.

Findings are cited by their track or verifier IDs (COM-A to COM-F, COM-V3). Each one's `Was:` line in `03-findings.md` maps it to its final `com-N` number.

| Cited as | Final ID |
|---|---|
| COM-A-1 | com-1 |
| COM-C-1, COM-E-2 | com-2 |
| COM-B-1 | com-3 |
| COM-B-2 | com-4 |
| COM-E-4, COM-V3-1 | com-5 |
| COM-A-2 | com-6 |
| COM-B-4 | com-7 |
| COM-A-3 | com-8 |
| COM-B-3 | com-9 |
| COM-A-4 | com-10 |
| COM-C-2 | com-11 |
| COM-C-3 | com-12 |
| COM-E-1 | com-14 |
| COM-E-3 | com-15 |
| COM-D-3 | com-16 |
| COM-F-1 | com-18 |
| COM-F-3 | com-20 |
| COM-F-4 | com-21 |
| COM-A-13 | com-22 |

COM-V3-2 and the multi-seller invoice finding from `verify/v2` are P2; find them by their `Was:` lines.

| # | Gate line | Result | Evidence |
|---|---|---|---|
| 1 | Every core user journey works end to end | **FAIL** | No journey was run; walked in code, several fail:<br>• **Purchase through to delivery:** a worker books the courier and marks the order shipped about a second after payment, before the seller has packed it. The buyer can no longer cancel, and the seller's pack, cancel and own-courier steps stop working (COM-A-1, confirmed P0 in `verify/v1`).<br>• **Cancel after shipping, partial and full refund:** there is no refund after shipping and no partial refund anywhere; returns are fenced and no dispute path exists (COM-A-2).<br>• **The webhook arrives twice, late or mismatched:** a second capture, a capture after payments failed the intent, and an amount mismatch are recorded as owed back and never refunded in qa or prod (COM-C-1 + COM-E-2, confirmed P0).<br>• **Seller onboarding:** approval and product moderation have no working tool in the Helm environments (COM-A-3). |
| 2 | No open P0 findings | **FAIL** | Three confirmed: COM-A-1, COM-C-1 (with COM-E-2), and COM-B-1. A possible fourth, COM-A-13, is SUSPECTED: Razorpay is declared manual-capture and nothing calls capture. |
| 3 | Every route has an auth decision; every object-ID route has a tested authorization check | **FAIL** | • A self-serve seller can attach any media id as a variant image, and commerce then vouches for it to media-service (COM-B-1, raised to P0 in `verify/v2`).<br>• Seller approve, KYC verify, product approve and COD settle accept the shared internal key with the actor taken from an unverified header (COM-B-3).<br>• The authorization check exists on the invoice, shipments, order-items and seller-order-detail read routes, but none has a stranger test (COM-F-3).<br>• In a multi-seller order, any seller with one line can read the whole invoice and every seller's shipments (`verify/v2`). |
| 4 | No secrets in the repository, client bundle or logs | PASS | Grep-based check of commerce and payments is clean, and the CI Security run on HEAD scans the full history with gitleaks and passes (`verify/v3`, part 2g). The `.gitleaksignore` suppressions were not reviewed. |
| 5 | Dependency and static security scans clean | UNVERIFIED | govulncheck and trivy pass in CI. No static analyser is configured, and Android and web have no dependency scan (`01-baseline.md`). |
| 6 | Upload and media paths meet section 4.4 | **FAIL** | The variant-image write has no ownership check (COM-B-1). KYC documents are stored privately and are not exposed by that path, but KYC itself is a format-only stub and reviewers cannot open the documents (COM-B-4). |
| 7 | Money paths meet the commerce checks | **FAIL** | These hold (`verify/v3`, part 2): money is computed only on the server, in integer minor units; no oversell, and holds are released; refund caps and coupon limits.<br>These fail:<br>• An order can be marked paid by a forged `payment.succeeded` on the shared Kafka topic: one login for every service, no ACLs, and blank currency, payer and intent are accepted (COM-B-2).<br>• Owed captures are never refunded (COM-C-1).<br>• Nothing records what a seller is owed, and no payout is ever executed (COM-C-2).<br>• GST invoices are not compliant: one platform-wide series, the number taken outside the transaction, no place of supply, no credit note on cancel (COM-C-3).<br>• Courier bookings declare ₹0 (COM-A-4). |
| 8 | Retried and concurrent writes are safe for core actions | **FAIL** | • The order state machine in code passes, but its database backstop is not deployed: no environment gets the gated migrations, and `-gated` applies nothing while the PII cutover is `dual` (COM-E-1).<br>• Courier webhook processing has no shipment state guard: the full history is re-applied on every call, so a status can move back from delivered (COM-V3-2).<br>• Checkout holds the product, inventory and coupon rows with no statement or lock timeout (COM-D-3).<br>Idempotent checkout and the 50-way concurrent oversell test hold. |
| 9 | Build, tests and migrations pass in CI from clean | **FAIL** | Go CI, Integration, Security, Lint, E2E and web-ci are green. But `integration-commerce.yml`, the only place the gated-migration proofs run, last ran 19 commits ago (COM-F-4). Android CI fails at lint, and no test sends a signed provider webhook through to a paid order (COM-F-1). |
| 10 | Logs, metrics and alerts for core flows | **FAIL** | No commerce or payments alert rule exists beyond the chart's generic ones. Six commerce money metrics are registered and never incremented, and payments has no webhook or reconciliation metrics (COM-E-3). |
| 11 | Rollback path exists and backups restore | UNVERIFIED | Commerce migrations 051 and 052 matter for a one-version rollback (`01-baseline.md`). No rollback has been exercised and no restore tested. Tax records need about 72 months of retention. |
| 12 | Prod config has debug, test accounts and permissive settings off | **FAIL** | • Prod is seeded with the stub courier: a hard-coded ₹49 shipping rate, every pincode deliverable, and an unsigned webhook (COM-V3-1, COM-E-4).<br>• KYC is a format-only stub in every environment (COM-B-4).<br>• Razorpay test keys are refused by the secrets seeder but not by payments-service at runtime, and the prod values file says the opposite (`verify/v3`, part 2h). |

## To move to GO WITH CONDITIONS

1. Engineering fixes the three P0s first, then lines 1, 3, 6, 7, 8, 9, 10 and 12, in the order given in `05-fix-plan.md`.
2. Someone with access to the Razorpay dashboard confirms the capture mode, and one test-mode payment is run on QA (COM-A-13).
3. The founder decides the courier for launch: a stated flat fee, or Shiprocket switched on with its secrets (COM-V3-1).
4. The founder decides how sellers are paid, with commission and TDS rates, before any seller is onboarded for real money (COM-C-2).
5. A tax adviser confirms the invoice series and fields (COM-C-3).
6. Ops runs one restore and one rollback, and the founder decides backup retention (line 11).
