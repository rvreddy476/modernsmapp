package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Doorstep permissions, exactly as doorstep-service checks them
// (doorstep-service/internal/http/admin_token.go) and as the contract names
// them (contracts/doorstep/openapi.yaml, x-doorstep-permissions).
const (
	permDoorstepCatalogueRead      = "doorstep:catalogue.read"
	permDoorstepCatalogueWrite     = "doorstep:catalogue.write"
	permDoorstepConfigWrite        = "doorstep:config.write"
	permDoorstepProsRead           = "doorstep:pros.read"
	permDoorstepProsApprove        = "doorstep:pros.approve"
	permDoorstepProsSuspend        = "doorstep:pros.suspend"
	permDoorstepDocumentsReview    = "doorstep:documents.review"
	permDoorstepBookingsRead       = "doorstep:bookings.read"
	permDoorstepBookingsCancel     = "doorstep:bookings.cancel"
	permDoorstepBookingsRedispatch = "doorstep:bookings.redispatch"
	permDoorstepRefundsIssue       = "doorstep:refunds.issue"
	permDoorstepIncidentsRead      = "doorstep:incidents.read"
	permDoorstepIncidentsAct       = "doorstep:incidents.act"
	permDoorstepTicketsAct         = "doorstep:tickets.act"
	permDoorstepRatingsModerate    = "doorstep:ratings.moderate"
	permDoorstepSettlementsRead    = "doorstep:settlements.read"
	permDoorstepStatsRead          = "doorstep:stats.read"
	permDoorstepAuditRead          = "doorstep:audit.read"
	permDoorstepPricesReview       = "doorstep:prices.review"
)

const (
	doorstepAuditApp = "doorstep"
	doorstepPrefix   = "/v1/admin/doorstep"
)

// Doorstep operations with a route-specific handler or decision.
const (
	// A booking refund gives money back to a customer: step-up and
	// two-person, always (the console's refund rule: every refund needs a
	// second approver, whatever the amount; there is no threshold).
	opDoorstepRefundIssue = "doorstep.booking.refund"
	// Resolving an incident with lift_suspension=true puts a professional
	// back on the platform: step-up, decided from the body.
	opDoorstepIncidentResolve = "doorstep.incident.resolve"
	// Viewing a document's image bytes: step-up, one audit row per view
	// written before the first byte (relayDocumentImage).
	opDoorstepDocumentView = "doorstep.document.view"
	// Verifying a skill: the audit target is the professional, not the code.
	opDoorstepSkillVerify = "doorstep.professional.skill.verify"
)

