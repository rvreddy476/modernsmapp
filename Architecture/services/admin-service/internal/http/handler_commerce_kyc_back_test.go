package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/admin-service/internal/service"
)

// stubWithBack lists the document with the given front/back flags and
// answers the image route with front or back bytes by ?side.
func (k kycCase) stubWithBack(rg *productsRig, viewable, backViewable bool, front, back []byte) {
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k.seller+"/documents", func(w http.ResponseWriter, _ *http.Request) {
		row := kycRow(k.doc, "voter_id", viewable)
		row["back_viewable"] = backViewable
		b, _ := json.Marshal(map[string]any{"data": []map[string]any{row}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k.seller+"/documents/"+k.doc+"/image", func(w http.ResponseWriter, r *http.Request) {
		body := front
		if r.URL.Query().Get("side") == "back" {
			body = back
		}
		serveImage("image/jpeg", body, true)(w, r)
	})
}

func TestKYCDocumentList_PassesBackViewable(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stubWithBack(rg, true, true, kycImage(10), kycImage(20))
	w := rg.do(http.MethodGet, commercePrefix+"/sellers/"+k.seller+"/documents", "", kycAdmin(rg), false)
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil || len(env.Data) != 1 || env.Data[0]["back_viewable"] != true {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
}

func TestKYCDocumentView_BackSideStreamsTheBack(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	front, back := kycImage(4000), bytes.Repeat([]byte{7}, 3000)
	k.stubWithBack(rg, true, true, front, back)
	actor := kycAdmin(rg)
	w := rg.do(http.MethodGet, k.path+"?side=back", "", actor, true)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), back) {
		t.Fatalf("back view: %d, %d bytes", w.Code, w.Body.Len())
	}
	hits := rg.takeHits()
	if len(hits) != 2 || hits[1].query != "side=back" {
		t.Fatalf("commerce hits = %+v", hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Payload["side"] != "back" {
		t.Fatalf("audit = %+v", a)
	}
	// Without ?side the front opens, and the upstream call carries no side.
	w = rg.do(http.MethodGet, k.path, "", actor, true)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), front) {
		t.Fatalf("front view: %d", w.Code)
	}
	if hits := rg.takeHits(); len(hits) != 2 || hits[1].query != "" {
		t.Fatalf("front hits = %+v", hits)
	}
}

// A document with no back never answers ?side=back with the front image:
// 404, and commerce's image route is not called.
func TestKYCDocumentView_BackRefusedWhenNotListed(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stubWithBack(rg, true, false, kycImage(4000), nil)
	w := rg.do(http.MethodGet, k.path+"?side=back", "", kycAdmin(rg), true)
	if w.Code != http.StatusNotFound || !hasCode(w, CodeDocumentNotFound) {
		t.Fatalf("back without back: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 1 {
		t.Fatalf("image route must not be called: %+v", hits)
	}
}
