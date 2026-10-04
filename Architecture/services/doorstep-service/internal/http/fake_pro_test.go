package http

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// fakeProStore is an in-memory service.ProStore for handler tests and the
// professional contract fixtures. It mirrors the pgx store's rules that the
// fixtures depend on (one professional per user, gender written only by
// RecordAadhaar, one pending certificate, status transitions through
// ChangeProStatus with the check inside, a clear background check only from
// an approved police certificate). The SQL itself is pinned by
// internal/itest on doorstep_it_test.
type fakeProStore struct {
	mu        sync.Mutex
	pros      map[uuid.UUID]*fakePro // by pro id
	byUser    map[uuid.UUID]uuid.UUID
	docs      map[uuid.UUID]*model.ProDocument
	docOrder  []uuid.UUID
	states    map[string]*fakeState
	events    []string // event types, in order
	roleOps   []string // "grant <user>" / "revoke <user>"
	audits    []string // action entity_id
	auditActs []store.Actor
}

type fakePro struct {
	p          model.Professional
	aadhaar    bool
	aadhaarRef string
	photoRef   *uuid.UUID
	selfie     bool
	kyc        []model.KycCheck
	skills     map[string]*model.ProSkill
	zones      []uuid.UUID
	home       bool
	hours      []model.HoursWindow
	daysOff    map[string]*string
	payout     *model.PayoutAccount
	pan        bool
	agreement  string
	bg         []fakeBG
	incident   bool
}

type fakeBG struct {
	id         uuid.UUID
	docID      uuid.UUID
	source     string
	status     string
	validFrom  *time.Time
	validUntil *time.Time
}

type fakeState struct {
	proID    uuid.UUID
	sealed   []byte
	expires  time.Time
	consumed bool
}

func newFakeProStore() *fakeProStore {
	return &fakeProStore{pros: map[uuid.UUID]*fakePro{}, byUser: map[uuid.UUID]uuid.UUID{},
		docs: map[uuid.UUID]*model.ProDocument{}, states: map[string]*fakeState{}}
}

// fakeSkillRules: a slice of the seed (certificate rule, categories).
var fakeSkillRules = map[string]*store.SkillRule{
	"deep_cleaning": {Code: "deep_cleaning", Categories: []prokyc.SkillCategory{{Skill: "deep_cleaning", Family: "HOME_CLEANING", GenderRule: "any"}}},
	"electrician":   {Code: "electrician", RequiresCertificate: true, Categories: []prokyc.SkillCategory{{Skill: "electrician", Family: "INSTALLATION_REPAIR", GenderRule: "any"}}},
	"salon_women":   {Code: "salon_women", Categories: []prokyc.SkillCategory{{Skill: "salon_women", Family: "BEAUTY_SALON", GenderRule: "female_pros_only"}}},
	"salon_men":     {Code: "salon_men", Categories: []prokyc.SkillCategory{{Skill: "salon_men", Family: "BEAUTY_SALON", GenderRule: "male_pros_only"}}},
}

var fakeZones = map[uuid.UUID]bool{
	devseed.ID("zone", "west-hitec-gachibowli"):   true,
	devseed.ID("zone", "central-banjara-jubilee"): true,
}

