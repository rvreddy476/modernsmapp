package com.us.android.feature.doorsteppro.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class AlternatePhoneChallengeDto(val delivery: String, @SerialName("expires_in") val expiresIn: Int, @SerialName("resend_after") val resendAfter: Int)
@Serializable
data class AlternatePhoneCodeDto(val code: String) { override fun toString() = "AlternatePhoneCodeDto(redacted)" }
@Serializable
data class AlternatePhoneResultDto(val verified: Boolean)

@Serializable
data class ApplicationStateDto(val code: String, val name: String)

@Serializable
data class PartnerApplicationDto(
    @SerialName("first_name") val firstName: String,
    @SerialName("last_name") val lastName: String,
    @SerialName("declared_gender") val declaredGender: String,
    @SerialName("state_code") val stateCode: String,
    @SerialName("state_name") val stateName: String,
    val town: String,
    @SerialName("alternate_phone_last4") val alternatePhoneLast4: String? = null,
    @SerialName("primary_phone_last4") val primaryPhoneLast4: String,
    @SerialName("alternate_phone_verified_at") val alternatePhoneVerifiedAt: String? = null,
    @SerialName("submitted_at") val submittedAt: String,
)

@Serializable
data class PartnerApplicationRequest(
    @SerialName("review_consent") val reviewConsent: Boolean,
    @SerialName("first_name") val firstName: String,
    @SerialName("last_name") val lastName: String,
    val gender: String,
    @SerialName("state_code") val stateCode: String,
    val town: String,
    @SerialName("alternate_phone") val alternatePhone: String = "",
    @SerialName("skill_codes") val skillCodes: List<String>,
    @SerialName("identity_kind") val identityKind: String,
    @SerialName("identity_media_id") val identityMediaId: String,
    @SerialName("experience_media_id") val experienceMediaId: String = "",
    val pan: String,
    val bank: BankRequest,
    @SerialName("selfie_media_id") val selfieMediaId: String = "",
    @SerialName("introduction_media_id") val introductionMediaId: String = "",
) { override fun toString() = "PartnerApplicationRequest(redacted)" }
