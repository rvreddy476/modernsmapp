package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.PartnerOfferCategory
import kotlinx.serialization.json.*
import java.time.Instant
import java.util.UUID
import org.junit.Test

class PartnerRequestCardTest {
    private fun row(extra: String): JsonObject = Json.parseToJsonElement("""{"id":"${UUID.randomUUID()}","expires_at":"${Instant.now().plusSeconds(20)}",$extra}""").jsonObject
    @Test fun everyCategoryUsesItsAuthoritativeAmountAndLocation() {
        val home=partnerRequestCard(PartnerOfferCategory.HOME,row(""""service_name":"AC service","locality":"Uppal","earning_estimate_paise":65000"""))!!
        assertThat(home.service).isEqualTo("AC service");assertThat(home.location).isEqualTo("Uppal");assertThat(home.amount).isEqualTo(65000)
        val food=partnerRequestCard(PartnerOfferCategory.FOOD,row(""""restaurant":{"name":"Pickup kitchen"},"drop_area":{"locality":"Uppal"},"payout_paise":9000"""))!!
        assertThat(food.location).isEqualTo("Pickup kitchen → Uppal");assertThat(food.amount).isEqualTo(9000)
        val commerce=partnerRequestCard(PartnerOfferCategory.COMMERCE,row(""""pickup_label":"Shop","drop_label":"Uppal","payout_paise":12000"""))!!
        assertThat(commerce.location).isEqualTo("Shop → Uppal");assertThat(commerce.amount).isEqualTo(12000)
        val moped=partnerRequestCard(PartnerOfferCategory.MOPED,row(""""pickup_address":"Uppal","drop_address":"Peerzadiguda","estimated_fare_paise":12345"""))!!
        assertThat(moped.location).isEqualTo("Uppal → Peerzadiguda");assertThat(moped.amount).isEqualTo(12345);assertThat(moped.amountLabel).isEqualTo("Estimated fare")
    }
    @Test fun missingOrNegativeAmountIsNotInvented() {
        assertThat(partnerRequestCard(PartnerOfferCategory.FOOD,row(""""status":"pending""""))!!.amount).isNull()
        assertThat(partnerRequestCard(PartnerOfferCategory.COMMERCE,row(""""payout_paise":-1"""))!!.amount).isNull()
        assertThat(partnerRequestCard(PartnerOfferCategory.HOME,row(""""status":"cancelled""""))).isNull()
    }
}