func (f *fakeProStore) get(id uuid.UUID) (*fakePro, error) {
	p, ok := f.pros[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p, nil
}

func (f *fakeProStore) event(t string) { f.events = append(f.events, t) }

func (f *fakeProStore) role(status string, user uuid.UUID) {
	switch status {
	case "draft", "pending_verification", "approved":
		f.roleOps = append(f.roleOps, "grant "+user.String())
	case "rejected", "blocked":
		f.roleOps = append(f.roleOps, "revoke "+user.String())
	}
}

func (f *fakeProStore) CreateProfessional(_ context.Context, in store.NewProfessional) (*model.Professional, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.CityCode != "HYD" {
		return nil, store.ErrBadReference
	}
	if _, ok := f.byUser[in.UserID]; ok {
		return nil, store.ErrConflict
	}
	id := devseed.ID("professional", in.UserID.String())
	fp := &fakePro{p: model.Professional{ID: id, UserID: in.UserID, Status: "draft", DisplayName: in.DisplayName, CityCode: in.CityCode,
		MaxJobsPerDay: 6, CreatedAt: fixtureTS, UpdatedAt: fixtureTS}, skills: map[string]*model.ProSkill{}, daysOff: map[string]*string{}}
	for _, d := range in.Skills {
		fp.declare(d)
	}
	f.pros[id], f.byUser[in.UserID] = fp, id
	f.role("draft", in.UserID)
	f.event("doorstep.pro.applied")
	cp := fp.p
	return &cp, nil
}

func (fp *fakePro) declare(d store.SkillDecl) {
	if _, ok := fp.skills[d.Code]; ok {
		return
	}
	// Declaring never verifies (founder rule 4 Oct 2026): an admin does.
	fp.skills[d.Code] = &model.ProSkill{SkillCode: d.Code, Status: "pending"}
}

func (f *fakeProStore) ProfessionalByUser(_ context.Context, uid uuid.UUID) (*model.Professional, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byUser[uid]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := f.pros[id].p
	return &cp, nil
}

func (f *fakeProStore) ProfessionalByID(_ context.Context, id uuid.UUID) (*model.Professional, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	cp := p.p
	return &cp, nil
}

func (f *fakeProStore) UpdateProfile(_ context.Context, id uuid.UUID, name, photo *string) (*model.Professional, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		p.p.DisplayName = *name
	}
	if photo != nil {
		v := *photo
		p.p.PhotoMediaID = &v
	}
	cp := p.p
	return &cp, nil
}

func (f *fakeProStore) state(p *fakePro, today time.Time) *store.ProState {
	st := &store.ProState{ProID: p.p.ID, UserID: p.p.UserID, Status: p.p.Status, IncidentSuspended: p.incident}
	if p.p.Gender != nil {
		st.Gender = *p.p.Gender
	}
	st.DisplayName, st.HasPhoto = p.p.DisplayName, p.p.PhotoMediaID != nil
	st.AadhaarVerified, st.SelfieMatched = p.aadhaar && p.p.Gender != nil, p.selfie
	for code, s := range p.skills {
		if s.Status == "verified" {
			st.VerifiedSkills++
		} else if s.Status == "pending" {
			if r := fakeSkillRules[code]; r != nil && !r.RequiresCertificate {
				st.SkillsAwaitingReview++
				continue
			}
			for _, id := range f.docOrder {
				d := f.docs[id]
				if d.ProID == p.p.ID && d.Kind == "trade_certificate" && (d.Status == "pending" || d.Status == "approved") && d.SkillCode != nil && *d.SkillCode == code {
					st.SkillsAwaitingReview++
					break
				}
			}
		}
	}
	st.HasServiceArea, st.HasWeeklyHours = p.home && len(p.zones) > 0, len(p.hours) > 0
	st.HasPayoutAccount = p.payout != nil
	for _, b := range p.bg {
		if b.status == "clear" && !b.validFrom.After(today) && b.validUntil.After(today) {
			st.BackgroundClear = true
		}
	}
	for _, id := range f.docOrder {
		d := f.docs[id]
		if d.ProID == p.p.ID && d.Kind == "police_certificate" && d.Status == "pending" {
			st.PoliceCertificatePending = true
		}
		if d.ProID == p.p.ID && d.Kind == "selfie" && d.Status == "pending" {
			st.SelfieAwaitingReview = true
		}
	}
	st.AgreementVersion, st.HasPAN = p.agreement, p.pan
	return st
}

func (f *fakeProStore) ProFacts(_ context.Context, id uuid.UUID, today time.Time) (*store.ProState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	return f.state(p, today), nil
}

func (f *fakeProStore) MarkPendingVerification(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil || p.p.Status != "draft" {
		return false, err
	}
	p.p.Status = "pending_verification"
	f.role("pending_verification", p.p.UserID)
	f.event("doorstep.pro.status_changed")
	return true, nil
}

