package com.us.android.feature.rider.gate

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.FoodCapabilities
import com.us.android.core.food.network.FoodCapabilitiesDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

class RiderGateTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val contracts = File("src/test/resources/contracts")

    private fun capabilities(fixture: String): FoodCapabilities {
        val dto = checkNotNull(
            strict.decodeFromString(ApiEnvelope.serializer(FoodCapabilitiesDto.serializer()), File(contracts, fixture).readText()).data,
        )
        return FoodCapabilities(dto.userId, dto.isCustomer, dto.isRestaurantOwner, dto.isDeliveryPartner, dto.isAdmin, dto.isModerator)
    }

    private fun caps(owner: Boolean = false, rider: Boolean = false, admin: Boolean = false, moderator: Boolean = false) =
        FoodCapabilities("u-1", isCustomer = true, isRestaurantOwner = owner, isDeliveryPartner = rider, isAdmin = admin, isModerator = moderator)

    @Test
    fun `a delivery partner is let in`() {
        val gate = RiderGate.from(FoodResult.Success(capabilities("me_capabilities_get_200_all_roles.json")))
        assertThat(gate).isEqualTo(RiderGate.DeliveryPartner("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0001"))
    }

    @Test
    fun `a customer is offered become-a-rider, not the rider screens`() {
        val gate = RiderGate.from(FoodResult.Success(capabilities("me_capabilities_get_200_customer.json")))
        assertThat(gate).isEqualTo(RiderGate.NotDeliveryPartner("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0010"))
    }

    @Test
    fun `restaurant owner, admin and moderator roles do not open the rider app`() {
        for (c in listOf(caps(owner = true), caps(admin = true), caps(moderator = true), caps(owner = true, admin = true, moderator = true))) {
            assertThat(RiderGate.from(FoodResult.Success(c))).isEqualTo(RiderGate.NotDeliveryPartner("u-1"))
        }
    }

    @Test
    fun `a failed capabilities call fails closed`() {
        assertThat(RiderGate.from(FoodResult.Failure(FoodError.Unauthorized))).isEqualTo(RiderGate.SessionExpired)
        for (error in listOf(FoodError.Network(null), FoodError.NotAvailable, FoodError.Unexpected(500, null, null))) {
            val gate = RiderGate.from(FoodResult.Failure(error))
            assertThat(gate).isEqualTo(RiderGate.Unavailable(error))
        }
    }

    @Test
    fun `this module's fixture copies are byte-identical to the food-service goldens`() {
        val source = File("../../../../Architecture/services/food-service/internal/http/testdata/contracts")
        assumeTrue("food-service is not checked out beside the app", source.isDirectory)
        val copies = contracts.listFiles { f -> f.name.endsWith(".json") }.orEmpty()
        assertThat(copies).isNotEmpty()
        for (copy in copies) {
            assertThat(copy.readBytes()).isEqualTo(File(source, copy.name).readBytes())
        }
    }
}
