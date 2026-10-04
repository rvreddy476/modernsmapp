package http

import (
	"io"
	"net/http"
	"strconv"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Professional onboarding (/v1/doorstep/pro, A2). Every route acts on the
// caller's own professional; identity is the gateway's X-User-Id behind the
// internal key. Bodies are strictly decoded, so a body naming a field the
// route does not take (gender above all) is refused with 400.

func (h *Handler) registerProRoutes(user *gin.RouterGroup) {
	user.POST("/pro/apply", h.proApply)
	user.GET("/pro/me", h.proGetMe)
	user.PATCH("/pro/me", h.proPatchMe)
	user.GET("/pro/readiness", h.proReadiness)
	user.POST("/pro/digilocker/start", h.proDigiLockerStart)
	user.POST("/pro/digilocker/callback", h.proDigiLockerCallback)
	user.POST("/pro/selfie", h.proSelfie)
	user.GET("/pro/skills", h.proSkills)
	user.PUT("/pro/me/skills", h.proPutSkills)
	user.POST("/pro/me/skills/:code/certificate", h.proTradeCertificate)
	user.PUT("/pro/me/area", h.proPutArea)
	user.GET("/pro/me/hours", h.proGetHours)
	user.PUT("/pro/me/hours", h.proPutHours)
	user.GET("/pro/me/days-off", h.proDaysOff)
	user.POST("/pro/me/days-off", h.proAddDayOff)
	user.DELETE("/pro/me/days-off/:date", h.proDeleteDayOff)
	user.PUT("/pro/me/bank", h.proPutBank)
	user.POST("/pro/me/police-certificate", h.proPoliceCertificate)
	user.POST("/pro/me/agreement", h.proAgreement)
	user.PUT("/pro/me/pan", h.proPutPAN)
}

// proBody runs fn with the caller and a strictly decoded body.
func proBody[In any, Out any](c *gin.Context, status int, fn func(uid uuid.UUID, in In) (Out, error)) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var in In
	if !bindJSON(c, &in) {
		return
	}
	v, err := fn(uid, in)
	respond(c, status, v, err)
}