func (f *fakeProStore) CreateDigiLockerState(_ context.Context, id uuid.UUID, hash string, sealed []byte, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[hash] = &fakeState{proID: id, sealed: sealed, expires: exp}
	return nil
}

func (f *fakeProStore) ConsumeDigiLockerState(_ context.Context, uid uuid.UUID, hash string) (uuid.UUID, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[hash]
	if !ok || f.pros[st.proID].p.UserID != uid {
		return uuid.Nil, nil, store.ErrNotFound
	}
	if st.consumed {
		return uuid.Nil, nil, store.ErrStateUsed
	}
	st.consumed = true
	return st.proID, st.sealed, nil
}

func (f *fakeProStore) RecordAadhaar(_ context.Context, id uuid.UUID, rec store.AadhaarRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return err
	}
	for oid, o := range f.pros {
		if oid != id && o.aadhaarRef == rec.Reference {
			return store.ErrDuplicateIdentity
		}
	}
	if p.p.Gender != nil && *p.p.Gender != rec.Gender {
		return store.ErrGenderMismatch
	}
	g := rec.Gender
	p.p.Gender, p.aadhaar, p.aadhaarRef, p.photoRef = &g, true, rec.Reference, rec.PhotoMediaID
	at := fixtureTS
	p.kyc = append(p.kyc, model.KycCheck{Kind: "digilocker_aadhaar", Status: "passed", VerifiedAt: &at})
	return nil
}

func (f *fakeProStore) AadhaarReference(_ context.Context, id uuid.UUID) (bool, *uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return false, nil, err
	}
	return p.aadhaar, p.photoRef, nil
}

func (f *fakeProStore) RecordSelfie(_ context.Context, id, media uuid.UUID, status, _ string, score *float64, details map[string]any) (*model.KycCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	// Advisory only: the selfie waits for an admin whatever the score.
	c := model.KycCheck{Kind: "selfie_face_match", Status: status, Score: score}
	p.kyc = append(p.kyc, c)
	docStatus := map[string]string{"pending": "pending", "failed": "rejected"}[status]
	if docStatus == "" {
		return nil, store.ErrInvalid
	}
	for _, did := range f.docOrder {
		if d := f.docs[did]; d.ProID == id && d.Kind == "selfie" && d.Status == "pending" {
			r := "superseded by a newer selfie"
			d.Status, d.Reason = "rejected", &r
		}
	}
	f.addDoc(&model.ProDocument{ID: devseed.ID("selfie", fmt.Sprintf("%s/%d", media, len(f.docOrder))), ProID: id, Kind: "selfie",
		MediaID: media.String(), Status: docStatus, CreatedAt: fixtureTS})
	return &c, nil
}

func (f *fakeProStore) addDoc(d *model.ProDocument) {
	f.docs[d.ID] = d
	f.docOrder = append(f.docOrder, d.ID)
}

func (f *fakeProStore) ListSkills(context.Context) ([]model.Skill, error) {
	return []model.Skill{
		{Code: "deep_cleaning", Name: "Deep cleaning", Description: "", RequiresCertificate: false},
		{Code: "electrician", Name: "Electrician", Description: "", RequiresCertificate: true},
		{Code: "salon_men", Name: "Salon for men", Description: "", RequiresCertificate: false},
		{Code: "salon_women", Name: "Salon for women", Description: "", RequiresCertificate: false},
	}, nil
}

func (f *fakeProStore) SkillRules(_ context.Context, codes []string) (map[string]*store.SkillRule, error) {
	out := map[string]*store.SkillRule{}
	for _, c := range codes {
		if r, ok := fakeSkillRules[c]; ok {
			out[c] = r
		}
	}
	return out, nil
}

func (f *fakeProStore) CategorySkills(_ context.Context, ids []uuid.UUID) ([]string, int, error) {
	found, skills := 0, []string{}
	for _, id := range ids {
		switch id {
		case devseed.ID("category", "home-cleaning"):
			found++
			skills = append(skills, "deep_cleaning")
		case devseed.ID("category", "salon-women"):
			found++
			skills = append(skills, "salon_women")
		}
	}
	return skills, found, nil
}

