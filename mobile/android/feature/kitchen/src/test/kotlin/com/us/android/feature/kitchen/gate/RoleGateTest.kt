package com.us.android.feature.kitchen.gate

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.FoodCapabilities
import com.us.android.core.food.network.FoodCapabilitiesDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.File

class RoleGateTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private fun capabilities(fixture: String): FoodCapabilities {
        val raw = File("src/test/resources/contracts/$fixture").readText()
        val dto = checkNotNull(strict.decodeFromString(ApiEnvelope.serializer(FoodCapabilitiesDto.serializer()), raw).data)
        return FoodCapabilities(
            userId = dto.userId,
            isCustomer = dto.isCustomer,
            isRestaurantOwner = dto.isRestaurantOwner,
            isDeliveryPartner = dto.isDeliveryPartner,
            isAdmin = dto.isAdmin,
            isModerator = dto.isModerator,
        )
    }

    private fun caps(owner: Boolean = false, rider: Boolean = false, admin: Boolean = false, moderator: Boolean = false) =
        FoodCapabilities("u-1", isCustomer = true, isRestaurantOwner = owner, isDeliveryPartner = rider, isAdmin = admin, isModerator = moderator)

    @Test
    fun `a restaurant owner is let in`() {
        val gate = RoleGate.from(FoodResult.Success(capabilities("me_capabilities_get_200_all_roles.json")))
        assertThat(gate).isEqualTo(RoleGate.RestaurantPartner("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0001"))
    }

    @Test
    fun `a customer sees the restaurant-partners-only screen`() {
        val gate = RoleGate.from(FoodResult.Success(capabilities("me_capabilities_get_200_customer.json")))
        assertThat(gate).isEqualTo(RoleGate.NotRestaurantPartner)
    }

    @Test
    fun `admin, moderator and rider roles do not open a kitchen`() {
        for (c in listOf(caps(rider = true), caps(admin = true), caps(moderator = true), caps(rider = true, admin = true, moderator = true))) {
            assertThat(RoleGate.from(FoodResult.Success(c))).isEqualTo(RoleGate.NotRestaurantPartner)
        }
        assertThat(RoleGate.from(FoodResult.Success(caps(owner = true)))).isEqualTo(RoleGate.RestaurantPartner("u-1"))
    }

    @Test
    fun `a failed capabilities call fails closed`() {
        assertThat(RoleGate.from(FoodResult.Failure(FoodError.Unauthorized))).isEqualTo(RoleGate.SessionExpired)
        for (error in listOf(FoodError.Network(null), FoodError.NotAvailable, FoodError.Unexpected(500, null, null))) {
            val gate = RoleGate.from(FoodResult.Failure(error))
            assertThat(gate).isEqualTo(RoleGate.Unavailable(error))
            assertThat(gate).isNotInstanceOf(RoleGate.RestaurantPartner::class.java)
        }
    }
}
