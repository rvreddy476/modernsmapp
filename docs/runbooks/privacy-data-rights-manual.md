# Manual privacy data-rights runbook (launch)

Status: launch procedure until the audited export/erasure coordinator ships. Owner: Privacy Operations. Canonical contact: `privacy@cleestudio.com`.

This procedure must not be represented as an automated in-app export or deletion. Opening the email route creates no request. A case exists only after Privacy Operations records it in the restricted case system.

## 1. Intake and identity verification

1. Record an immutable case ID, receipt time, requested right, claimed account ID, contact address, jurisdiction, and assigned operator.
2. Reply from the controlled privacy mailbox. Never request passwords, OTPs, access tokens, government-ID secrets, or payment credentials by ordinary email.
3. Verify control of the registered account using an authenticated in-app challenge or a separately approved recovery procedure. A mailbox match alone is insufficient for a high-risk export or deletion.
4. Record the verification method, operator, timestamp, and result. If identity cannot be established, pause the case and disclose no account existence or data.

## 2. Scope and legal hold

1. Confirm whether the request is access/export, correction, deletion, or a combination.
2. Record any lawful retention, fraud, safety, financial, dispute, or legal-hold exception with its owner and expiry/review date. Do not silently omit held data; explain the applicable category in the final acknowledgement without exposing protected investigations.
3. Use the stable canonical user ID. Do not scope collection by display name, handle, phone suffix, or mutable email alone.

## 3. Export collection

1. Collect only data belonging to the verified canonical user from each launch data owner: identity/auth, profile, graph, post/PostTube/story references, media metadata and owned objects, feed/search projections, chat/message metadata and permitted content, reports/appeals, analytics, and recorded monetization ledger data.
2. Each service owner returns a signed or access-controlled receipt containing case ID, canonical user ID, data categories, query time/range, record count, omissions, and operator/tool version.
3. Reconcile receipts against the launch service inventory. A missing, timed-out, or ambiguous service response blocks completion; it must never be treated as an empty result.
4. Scan the package for secrets and third-party personal data. Apply documented redaction while preserving the requester’s own information and required context.

## 4. Secure delivery

1. Build the export in an encrypted, case-specific workspace with least-privilege access and audit logging.
2. Deliver through a short-lived authenticated download. Send any decryption factor through a separate verified channel. Ordinary email attachments and public object URLs are prohibited.
3. Record package checksum, size, encryption method, delivery expiry, successful access (if available), and operator approval.
4. Remove temporary plaintext and revoke package access after the approved retention window; record that cleanup in the case.

## 5. Deletion execution

Launch self-service deletion remains disabled and mutation-free. Until the deletion coordinator and rollback/reconciliation tests are approved, deletion is a controlled operator process:

1. Reconfirm identity and irreversible scope immediately before execution.
2. Freeze conflicting account mutations and create a case-bound deletion plan covering every launch data owner.
3. Preserve only explicitly approved legal-hold or statutory records. Record the basis and retention boundary.
4. Execute through each service’s reviewed owner-deletion path. Post/search removal must pass through the established transactional eligibility choke point and author-erasure fence; direct index deletion is not sufficient.
5. Require a durable per-service receipt. Any missing receipt leaves the case incomplete and enters retry/escalation, not success.
6. Reconcile public surfaces, authentication/session state, graph relationships, media access, search, feeds, chat access, analytics identifiers, and financial records before closure.

## 6. Completion and incident handling

1. A second authorized operator reviews identity proof, scope, service receipts, redactions/holds, delivery or deletion reconciliation, and cleanup evidence.
2. Send a completion acknowledgement containing the case ID, completed action, completion date, delivery expiry where relevant, and a contact/escalation route.
3. Never mark a case complete on a partial export, partial erasure, failed verification, unresolved service response, or unacknowledged delivery failure.
4. Suspected disclosure to the wrong person, leaked package URL/key, incomplete erasure reported as complete, or audit-log loss is a privacy incident: revoke access where possible, preserve evidence, notify Security/Privacy leadership, and begin the incident process immediately.

## 7. Audit minimum

Retain case metadata, consent/request evidence, identity-verification result (not reusable secrets), operator actions, service receipts, exceptions/holds, approvals, package checksum, delivery/revocation evidence, completion acknowledgement, and incident links according to the approved retention schedule. Access to the case record is least-privilege and audited.