func (f *fakeProStore) skillList(p *fakePro) []model.ProSkill {
	out := []model.ProSkill{}
	for _, s := range p.skills {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkillCode < out[j].SkillCode })
	return out
}

func (f *fakeProStore) ReplaceSkills(_ context.Context, id uuid.UUID, decls []store.SkillDecl) ([]model.ProSkill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, d := range decls {
		keep[d.Code] = true
		p.declare(d)
	}
	for c := range p.skills {
		if !keep[c] {
			delete(p.skills, c)
		}
	}
	return f.skillList(p), nil
}

func (f *fakeProStore) ProSkills(_ context.Context, id uuid.UUID) ([]model.ProSkill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	return f.skillList(p), nil
}

func (f *fakeProStore) UploadDocument(_ context.Context, d store.NewDocument, bg *store.BackgroundInit) (*model.ProDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(d.ProID)
	if err != nil {
		return nil, err
	}
	if d.Kind == "trade_certificate" {
		if _, ok := p.skills[*d.SkillCode]; !ok {
			return nil, store.ErrNotFound
		}
	}
	for _, id := range f.docOrder {
		o := f.docs[id]
		if o.ProID == d.ProID && o.Kind == d.Kind && o.Status == "pending" && (d.SkillCode == nil || (o.SkillCode != nil && *o.SkillCode == *d.SkillCode)) {
			return nil, store.ErrConflict
		}
	}
	issued := d.IssuedOn.Format("2006-01-02")
	doc := &model.ProDocument{ID: d.ID, ProID: d.ProID, Kind: d.Kind, SkillCode: d.SkillCode, MediaID: d.MediaID.String(),
		Status: "pending", IssuedOn: &issued, ExpiresOn: datePtr(d.ExpiresOn), CreatedAt: fixtureTS}
	if bg != nil && bg.Status == "clear" {
		doc.Status = "approved"
	}
	f.addDoc(doc)
	if bg != nil {
		p.bg = append(p.bg, fakeBG{id: devseed.ID("background_check", d.ID.String()), docID: d.ID, source: "uploaded_document",
			status: bg.Status, validFrom: bg.ValidFrom, validUntil: bg.ValidUntil})
	}
	cp := *doc
	return &cp, nil
}

func datePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func (f *fakeProStore) SetArea(_ context.Context, id uuid.UUID, zones []uuid.UUID, lat, lng float64, r int) (*model.ProArea, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		if !fakeZones[z] {
			return nil, store.ErrBadReference
		}
	}
	p.zones, p.home = zones, true
	return &model.ProArea{ZoneIDs: zones, HomeLat: &lat, HomeLng: &lng, RadiusM: r}, nil
}

func (f *fakeProStore) ProZoneIDs(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	out := append([]uuid.UUID{}, p.zones...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (f *fakeProStore) ReplaceHours(_ context.Context, id uuid.UUID, ws []prokyc.Window) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return err
	}
	p.hours = nil
	for _, w := range ws {
		p.hours = append(p.hours, model.HoursWindow{Weekday: w.Weekday, Start: w.Start, End: w.End})
	}
	return nil
}

func (f *fakeProStore) Hours(_ context.Context, id uuid.UUID) ([]model.HoursWindow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	return append([]model.HoursWindow{}, p.hours...), nil
}

func (f *fakeProStore) AddDayOff(_ context.Context, id uuid.UUID, day string, reason *string, _, _ time.Time) (*model.DayOff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	if _, ok := p.daysOff[day]; ok {
		return nil, store.ErrConflict
	}
	if day == fakeBusyDay {
		return nil, store.ErrOverlap
	}
	p.daysOff[day] = reason
	return &model.DayOff{Date: day, Reason: reason}, nil
}

// fakeBusyDay has an accepted job on the fake calendar.
const fakeBusyDay = "2026-10-09"