func (h *Handler) proApply(c *gin.Context) {
	proBody(c, http.StatusCreated, func(uid uuid.UUID, in model.ProApplyInput) (*model.Professional, error) {
		return h.svc.ProApply(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proGetMe(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	v, err := h.svc.ProMe(c.Request.Context(), uid)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proPatchMe(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.ProPatchInput) (*model.Professional, error) {
		return h.svc.ProPatchMe(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proReadiness(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	v, err := h.svc.ProReadiness(c.Request.Context(), uid)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proDigiLockerStart(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	v, err := h.svc.ProDigiLockerStart(c.Request.Context(), uid)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proDigiLockerCallback(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.DigiLockerCallbackInput) (*model.ProReadiness, error) {
		return h.svc.ProDigiLockerCallback(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proSelfie(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.MediaInput) (*model.KycCheck, error) {
		return h.svc.ProSelfie(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proSkills(c *gin.Context) {
	if _, ok := userID(c); !ok {
		return
	}
	v, err := list(h.svc.ProSkillCatalogue(c.Request.Context()))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proPutSkills(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.ProSkillsInput) (model.List[model.ProSkill], error) {
		return list(h.svc.ProPutSkills(c.Request.Context(), uid, in))
	})
}

func (h *Handler) proTradeCertificate(c *gin.Context) {
	proBody(c, http.StatusCreated, func(uid uuid.UUID, in model.CertificateInput) (*model.ProDocument, error) {
		return h.svc.ProTradeCertificate(c.Request.Context(), uid, c.Param("code"), in)
	})
}

func (h *Handler) proPutArea(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.ProAreaInput) (*model.ProArea, error) {
		return h.svc.ProPutArea(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proGetHours(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	v, err := h.svc.ProGetHours(c.Request.Context(), uid)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proPutHours(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.WeeklyHours) (*model.WeeklyHours, error) {
		return h.svc.ProPutHours(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proDaysOff(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	v, err := list(h.svc.ProDaysOff(c.Request.Context(), uid))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proAddDayOff(c *gin.Context) {
	proBody(c, http.StatusCreated, func(uid uuid.UUID, in model.DayOff) (*model.DayOff, error) {
		return h.svc.ProAddDayOff(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proDeleteDayOff(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	if err := h.svc.ProDeleteDayOff(c.Request.Context(), uid, c.Param("date")); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) proPutBank(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.BankInput) (*model.PayoutAccount, error) {
		return h.svc.ProPutBank(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proPoliceCertificate(c *gin.Context) {
	proBody(c, http.StatusCreated, func(uid uuid.UUID, in model.CertificateInput) (*model.ProDocument, error) {
		return h.svc.ProPoliceCertificate(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proAgreement(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.AgreementInput) (*model.ProReadiness, error) {
		return h.svc.ProAcceptAgreement(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proPutPAN(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.PANInput) (*model.ProReadiness, error) {
		return h.svc.ProPutPAN(c.Request.Context(), uid, in)
	})
}

// MaxWebhookBytes bounds a vendor webhook body.
const MaxWebhookBytes = 64 << 10

// POST /v1/doorstep/webhooks/background-check/:provider — a future vendor's
// signed callback (no identity). No vendor is enabled: every provider 404s.
func (h *Handler) backgroundCheckWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxWebhookBytes+1))
	if err != nil || len(body) > MaxWebhookBytes {
		writeErr(c, apperr.Invalid("", "webhook body too large"))
		return
	}
	if err := h.svc.BackgroundCheckWebhook(c.Request.Context(), c.Param("provider"), c.Request.Header, body); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- admin-internal professional review ----

func (h *Handler) proAdminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodGet, "/professionals", PermProsRead, h.adminListProfessionals},
		{http.MethodGet, "/professionals/:id", PermProsRead, h.adminGetProfessional},
		{http.MethodPost, "/professionals/:id/approve", PermProsApprove, h.adminProStatus("approve")},
		{http.MethodPost, "/professionals/:id/reject", PermProsApprove, h.adminProStatus("reject")},
		{http.MethodPost, "/professionals/:id/suspend", PermProsSuspend, h.adminProStatus("suspend")},
		{http.MethodPost, "/professionals/:id/reinstate", PermProsSuspend, h.adminProStatus("reinstate")},
		{http.MethodPost, "/professionals/:id/block", PermProsSuspend, h.adminProStatus("block")},
		{http.MethodPost, "/professionals/:id/skills/:code/verify", PermProsApprove, h.adminVerifySkill},
		{http.MethodGet, "/documents", PermDocumentsReview, h.adminListDocuments},
		{http.MethodPost, "/documents/:id/decide", PermDocumentsReview, h.adminDecideDocument},
		{http.MethodGet, "/documents/:id/view", PermDocumentsReview, h.adminViewDocument},
	}
}

func (h *Handler) adminListProfessionals(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	v, err := h.svc.AdminListProfessionals(c.Request.Context(), c.Query("status"), c.Query("city"), c.Query("cursor"), limit)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminGetProfessional(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.AdminProfessional(c.Request.Context(), id)
	respond(c, http.StatusOK, v, err)
}

// bindOptionalJSON decodes a body when one is sent (approve takes none).
func bindOptionalJSON(c *gin.Context, dst any) bool {
	if c.Request.ContentLength == 0 {
		return true
	}
	return bindJSON(c, dst)
}

func (h *Handler) adminProStatus(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := uuidParam(c, "id")
		if !ok {
			return
		}
		var in model.ReasonInput
		if !bindOptionalJSON(c, &in) {
			return
		}
		ctx, a := c.Request.Context(), adminActor(c)
		var v *model.Professional
		var err error
		switch action {
		case "approve":
			v, err = h.svc.AdminApproveProfessional(ctx, a, id, in)
		case "reject":
			v, err = h.svc.AdminRejectProfessional(ctx, a, id, in)
		case "suspend":
			v, err = h.svc.AdminSuspendProfessional(ctx, a, id, in)
		case "reinstate":
			v, err = h.svc.AdminReinstateProfessional(ctx, a, id, in)
		case "block":
			v, err = h.svc.AdminBlockProfessional(ctx, a, id, in)
		}
		respond(c, http.StatusOK, v, err)
	}
}

func (h *Handler) adminVerifySkill(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.SkillVerifyInput
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.AdminVerifySkill(c.Request.Context(), adminActor(c), id, c.Param("code"), in)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminListDocuments(c *gin.Context) {
	v, err := list(h.svc.AdminListDocuments(c.Request.Context(), c.Query("status")))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminDecideDocument(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, in model.DocumentDecisionInput) (*model.ProDocument, error) {
		return h.svc.AdminDecideDocument(c.Request.Context(), adminActor(c), id, in)
	})
}

// GET /internal/admin/documents/:id/view — the document's image BYTES for
// the console's canvas (police certificate, trade certificate, selfie),
// fetched from media-service server-side by the document's own media id,
// one audit row per view. Never a redirect or a URL; never cached.
func (h *Handler) adminViewDocument(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("X-Content-Type-Options", "nosniff")
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	img, err := h.svc.AdminViewDocument(c.Request.Context(), adminActor(c), id)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.Header("Content-Length", strconv.Itoa(len(img.Bytes)))
	c.Data(http.StatusOK, img.ContentType, img.Bytes)
}
