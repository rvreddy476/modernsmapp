package http

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// navigationOrder is the console's menu order (founder order: live pilots,
// money, content apps, Mopedu, then cross-app areas).
var navigationOrder = []struct{ app, label string }{
	{"dating", "Dating"},
	{"food", "Feast"},
	{"commerce", "MStore"},
	{"monetization", "Monetization"},
	{"payments", "Payments"},
	{"wallet", "Wallet"},
	{"social", "Social"},
	{"tube", "Tube"},
	{"qa", "Q&A"},
	{"chat", "Chat"},
	{"rider", "Mopedu"},
	{"trust_safety", "Trust & safety"},
	{"platform", "Platform"},
	// Access (roles, holders, sessions) is a page of the platform, shown to
	// anyone holding one of accessPermissions; its app name is not one
	// identity knows, so it never collides with a permission map key.
	{accessNavApp, "Access"},
}

// accessNavApp is the navigation entry for the Access page.
const accessNavApp = "access"

// accessPermissions are the platform permissions that open the Access page.
var accessPermissions = []string{permPlatformRolesRead, permPlatformRolesManage, permPlatformSessionsRevoke, permPlatformUsersSearch}

// MeResponse is GET /v1/admin/me.
type MeResponse struct {
	UserID              string           `json:"user_id"`
	Permissions         MePerms          `json:"permissions"`
	MFA                 MeMFA            `json:"mfa"`
	StepUpValidUntil    *time.Time       `json:"step_up_valid_until"`
	StepUpWindowSeconds int              `json:"step_up_window_seconds"`
	Navigation          []NavigationItem `json:"navigation"`
}

type MePerms struct {
	Platform []string            `json:"platform"`
	Apps     map[string][]string `json:"apps"`
}

type MeMFA struct {
	// Required is ADMIN_REQUIRE_MFA.
	Required bool `json:"required"`
	// Verified is whether this session passed TOTP (X-Admin-MFA).
	Verified bool       `json:"verified"`
	AuthTime *time.Time `json:"auth_time"`
}

type NavigationItem struct {
	App   string `json:"app"`
	Label string `json:"label"`
	// Applications is set on Payments for an admin who reaches it only through
	// confined permissions (<app>:payments_…): the payments applications they
	// may view. Absent means every application.
	Applications []string `json:"applications,omitempty"`
}

// RegisterMeRoute adds GET /v1/admin/me. It is the one admin route reachable
// without MFA, so the console can send an admin who has not enrolled to
// enrolment; it shows only the caller's own permissions.
func (h *Handler) RegisterMeRoute(r *gin.Engine) {
	h.gate.Handle(r, http.MethodGet, "/v1/admin/me",
		Requirement{Operation: "me.read", Access: AccessAnyAdmin, App: "platform", AllowWithoutMFA: true},
		h.me)
}

func (h *Handler) me(c *gin.Context) {
	perms := permsFrom(c)
	now := h.gate.now()
	resp := MeResponse{
		UserID:      actorFrom(c),
		Permissions: MePerms{Platform: nonNil(perms.Platform), Apps: map[string][]string{}},
		MFA: MeMFA{
			Required: h.gate.requireMFA,
			Verified: adminauth.MFAVerified(c.GetHeader(adminauth.HeaderAdminMFA)),
		},
		StepUpWindowSeconds: int(adminauth.StepUpWindow / time.Second),
		Navigation:          navigation(perms),
	}
	for app, list := range perms.Apps {
		if len(list) > 0 {
			resp.Permissions.Apps[app] = list
		}
	}
	if t, ok := adminauth.ParseUnix(c.GetHeader(adminauth.HeaderAuthTime)); ok {
		resp.MFA.AuthTime = &t
	}
	if until, ok := adminauth.StepUpValidUntil(c.GetHeader(adminauth.HeaderStepUpAt), now); ok {
		resp.StepUpValidUntil = &until
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// navigation lists the apps the caller holds any permission in, in menu
// order; apps identity knows and this list does not yet are appended by name.
func navigation(p adminauth.Permissions) []NavigationItem {
	visible := map[string]bool{}
	for app, list := range p.Apps {
		if len(list) > 0 {
			visible[app] = true
		}
	}
	if len(p.Platform) > 0 {
		visible["platform"] = true
	}
	for _, perm := range accessPermissions {
		if p.Has(perm) {
			visible[accessNavApp] = true
			break
		}
	}
	// Payments confined to applications: a payments view held in a product app.
	var confined []string
	if !visible[paymentsAuditApp] {
		for _, m := range paymentsApplications {
			for _, perm := range p.Apps[m.app] {
				if strings.HasPrefix(perm, m.app+":payments_") {
					confined = append(confined, m.application)
					break
				}
			}
		}
		if len(confined) > 0 {
			visible[paymentsAuditApp] = true
		}
	}
	out := []NavigationItem{}
	for _, n := range navigationOrder {
		if visible[n.app] {
			item := NavigationItem{App: n.app, Label: n.label}
			if n.app == paymentsAuditApp {
				item.Applications = confined
			}
			out = append(out, item)
			delete(visible, n.app)
		}
	}
	rest := make([]string, 0, len(visible))
	for app := range visible {
		rest = append(rest, app)
	}
	sort.Strings(rest)
	for _, app := range rest {
		out = append(out, NavigationItem{App: app, Label: app})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