func (f *fakeProStore) DaysOff(_ context.Context, id uuid.UUID, from string) ([]model.DayOff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	out := []model.DayOff{}
	for d, r := range p.daysOff {
		if d >= from {
			out = append(out, model.DayOff{Date: d, Reason: r})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

func (f *fakeProStore) DeleteDayOff(_ context.Context, id uuid.UUID, day string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return err
	}
	if _, ok := p.daysOff[day]; !ok {
		return store.ErrNotFound
	}
	delete(p.daysOff, day)
	return nil
}

func (f *fakeProStore) SetPayoutAccount(_ context.Context, id uuid.UUID, holder string, sealed []byte, _ uint32, last4, ifsc string) (*model.PayoutAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	if len(sealed) == 0 {
		panic("payout account stored unsealed")
	}
	p.payout = &model.PayoutAccount{AccountHolder: holder, AccountLast4: last4, IFSC: ifsc, Status: "pending"}
	cp := *p.payout
	return &cp, nil
}

func (f *fakeProStore) PayoutAccount(_ context.Context, id uuid.UUID) (*model.PayoutAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil || p.payout == nil {
		return nil, err
	}
	cp := *p.payout
	return &cp, nil
}

func (f *fakeProStore) SetPAN(_ context.Context, id uuid.UUID, sealed []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return err
	}
	p.pan = len(sealed) > 0
	return nil
}

func (f *fakeProStore) AcceptAgreement(_ context.Context, id uuid.UUID, v string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return err
	}
	p.agreement = v
	return nil
}

func (f *fakeProStore) ApplyProviderCheck(context.Context, string, string, string, *time.Time, *time.Time) error {
	return store.ErrNotFound
}

