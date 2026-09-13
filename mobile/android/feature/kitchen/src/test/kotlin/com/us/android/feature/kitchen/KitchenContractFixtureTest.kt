package com.us.android.feature.kitchen

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.OnboardingStep
import com.us.android.core.food.network.AcceptingDto
import com.us.android.core.food.network.ComplianceDto
import com.us.android.core.food.network.FoodCapabilitiesDto
import com.us.android.core.food.network.FoodErrorEnvelopeDto
import com.us.android.core.food.network.FssaiDto
import com.us.android.core.food.network.LocationDto
import com.us.android.core.food.network.OperatingHoursDto
import com.us.android.core.food.network.PayoutAccountDto
import com.us.android.core.food.network.SubmitDto
import com.us.android.core.food.repository.FoodError
import com.us.android.feature.kitchen.onboarding.KitchenChecklist
import com.us.android.feature.kitchen.onboarding.RowStatus
import com.us.android.feature.kitchen.onboarding.StepEditor
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * The food-service golden fixtures the Kitchen screens read still decode
 * STRICTLY (unknown keys fail) into the DTOs those screens use, and still map to
 * what the screens show.
 *
 * Two sources: the onboarding goldens :core:food already carries (read in place,
 * so there is one copy of each), and the four this module adds under
 * src/test/resources/contracts — capabilities (the role gate) and the location
 * `state` errors (the location step) — which :core:food does not have.
 */
class KitchenContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val coreFood = File("../../core/food/src/test/resources/contracts")
    private val kitchen = File("src/test/resources/contracts")

    private fun <T> data(dir: File, name: String, serializer: KSerializer<T>): T {
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), File(dir, name).readText())
        assertThat(envelope.error).isNull()
        return checkNotNull(envelope.data) { "$name carried no data" }
    }

    private fun error(dir: File, name: String): FoodError {
        val status = Regex("""_(\d{3})(?:_|\.json)""").find(name)!!.groupValues[1].toInt()
        val envelope = strict.decodeFromString(FoodErrorEnvelopeDto.serializer(), File(dir, name).readText())
        return FoodError.from(status, checkNotNull(envelope.error))
    }

    @Test
    fun `the role gate's capabilities fixtures decode`() {
        val all = data(kitchen, "me_capabilities_get_200_all_roles.json", FoodCapabilitiesDto.serializer())
        assertThat(all.isRestaurantOwner).isTrue()
        val customer = data(kitchen, "me_capabilities_get_200_customer.json", FoodCapabilitiesDto.serializer())
        assertThat(customer.isRestaurantOwner).isFalse()
        assertThat(customer.isCustomer).isTrue()
    }

    @Test
    fun `a not-ready submit builds the checklist the onboarding screen shows`() {
        val notReady = error(coreFood, "submit_post_422_not_ready.json") as FoodError.NotReady
        val checklist = KitchenChecklist.fromMissing(notReady.missing)

        assertThat(checklist.rows.filter { it.status == RowStatus.TO_DO }.map { it.editor })
            .containsExactly(StepEditor.FSSAI, StepEditor.PAYOUT).inOrder()
        assertThat(checklist.isReady).isFalse()

        val submitted = data(coreFood, "submit_post_200.json", SubmitDto.serializer())
        assertThat(submitted.status).isEqualTo("PENDING_REVIEW")
        assertThat(KitchenChecklist.fromMissing(submitted.missing).isReady).isTrue()
    }

    @Test
    fun `the step screens' success fixtures decode`() {
        assertThat(data(coreFood, "location_put_200.json", LocationDto.serializer()).deliveryRadiusKm).isEqualTo(6.5)
        assertThat(data(coreFood, "operating_hours_put_200.json", OperatingHoursDto.serializer()).timezone)
            .isEqualTo("Asia/Kolkata")
        assertThat(data(coreFood, "compliance_put_200.json", ComplianceDto.serializer()).panMasked).isEqualTo("****000Z")
        assertThat(data(coreFood, "fssai_put_200.json", FssaiDto.serializer()).document?.status).isEqualTo("PENDING")
        val payout = data(coreFood, "restaurant_payout_account_put_200.json", PayoutAccountDto.serializer())
        assertThat(payout.accountNumberMasked).startsWith("****")
        assertThat(data(coreFood, "accepting_patch_200.json", AcceptingDto.serializer()).isAcceptingOrders).isTrue()
    }

    @Test
    fun `the location step's field errors name the field they belong to`() {
        for (name in listOf("location_put_422_state_invalid.json", "location_put_422_state_required.json")) {
            val failure = error(kitchen, name) as FoodError.InvalidField
            assertThat(failure.field).isEqualTo("state")
        }
        val radius = error(coreFood, "location_put_422_radius.json") as FoodError.InvalidField
        assertThat(radius.field).isEqualTo("delivery_radius_km")
        assertThat(OnboardingStep.fromWire("state")).isEqualTo(OnboardingStep.STATE)
    }

    @Test
    fun `this module's copies are byte-identical to the food-service goldens`() {
        val source = File("../../../../Architecture/services/food-service/internal/http/testdata/contracts")
        assumeTrue("food-service is not checked out beside the app", source.isDirectory)
        val copies = kitchen.listFiles { f -> f.name.endsWith(".json") }.orEmpty()
        assertThat(copies).isNotEmpty()
        for (copy in copies) {
            assertThat(copy.readBytes()).isEqualTo(File(source, copy.name).readBytes())
        }
    }
}
