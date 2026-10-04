package itest

import (
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/model"
	"testing"
)

func TestAdminTaxActiveBookingGuardAndAudit(t *testing.T) {
	br, b, _, _ := newVisitRig(t)
	pro := br.pros[0].id
	path := "/professionals/" + pro.String() + "/tax-registration"
	body := `{"gstin":"29ZZZPZ0000Z1Z6","verified":true,"reason":"Registration documents reviewed with ownership"}`
	code, raw := br.adminCall("POST", path, doorstephttp.PermProsApprove, body)
	want(t, "active visit refusal", code, raw, 409)
	br.exec(t, `UPDATE doorstep.bookings SET status='cancelled', cancelled_at=$2 WHERE id=$1`, b, br.now)
	code, raw = br.adminCall("POST", path, doorstephttp.PermProsApprove, body)
	want(t, "record", code, raw, 200)
	var registration model.TaxRegistration
	data(t, raw, &registration)
	if registration.GSTIN == nil || *registration.GSTIN != "29ZZZPZ0000Z1Z6" {
		t.Fatal("missing registration")
	}
	if br.one(t, `SELECT count(*) FROM doorstep.admin_audit_log WHERE action='professional.tax_registration' AND entity_id=$1`, pro.String()).(int64) != 1 {
		t.Fatal("missing tax review audit")
	}
	code, raw = br.adminCall("GET", path, doorstephttp.PermProsRead, "")
	want(t, "scope", code, raw, 403)
}
