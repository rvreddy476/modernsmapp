package com.us.android.core.profile.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** `GET/PUT /v1/users/me/modules` payload. Defaults mirror the no-row answer. */
@Serializable
data class ModulePreferencesDto(
    @SerialName("modules") val modules: List<String> = emptyList(),
    @SerialName("home_module") val homeModule: String = "feed",
    @SerialName("onboarding_completed_at") val onboardingCompletedAt: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

/** Full snapshot: the server replaces the row, so every field is sent. */
@Serializable
data class UpdateModulePreferencesRequest(
    @SerialName("modules") val modules: List<String>,
    @SerialName("home_module") val homeModule: String,
    @SerialName("complete_onboarding") val completeOnboarding: Boolean,
)
