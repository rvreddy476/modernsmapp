// Package pages holds the spec's single-source-of-truth configuration for the
// Follow-Only Public Pages system: the 13 page types, their per-type action
// buttons + required/optional verification documents, and the display-label
// derivation rules. Handlers and the create flow read from here — nothing about
// page types is hardcoded elsewhere.
package pages

import "strings"

// PageType — the 13 canonical types (spec §2). No additions without an explicit
// spec change.
const (
	PageTypeBusiness              = "business"
	PageTypeCreator               = "creator"
	PageTypeCelebrity             = "celebrity"
	PageTypeFoundationNGO         = "foundation_ngo"
	PageTypeAuthorityGovernment   = "authority_government"
	PageTypeInstitution           = "institution"
	PageTypeCommunityOrganization = "community_organization"
	PageTypeMediaNews             = "media_news"
	PageTypeBrand                 = "brand"
	PageTypeProfessional          = "professional"
	PageTypeMarketplaceSeller     = "marketplace_seller"
	PageTypeFoodPartner           = "food_partner"
	PageTypeServiceProvider       = "service_provider"
)

// Page lifecycle statuses (spec §3).
const (
	StatusDraft         = "draft"
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusRejected      = "rejected"
	StatusSuspended     = "suspended"
	StatusDisabled      = "disabled"
)

// PageTypeConfig drives the create-UI, the page-profile action buttons, and the
// verification requirements for a given page type (spec §8).
type PageTypeConfig struct {
	Label             string   `json:"label"`
	Description       string   `json:"description"`
	ActionButtons     []string `json:"action_buttons"`
	RequiredDocuments []string `json:"required_documents"`
	OptionalDocuments []string `json:"optional_documents"`
}

// PageTypes is the ordered source-of-truth list of valid page types.
var PageTypes = []string{
	PageTypeBusiness, PageTypeCreator, PageTypeCelebrity, PageTypeFoundationNGO,
	PageTypeAuthorityGovernment, PageTypeInstitution, PageTypeCommunityOrganization,
	PageTypeMediaNews, PageTypeBrand, PageTypeProfessional, PageTypeMarketplaceSeller,
	PageTypeFoodPartner, PageTypeServiceProvider,
}