// DoorstepRoutes is the Doorstep route table under /v1/admin/doorstep: one
// console route per doorstep-service admin route in the contract, at the same
// path, forwarded with a token scoped to its x-permission. Routes whose
// upstream is not built yet answer with doorstep-service's own 404/501; the
// table is complete so the console is built once.
//
//	step-up      the professional detail (it carries the document media ids
//	             and the payout account: a KYC reveal), the document queue
//	             and document decisions, every document view (the image
//	             bytes, one audit row each), professional suspend / reinstate /
//	             block, booking cancel (refunds the customer), every money
//	             setting (prices, rate cards, cancellation and commission
//	             rules, approving a professional's own price), an incident
//	             resolve that lifts a suspension, and the refund
//	two-person   the refund, always
//	idempotent   the refund needs an Idempotency-Key, stored with the
//	             approval and replayed by the approver; every other write
//	             forwards the console's key when it sends one
var DoorstepRoutes = []productRoute{
	{method: http.MethodGet, path: "/professionals/:id/tax-registration", operation: "doorstep.professional.tax_registration.read", permission: permDoorstepProsApprove, stepUp: true, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/tax-registration", operation: "doorstep.professional.tax_registration.write", permission: permDoorstepProsApprove, stepUp: true, targetType: "doorstep_professional"},
	{method: http.MethodGet, path: "/stats", operation: "doorstep.stats", permission: permDoorstepStatsRead},

	// Catalogue and config (lane A1).
	{method: http.MethodGet, path: "/cities", operation: "doorstep.cities.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/cities", operation: "doorstep.city.create", permission: permDoorstepConfigWrite},
	{method: http.MethodPatch, path: "/cities/:code", operation: "doorstep.city.update", permission: permDoorstepConfigWrite, targetType: "doorstep_city"},
	{method: http.MethodGet, path: "/zones", operation: "doorstep.zones.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/zones", operation: "doorstep.zone.create", permission: permDoorstepConfigWrite},
	{method: http.MethodPatch, path: "/zones/:id", operation: "doorstep.zone.update", permission: permDoorstepConfigWrite, targetType: "doorstep_zone"},
	{method: http.MethodGet, path: "/categories", operation: "doorstep.categories.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/categories", operation: "doorstep.category.create", permission: permDoorstepCatalogueWrite},
	{method: http.MethodPatch, path: "/categories/:id", operation: "doorstep.category.update", permission: permDoorstepCatalogueWrite, targetType: "doorstep_category"},
	{method: http.MethodGet, path: "/skills", operation: "doorstep.skills.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/skills", operation: "doorstep.skill.create", permission: permDoorstepCatalogueWrite},
	{method: http.MethodGet, path: "/services", operation: "doorstep.services.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/services", operation: "doorstep.service.create", permission: permDoorstepCatalogueWrite},
	{method: http.MethodGet, path: "/services/:id", operation: "doorstep.service.read", permission: permDoorstepCatalogueRead, targetType: "doorstep_service"},
	{method: http.MethodPatch, path: "/services/:id", operation: "doorstep.service.update", permission: permDoorstepCatalogueWrite, targetType: "doorstep_service"},
	{method: http.MethodPost, path: "/services/:id/options", operation: "doorstep.option.create", permission: permDoorstepCatalogueWrite, targetType: "doorstep_service"},
	{method: http.MethodPatch, path: "/options/:id", operation: "doorstep.option.update", permission: permDoorstepCatalogueWrite, targetType: "doorstep_option"},
	{method: http.MethodPost, path: "/services/:id/addon-groups", operation: "doorstep.addon_group.create", permission: permDoorstepCatalogueWrite, targetType: "doorstep_service"},
	{method: http.MethodPatch, path: "/addon-groups/:id", operation: "doorstep.addon_group.update", permission: permDoorstepCatalogueWrite, targetType: "doorstep_addon_group"},
	{method: http.MethodPost, path: "/addon-groups/:id/addons", operation: "doorstep.addon.create", permission: permDoorstepCatalogueWrite, targetType: "doorstep_addon_group"},
	{method: http.MethodPatch, path: "/addons/:id", operation: "doorstep.addon.update", permission: permDoorstepCatalogueWrite, targetType: "doorstep_addon"},
	// Money settings change what customers pay or professionals earn: every
	// write is step-up (as Mopedu's fare rules).
	{method: http.MethodGet, path: "/prices", operation: "doorstep.prices.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/prices", operation: "doorstep.price.create", permission: permDoorstepCatalogueWrite, stepUp: true},
	{method: http.MethodGet, path: "/rate-cards", operation: "doorstep.rate_cards.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/rate-cards", operation: "doorstep.rate_card.create", permission: permDoorstepCatalogueWrite, stepUp: true},
	{method: http.MethodPatch, path: "/rate-cards/:id", operation: "doorstep.rate_card.update", permission: permDoorstepCatalogueWrite, stepUp: true, targetType: "doorstep_rate_card"},
	{method: http.MethodGet, path: "/slot-configs", operation: "doorstep.slot_configs.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/slot-configs", operation: "doorstep.slot_config.create", permission: permDoorstepConfigWrite},
	{method: http.MethodPatch, path: "/slot-configs/:id", operation: "doorstep.slot_config.update", permission: permDoorstepConfigWrite, targetType: "doorstep_slot_config"},
	{method: http.MethodGet, path: "/cancellation-rules", operation: "doorstep.cancellation_rules.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/cancellation-rules", operation: "doorstep.cancellation_rule.create", permission: permDoorstepConfigWrite, stepUp: true},
	{method: http.MethodPatch, path: "/cancellation-rules/:id", operation: "doorstep.cancellation_rule.update", permission: permDoorstepConfigWrite, stepUp: true, targetType: "doorstep_cancellation_rule"},
	{method: http.MethodGet, path: "/commission-rules", operation: "doorstep.commission_rules.list", permission: permDoorstepCatalogueRead},
	{method: http.MethodPost, path: "/commission-rules", operation: "doorstep.commission_rule.create", permission: permDoorstepConfigWrite, stepUp: true},
	{method: http.MethodPatch, path: "/commission-rules/:id", operation: "doorstep.commission_rule.update", permission: permDoorstepConfigWrite, stepUp: true, targetType: "doorstep_commission_rule"},

	// Professionals (lane A2): approve/reject is one severity, suspend /
	// reinstate / block another. The detail carries the document media ids
	// (Aadhaar, police certificate) and the payout account: a reveal.
	{method: http.MethodGet, path: "/professionals", operation: "doorstep.professionals.list", permission: permDoorstepProsRead},
	{method: http.MethodGet, path: "/professionals/:id", operation: "doorstep.professional.read", permission: permDoorstepProsRead, stepUp: true, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/approve", operation: "doorstep.professional.approve", permission: permDoorstepProsApprove, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/reject", operation: "doorstep.professional.reject", permission: permDoorstepProsApprove, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/suspend", operation: "doorstep.professional.suspend", permission: permDoorstepProsSuspend, stepUp: true, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/reinstate", operation: "doorstep.professional.reinstate", permission: permDoorstepProsSuspend, stepUp: true, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/block", operation: "doorstep.professional.block", permission: permDoorstepProsSuspend, stepUp: true, targetType: "doorstep_professional"},
	{method: http.MethodPost, path: "/professionals/:id/skills/:code/verify", operation: opDoorstepSkillVerify, permission: permDoorstepProsApprove, targetType: "doorstep_professional"},
	// Documents: the queue and each decision open the document itself.
	{method: http.MethodGet, path: "/documents", operation: "doorstep.documents.list", permission: permDoorstepDocumentsReview, stepUp: true},
	{method: http.MethodGet, path: "/documents/:id/view", operation: opDoorstepDocumentView, permission: permDoorstepDocumentsReview, stepUp: true, targetType: "doorstep_document"},
	{method: http.MethodPost, path: "/documents/:id/decide", operation: "doorstep.document.decide", permission: permDoorstepDocumentsReview, stepUp: true, targetType: "doorstep_document"},

	// Bookings and money (lane A6). Cancel refunds the customer (in full
	// unless a fee is stated): step-up. Redispatch re-runs matching without
	// the current professional; it never hand-picks one.
	{method: http.MethodGet, path: "/bookings", operation: "doorstep.bookings.list", permission: permDoorstepBookingsRead},
	{method: http.MethodGet, path: "/bookings/:id", operation: "doorstep.booking.read", permission: permDoorstepBookingsRead, targetType: "doorstep_booking"},
	{method: http.MethodPost, path: "/bookings/:id/cancel", operation: "doorstep.booking.cancel", permission: permDoorstepBookingsCancel, stepUp: true, targetType: "doorstep_booking"},
	{method: http.MethodPost, path: "/bookings/:id/redispatch", operation: "doorstep.booking.redispatch", permission: permDoorstepBookingsRedispatch, targetType: "doorstep_booking"},
	{method: http.MethodPost, path: "/bookings/:id/refund", operation: opDoorstepRefundIssue, permission: permDoorstepRefundsIssue, stepUp: true, twoPerson: true, targetType: "doorstep_booking", idempotent: true},

	// Safety, support, ratings.
	{method: http.MethodGet, path: "/incidents", operation: "doorstep.incidents.list", permission: permDoorstepIncidentsRead},
	{method: http.MethodPost, path: "/incidents/:id/acknowledge", operation: "doorstep.incident.acknowledge", permission: permDoorstepIncidentsAct, targetType: "doorstep_incident"},
	{method: http.MethodPost, path: "/incidents/:id/resolve", operation: opDoorstepIncidentResolve, permission: permDoorstepIncidentsAct, targetType: "doorstep_incident"},
	{method: http.MethodGet, path: "/tickets", operation: "doorstep.tickets.list", permission: permDoorstepTicketsAct},
	{method: http.MethodPost, path: "/tickets/:id/status", operation: "doorstep.ticket.status", permission: permDoorstepTicketsAct, targetType: "doorstep_ticket"},
	{method: http.MethodGet, path: "/ratings", operation: "doorstep.ratings.list", permission: permDoorstepRatingsModerate},
	{method: http.MethodPost, path: "/ratings/:id/hide", operation: "doorstep.rating.hide", permission: permDoorstepRatingsModerate, targetType: "doorstep_rating"},

	{method: http.MethodGet, path: "/settlements", operation: "doorstep.settlements.list", permission: permDoorstepSettlementsRead},
	{method: http.MethodGet, path: "/audit-logs", operation: "doorstep.audit.list", permission: permDoorstepAuditRead},

	// Professionals' own prices (lane B1): the review queue, approve, reject.
	// Nothing approves itself; doorstep-service audits each decision in the
	// same transaction. Approving makes a price customers pay live now, so it
	// is a money setting: step-up, as the city price. Rejecting changes no
	// price and the queue shows no personal data beyond the display name.
	{method: http.MethodGet, path: "/pro-prices", operation: "doorstep.pro_prices.list", permission: permDoorstepPricesReview},
	{method: http.MethodPost, path: "/pro-prices/:id/approve", operation: "doorstep.pro_price.approve", permission: permDoorstepPricesReview, stepUp: true, targetType: "doorstep_pro_price"},
	{method: http.MethodPost, path: "/pro-prices/:id/reject", operation: "doorstep.pro_price.reject", permission: permDoorstepPricesReview, targetType: "doorstep_pro_price"},
}

// RegisterDoorstepRoutes adds the Doorstep dashboard under /v1/admin/doorstep.
// The refund stores the console's call (with its Idempotency-Key) and replays
// it as the approver (doorstepStoredCall / doorstepExecutor).
func (h *Handler) RegisterDoorstepRoutes(r *gin.Engine) {
	p := product{app: doorstepAuditApp, label: "Doorstep", prefix: doorstepPrefix, client: h.doorstep}
	routes := make([]productRoute, len(DoorstepRoutes))
	copy(routes, DoorstepRoutes)
	special := map[string]gin.HandlerFunc{}
	for i := range routes {
		switch routes[i].operation {
		case opDoorstepRefundIssue:
			routes[i].decide = doorstepRefundDecision
		case opDoorstepIncidentResolve:
			routes[i].decide = doorstepIncidentResolveDecision
		case opDoorstepDocumentView:
			special[opDoorstepDocumentView] = h.viewDoorstepDocument(p)
		case opDoorstepSkillVerify:
			special[opDoorstepSkillVerify] = doorstepSkillVerify(h.forwardProduct(p, routes[i]))
		}
		rt := routes[i]
		if rt.twoPerson {
			h.approvals.Register(doorstepAuditApp, rt.operation, h.doorstepExecutor(rt))
			special[rt.operation] = h.doorstepStoredCall(rt)
		}
	}
	h.registerProduct(r, p, routes, special)
}

// doorstepSkillVerify forwards as-is and points the audit row at the
// professional, with the skill code alongside (the route's last :param is
// the code).
func doorstepSkillVerify(forward gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		forward(c)
		info := auditFrom(c)
		info.targetType, info.targetID = "doorstep_professional", c.Param("id")
		info.set("skill_code", c.Param("code"))
	}
}

// doorstepIncidentResolveDecision: resolving with lift_suspension=true lifts
// an incident auto-suspension (a professional back on the platform), so it
// needs a step-up like a reinstate; a plain resolve does not.
func doorstepIncidentResolveDecision(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
	if !perms.Has(permDoorstepIncidentsAct) {
		return Decision{}, nil // the gate refuses; nothing is parsed
	}
	raw, _, err := jsonBody(c)
	if err != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b struct {
		LiftSuspension *bool `json:"lift_suspension"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &b) != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The resolve body is malformed")
	}
	lift := b.LiftSuspension != nil && *b.LiftSuspension
	return Decision{StepUp: lift, Audit: map[string]any{"lift_suspension": lift}}, nil
}

// --- the refund (two-person, always) ---

// doorstepRefundBody is the contract's refund input: amount_paise and reason
// required, payment booking (default) or extras.
type doorstepRefundBody struct {
	AmountPaise *json.Number `json:"amount_paise"`
	Reason      string       `json:"reason"`
	Payment     *string      `json:"payment"`
}

// doorstepRefund validates the refund body: a positive whole number of paise
// (never a full refund by omission; the contract requires the amount) and
// payment booking or extras.
func doorstepRefund(raw []byte) (paise int64, payment string, err error) {
	var b doorstepRefundBody
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil {
		return 0, "", badRequest(CodeInvalidBody, "The refund body is malformed")
	}
	if b.AmountPaise == nil {
		return 0, "", badRequest(CodeInvalidBody, "amount_paise is required")
	}
	n, perr := strconv.ParseInt(b.AmountPaise.String(), 10, 64)
	if perr != nil || n <= 0 || n > maxRefundPaise {
		return 0, "", badRequest(CodeInvalidBody, "amount_paise must be a positive whole number of paise")
	}
	payment = "booking"
	if b.Payment != nil {
		payment = strings.TrimSpace(*b.Payment)
	}
	if payment != "booking" && payment != "extras" {
		return 0, "", badRequest(CodeInvalidBody, "payment must be booking or extras")
	}
	return n, payment, nil
}

// doorstepRefundDecision validates the refund before anything is stored and
// records the amount for the audit row. The route is two-person in the table
// whatever the amount.
func doorstepRefundDecision(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
	if !perms.Has(permDoorstepRefundsIssue) {
		return Decision{}, nil // the gate refuses; nothing is parsed
	}
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		return Decision{}, badRequest(CodeInvalidID, "Invalid booking id")
	}
	raw, _, err := jsonBody(c)
	if err != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	paise, payment, err := doorstepRefund(raw)
	if err != nil {
		return Decision{}, err
	}
	return Decision{TwoPerson: true, Audit: map[string]any{"amount_paise": paise, "payment": payment}}, nil
}

// doorstepStoredCallPayload is a two-person write exactly as the first admin
// sent it, with the Idempotency-Key the approver's replay carries.
type doorstepStoredCallPayload struct {
	Path           string          `json:"path"`
	Body           json.RawMessage `json:"body,omitempty"`
	AmountPaise    int64           `json:"amount_paise,omitempty"` // approval summary
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

// doorstepStoredCall submits a two-person write for approval. Every :param
// must be a uuid (doorstep ids are); the reason is the body's.
func (h *Handler) doorstepStoredCall(rt productRoute) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		for _, s := range strings.Split(rt.path, "/") {
			if strings.HasPrefix(s, ":") {
				if _, err := uuid.Parse(c.Param(s[1:])); err != nil {
					api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid id", nil)
					return
				}
			}
		}
		path, last, err := productPath(c, rt.path)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		key, ok := idempotencyKey(c, rt.idempotent)
		if !ok {
			return
		}
		raw, fields, err := jsonBody(c)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
			return
		}
		call := doorstepStoredCallPayload{Path: path, Body: raw, IdempotencyKey: key}
		if rt.operation == opDoorstepRefundIssue {
			call.AmountPaise, _, _ = doorstepRefund(raw) // validated by the decision
		}
		h.submitTwoPerson(c, rt.targetType, last, stringField(fields, "reason"), call)
	}
}

// doorstepExecutor replays a stored call as the approver, with a token scoped
// to the route's permission and the stored Idempotency-Key; a path that is
// not the route's own is refused.
func (h *Handler) doorstepExecutor(rt productRoute) approvals.Executor {
	return func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var sc doorstepStoredCallPayload
		if err := json.Unmarshal(payload, &sc); err != nil {
			return approvals.Result{Err: err}
		}
		if !storedPathMatches(rt.path, sc.Path) {
			return approvals.Result{Err: errStoredPath}
		}
		pr := service.ProductRequest{Method: rt.method, Path: sc.Path, Permission: rt.permission, Actor: actor, IdempotencyKey: sc.IdempotencyKey}
		if len(sc.Body) > 0 && string(sc.Body) != "null" {
			pr.RawBody = sc.Body
		}
		resp, err := h.doorstep.Do(ctx, pr)
		return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
	}
}

// --- the document view (image bytes, one audit row per view) ---

// doorstepDocumentImageTypes are the raster types the contract allows for a
// document view (JPEG, PNG, WebP, GIF, AVIF); an SVG or HTML answer is never
// relayed.
var doorstepDocumentImageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true, "image/avif": true,
}

// viewDoorstepDocument answers GET /v1/admin/doorstep/documents/:id/view with
// the document's image bytes, exactly as the commerce KYC view: the gate
// (doorstep:documents.review + step-up) → doorstep-service's
// /documents/:id/view, opened and judged (status, raster type, 15 MB cap,
// never a redirect) → the ONE audit row (doorstep.document.view, target the
// document) → only then the first byte, with Cache-Control no-store and the
// pinned view headers. doorstep-service resolves the media id itself and
// writes its own row; nothing here names a media id or a URL.
func (h *Handler) viewDoorstepDocument(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		info := auditFrom(c)
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		document := id.String()
		info.targetType, info.targetID = "doorstep_document", document
		perm, ok := kycPermission(c)
		if !ok {
			return
		}
		notFound := func() {
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeDocumentNotFound, "Document not found", nil)
		}
		h.relayDocumentImage(c, p, info, service.ProductRequest{
			Method: http.MethodGet, Path: "/documents/" + document + "/view",
			Permission: perm, Actor: actorFrom(c),
		}, doorstepDocumentImageTypes, notFound, "doorstep document stream ended early", "document_id", document)
	}
}
