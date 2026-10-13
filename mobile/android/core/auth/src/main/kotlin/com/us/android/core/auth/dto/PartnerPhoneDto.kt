package com.us.android.core.auth.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class PartnerPhoneRequestDto(
    val mode: String,
    val phone: String,
    @SerialName("first_name") val firstName: String = "",
    @SerialName("last_name") val lastName: String = "",
    val gender: String = "",
    @SerialName("accepted_terms") val acceptedTerms: Boolean = false,
    @SerialName("terms_version") val termsVersion: String,
) { override fun toString() = "PartnerPhoneRequestDto(redacted)" }

@Serializable
data class PartnerPhoneChallengeDto(
    @SerialName("challenge_id") val challengeId: String,
    val delivery: String,
    @SerialName("expires_in") val expiresIn: Int,
    @SerialName("resend_after") val resendAfter: Int,
) { override fun toString() = "PartnerPhoneChallengeDto(redacted)" }

@Serializable
data class PartnerPhoneVerifyDto(
    @SerialName("challenge_id") val challengeId: String,
    val code: String,
    @SerialName("device_id") val deviceId: String,
    val platform: String = "android",
) { override fun toString() = "PartnerPhoneVerifyDto(redacted)" }
