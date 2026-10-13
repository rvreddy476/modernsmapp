package com.us.android.core.network

import kotlinx.serialization.Serializable

/** Minimum authenticated work-session contract required for a duty mutation. */
@Serializable
data class PartnerWorkReadbackDto(val session: PartnerWorkSessionRevisionDto)

@Serializable
data class PartnerWorkSessionRevisionDto(val revision: Long, val category: String? = null)