func (f *fakeProStore) ListProfessionals(_ context.Context, status, city string, after *store.ProCursor, limit int) ([]model.Professional, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.Professional{}
	for _, p := range f.pros {
		if (status == "" || p.p.Status == status) && (city == "" || p.p.CityCode == city) {
			out = append(out, p.p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() > out[j].ID.String() })
	if after != nil {
		for i, p := range out {
			if p.ID == after.ID {
				out = out[i+1:]
				break
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeProStore) docsWhere(keep func(*model.ProDocument) bool) []model.ProDocument {
	out := []model.ProDocument{}
	for _, id := range f.docOrder {
		if d := f.docs[id]; keep(d) {
			out = append(out, *d)
		}
	}
	return out
}

func (f *fakeProStore) ProDocuments(_ context.Context, id uuid.UUID) ([]model.ProDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.docsWhere(func(d *model.ProDocument) bool { return d.ProID == id }), nil
}

func (f *fakeProStore) ListDocuments(_ context.Context, status string) ([]model.ProDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.docsWhere(func(d *model.ProDocument) bool { return d.Status == status })
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind == "police_certificate" && out[j].Kind != "police_certificate" })
	return out, nil
}

func (f *fakeProStore) BackgroundChecks(_ context.Context, id uuid.UUID) ([]model.BackgroundCheckView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	out := []model.BackgroundCheckView{}
	for _, b := range p.bg {
		out = append(out, model.BackgroundCheckView{ID: b.id, Source: b.source, Status: b.status, ValidUntil: datePtr(b.validUntil)})
	}
	return out, nil
}

func (f *fakeProStore) KycChecks(_ context.Context, id uuid.UUID) ([]model.KycCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	return append([]model.KycCheck{}, p.kyc...), nil
}

func (f *fakeProStore) audit(a store.Actor, action, entity string) {
	f.audits = append(f.audits, action+" "+entity)
	f.auditActs = append(f.auditActs, a)
}

func (f *fakeProStore) ChangeProStatus(_ context.Context, a store.Actor, id uuid.UUID, ch store.StatusChange) (*model.Professional, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, s := range ch.From {
		allowed = allowed || s == p.p.Status
	}
	if !allowed {
		return nil, &store.TransitionError{Status: p.p.Status}
	}
	if ch.Check != nil {
		var cats []prokyc.SkillCategory
		for code, s := range p.skills {
			if s.Status == "verified" {
				cats = append(cats, fakeSkillRules[code].Categories...)
			}
		}
		if err := ch.Check(f.state(p, ch.Today), cats); err != nil {
			return nil, err
		}
	}
	p.p.Status = ch.To
	f.role(ch.To, p.p.UserID)
	f.event("doorstep.pro.status_changed")
	f.audit(a, ch.Action, id.String())
	cp := p.p
	return &cp, nil
}

func (f *fakeProStore) VerifySkill(_ context.Context, a store.Actor, id uuid.UUID, code string, verified bool, _ *string, check store.SkillVerifyCheck) (*model.ProSkill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(id)
	if err != nil {
		return nil, err
	}
	s, ok := p.skills[code]
	if !ok {
		return nil, store.ErrNotFound
	}
	hasCert := len(f.docsWhere(func(d *model.ProDocument) bool {
		return d.ProID == id && d.Kind == "trade_certificate" && d.Status == "approved" && d.SkillCode != nil && *d.SkillCode == code
	})) > 0
	gender := ""
	if p.p.Gender != nil {
		gender = *p.p.Gender
	}
	if err := check(fakeSkillRules[code], hasCert, gender); err != nil {
		return nil, err
	}
	if verified {
		at := fixtureTS
		s.Status, s.VerifiedAt = "verified", &at
	} else {
		s.Status, s.VerifiedAt = "revoked", nil
	}
	f.audit(a, "professional.skill_verify", id.String()+":"+code)
	cp := *s
	return &cp, nil
}

func (f *fakeProStore) DecideDocument(_ context.Context, a store.Actor, id uuid.UUID, approve bool, reason *string, check store.DocumentCheck) (*model.ProDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if d.Status != "pending" {
		return nil, &store.TransitionError{Status: d.Status}
	}
	if check != nil {
		cp := *d
		if err := check(&cp); err != nil {
			return nil, err
		}
	}
	p := f.pros[d.ProID]
	d.Status, d.Reason = "rejected", reason
	if approve {
		d.Status = "approved"
	}
	switch d.Kind {
	case "police_certificate":
		for i := range p.bg {
			if p.bg[i].docID == id && p.bg[i].status == "pending" {
				if approve {
					from, _ := time.ParseInLocation("2006-01-02", *d.IssuedOn, time.UTC)
					until := from.AddDate(1, 0, 0)
					p.bg[i].status, p.bg[i].validFrom, p.bg[i].validUntil = "clear", &from, &until
				} else {
					p.bg[i].status = "failed"
				}
			}
		}
	case "trade_certificate":
		if approve {
			at := fixtureTS
			p.skills[*d.SkillCode].Status, p.skills[*d.SkillCode].VerifiedAt = "verified", &at
		}
	case "selfie":
		if approve {
			p.selfie = true
		}
	}
	f.event("doorstep.pro.document_reviewed")
	f.audit(a, "document.decide", id.String())
	cp := *d
	return &cp, nil
}

func (f *fakeProStore) DocumentByID(_ context.Context, id uuid.UUID) (*model.ProDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (f *fakeProStore) AuditDocumentView(_ context.Context, a store.Actor, d *model.ProDocument) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audit(a, "document.view", d.ID.String())
	return nil
}

// ---- media fakes ----

// fakeMedia owns media per user and serves image bytes.
type fakeMedia struct {
	owner       map[uuid.UUID]uuid.UUID
	unavailable bool
}

func (m *fakeMedia) PrepareVisitPhoto(ctx context.Context, media, owner uuid.UUID) error {
	return m.VerifyOwned(ctx, media, owner, mediaclient.KindImage)
}

func (m *fakeMedia) VerifyOwned(_ context.Context, media, owner uuid.UUID, _ ...mediaclient.Kind) error {
	if m.unavailable {
		return mediaclient.ErrUnavailable
	}
	if o, ok := m.owner[media]; !ok || o != owner {
		return mediaclient.ErrNotYours
	}
	return nil
}

var fakePNG = []byte("\x89PNG\r\n\x1a\nfake-document-image")

func (m *fakeMedia) FetchImage(_ context.Context, media uuid.UUID) ([]byte, string, error) {
	if m.unavailable {
		return nil, "", mediaclient.ErrUnavailable
	}
	if _, ok := m.owner[media]; !ok {
		return nil, "", mediaclient.ErrNotFound
	}
	return fakePNG, "image/png", nil
}
