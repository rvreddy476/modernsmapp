package com.us.android.feature.mopedu.captain.data

/**
 * rider-service's `rider_document_type` values the onboarding uses, as the
 * server names them in `POST /partners/me/documents` and in `review.pending`.
 */
object CaptainDocumentTypes {
    const val AADHAAR = "aadhaar"

    /**
     * The selfie. On the wire it is `profile_photo` — the enum has no `selfie`
     * member (service/approval.go: `DocSelfie = "profile_photo"`), and the
     * automatic face check looks for exactly that type with a `media_id`.
     */
    const val SELFIE = "profile_photo"
    const val DRIVING_LICENCE = "driving_license"
    const val VEHICLE_RC = "vehicle_rc"
}
