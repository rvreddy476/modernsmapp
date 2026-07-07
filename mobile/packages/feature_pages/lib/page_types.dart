/// Client mirror of user-service `internal/pages/config.go` — the 13 canonical
/// page types with labels, descriptions, and required documents. Drives the
/// create picker and the owner document-upload UI.
class PageTypeDef {
  final String value;
  final String label;
  final String description;
  final List<String> requiredDocuments;
  final List<String> optionalDocuments;

  const PageTypeDef(
    this.value,
    this.label,
    this.description, {
    this.requiredDocuments = const [],
    this.optionalDocuments = const [],
  });
}

const List<PageTypeDef> kPageTypes = [
  PageTypeDef('business', 'Business Page', 'Shops, companies, restaurants, agencies, local businesses.', requiredDocuments: ['address_proof'], optionalDocuments: ['business_registration']),
  PageTypeDef('creator', 'Creator Page', 'Video creators, educators, artists, coaches, writers, influencers.', requiredDocuments: ['identity_proof']),
  PageTypeDef('celebrity', 'Celebrity Page', 'Actors, musicians, sports personalities, public figures.', requiredDocuments: ['identity_proof', 'official_proof']),
  PageTypeDef('foundation_ngo', 'Foundation / NGO Page', 'Charities, NGOs, social work groups, public welfare orgs.', requiredDocuments: ['ngo_registration', 'address_proof']),
  PageTypeDef('authority_government', 'Authority / Government Page', 'Government departments, public authorities, official services.', requiredDocuments: ['government_authorization', 'official_email_domain_proof']),
  PageTypeDef('institution', 'Institution Page', 'Schools, colleges, universities, coaching & training institutions.', requiredDocuments: ['institution_registration', 'address_proof']),
  PageTypeDef('community_organization', 'Community / Organization Page', 'Associations, clubs, societies, local & professional groups.', optionalDocuments: ['other']),
  PageTypeDef('media_news', 'Media / News Page', 'News outlets, journalists, magazines, publications, media networks.', requiredDocuments: ['media_publication_proof']),
  PageTypeDef('brand', 'Brand Page', 'Product brands, labels, franchises, official brand identities.', requiredDocuments: ['brand_ownership_proof']),
  PageTypeDef('professional', 'Professional Page', 'Doctors, lawyers, consultants, architects, licensed professionals.', requiredDocuments: ['identity_proof', 'professional_license']),
  PageTypeDef('marketplace_seller', 'Marketplace Seller Page', 'Sellers, merchants, handmade creators, store owners, distributors.', requiredDocuments: ['identity_proof', 'seller_address_proof']),
  PageTypeDef('food_partner', 'Food Partner Page', 'Restaurants, cloud kitchens, bakeries, tiffin centers, caterers.', requiredDocuments: ['identity_proof'], optionalDocuments: ['fssai_license']),
  PageTypeDef('service_provider', 'Service Provider Page', 'Electricians, plumbers, repair, cleaners, mechanics, movers.', requiredDocuments: ['identity_proof', 'service_proof']),
];

PageTypeDef? pageTypeByValue(String value) {
  for (final t in kPageTypes) {
    if (t.value == value) return t;
  }
  return null;
}

/// "identity_proof" → "Identity Proof"
String documentLabel(String dt) => dt
    .split('_')
    .map((p) => p.isEmpty ? p : '${p[0].toUpperCase()}${p.substring(1)}')
    .join(' ');
