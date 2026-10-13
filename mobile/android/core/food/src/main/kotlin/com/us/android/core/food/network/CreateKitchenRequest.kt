package com.us.android.core.food.network

import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName

@Serializable
data class CreateKitchenRequest(
    val name: String,
    val slug: String,
    @SerialName("address_line1") val addressLine1: String,
    val city: String,
)