// PageTypeConfigs — the spec §8 config block, verbatim.
var PageTypeConfigs = map[string]PageTypeConfig{
	PageTypeBusiness: {
		Label:             "Business Page",
		Description:       "For shops, companies, restaurants, agencies, and local businesses.",
		ActionButtons:     []string{"follow", "message", "call", "website"},
		RequiredDocuments: []string{"address_proof"},
		OptionalDocuments: []string{"business_registration"},
	},
	PageTypeCreator: {
		Label:             "Creator Page",
		Description:       "For video creators, educators, artists, coaches, writers, and influencers.",
		ActionButtons:     []string{"follow", "subscribe", "videos", "message"},
		RequiredDocuments: []string{"identity_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeCelebrity: {
		Label:             "Celebrity Page",
		Description:       "For actors, musicians, sports personalities, public figures, and entertainers.",
		ActionButtons:     []string{"follow", "official_updates", "events"},
		RequiredDocuments: []string{"identity_proof", "official_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeFoundationNGO: {
		Label:             "Foundation / NGO Page",
		Description:       "For charities, NGOs, social work groups, and public welfare organizations.",
		ActionButtons:     []string{"follow", "donate", "volunteer", "contact"},
		RequiredDocuments: []string{"ngo_registration", "address_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeAuthorityGovernment: {
		Label:             "Authority / Government Page",
		Description:       "For government departments, public authorities, police, municipal bodies, and official services.",
		ActionButtons:     []string{"follow", "alerts", "report_issue", "official_website"},
		RequiredDocuments: []string{"government_authorization", "official_email_domain_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeInstitution: {
		Label:             "Institution Page",
		Description:       "For schools, colleges, universities, coaching centers, and training institutions.",
		ActionButtons:     []string{"follow", "admissions", "courses", "events", "contact"},
		RequiredDocuments: []string{"institution_registration", "address_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeCommunityOrganization: {
		Label:             "Community / Organization Page",
		Description:       "For associations, clubs, societies, local organizations, and professional groups.",
		ActionButtons:     []string{"follow", "join_community", "events", "contact"},
		RequiredDocuments: []string{},
		OptionalDocuments: []string{"other"},
	},
	PageTypeMediaNews: {
		Label:             "Media / News Page",
		Description:       "For news outlets, journalists, magazines, publications, and media networks.",
		ActionButtons:     []string{"follow", "latest_news", "subscribe", "contact"},
		RequiredDocuments: []string{"media_publication_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeBrand: {
		Label:             "Brand Page",
		Description:       "For product brands, labels, franchises, and official brand identities.",
		ActionButtons:     []string{"follow", "shop", "offers", "support"},
		RequiredDocuments: []string{"brand_ownership_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeProfessional: {
		Label:             "Professional Page",
		Description:       "For doctors, lawyers, consultants, trainers, architects, designers, and licensed professionals.",
		ActionButtons:     []string{"follow", "book_appointment", "message", "call", "reviews"},
		RequiredDocuments: []string{"identity_proof", "professional_license"},
		OptionalDocuments: []string{},
	},
	PageTypeMarketplaceSeller: {
		Label:             "Marketplace Seller Page",
		Description:       "For sellers, merchants, handmade product creators, store owners, and distributors.",
		ActionButtons:     []string{"follow", "shop_products", "message_seller", "offers", "reviews"},
		RequiredDocuments: []string{"identity_proof", "seller_address_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeServiceProvider: {
		Label:             "Service Provider Page",
		Description:       "For electricians, plumbers, repair providers, cleaners, mechanics, movers, and local service experts.",
		ActionButtons:     []string{"follow", "book_service", "message", "call", "reviews"},
		RequiredDocuments: []string{"identity_proof", "service_proof"},
		OptionalDocuments: []string{},
	},
	PageTypeFoodPartner: {
		Label:             "Food Partner Page",
		Description:       "For restaurants, cloud kitchens, home kitchens, bakeries, tiffin centers, caterers, and food sellers.",
		ActionButtons:     []string{"follow", "order_food", "menu", "offers", "reviews"},
		RequiredDocuments: []string{"identity_proof"},
		OptionalDocuments: []string{"fssai_license"},
	},
}

// gatedButtons are the action buttons the backend actually enforces (spec §8).
// Everything else is a render-only hint.
var gatedButtons = map[string]bool{
	"follow":   true,
	"unfollow": true,
	"message":  true,
}

// validDocumentTypes — spec §2 DocumentType vocabulary.
var validDocumentTypes = map[string]bool{
	"identity_proof": true, "address_proof": true, "business_registration": true,
	"gst_certificate": true, "fssai_license": true, "ngo_registration": true,
	"government_authorization": true, "official_email_domain_proof": true,
	"institution_registration": true, "professional_license": true,
	"media_publication_proof": true, "brand_ownership_proof": true,
	"seller_address_proof": true, "service_proof": true, "official_proof": true,
	"other": true,
}

// IsValidPageType reports whether pt is one of the 13 canonical page types.
func IsValidPageType(pt string) bool {
	_, ok := PageTypeConfigs[pt]
	return ok
}

// IsValidDocumentType reports whether dt is in the §2 DocumentType vocabulary.
func IsValidDocumentType(dt string) bool {
	return validDocumentTypes[dt]
}

// IsGatedButton reports whether a button id is backend-enforced (vs a UI hint).
func IsGatedButton(id string) bool {
	return gatedButtons[id]
}

// RequiredDocs returns the required document types for a page type.
func RequiredDocs(pt string) []string {
	if cfg, ok := PageTypeConfigs[pt]; ok {
		return cfg.RequiredDocuments
	}
	return nil
}

// ActionButtons returns the configured action buttons for a page type.
func ActionButtons(pt string) []string {
	if cfg, ok := PageTypeConfigs[pt]; ok {
		return cfg.ActionButtons
	}
	return nil
}

// DisplayType derives the human label for a page type (spec §2): the config
// Label when known, otherwise a title-cased fallback from the raw type.
func DisplayType(pt string) string {
	if cfg, ok := PageTypeConfigs[pt]; ok {
		return cfg.Label
	}
	// Fallback: replace "_" with space and title-case each word.
	parts := strings.Split(pt, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
