package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.*
import org.junit.Test
import java.time.Instant

class PartnerWorkSessionTest {
    private val now = Instant.parse("2026-10-11T10:00:00Z")
    private fun state(category: String? = "moped", reservations: String = "[]") = Json.parseToJsonElement("""{"session":{"category":${category?.let { "\"$it\"" } ?: "null"},"revision":8},"reservations":$reservations}""")
    private fun reservation(state: String = "active", expires: String = "2026-10-11T10:00:20Z", category: String = "moped") = """{"category":"$category","job_id":"job-1","state":"$state","starts_at":"2026-10-11T08:00:00Z","ends_at":"2026-10-11T09:00:00Z","offer_expires_at":"$expires"}"""

    @Test fun exactlyFourDutyCategoriesAndNullOffline() {
        assertThat(listOf("moped", "home_services", "commerce_delivery", "food_delivery").map { parseWorkSession(state(it), now).category }).containsExactlyElementsIn(PartnerBusiness.entries)
        assertThat(parseWorkSession(state(null), now).category).isNull()
        assertThat(workBusiness("fish")).isNull()
        assertThat(workBusiness("restaurant")).isNull()
    }
    @Test fun serverRevisionIsPreserved() { assertThat(parseWorkSession(state(), now).revision).isEqualTo(8) }
    @Test fun passedEstimatedEndDoesNotAbandonAcceptedJob() {
        assertThat(parseWorkSession(state(reservations = "[${reservation()}]"), now).reservations).hasSize(1)
    }
    @Test fun expiredOfferIsNotShownAsFreshRequest() {
        assertThat(parseWorkSession(state(reservations = "[${reservation("offered", "2026-10-11T10:00:00Z")}]"), now).reservations).isEmpty()
        assertThat(parseWorkSession(state(reservations = "[${reservation("offered")}]"), now).reservations).hasSize(1)
    }
    @Test fun invalidUnknownAndIncompleteDataNeverMeansOfflineOrPending() {
        listOf(JsonNull, buildJsonObject {}, state("fish"), Json.parseToJsonElement("""{"session":{"category":null},"reservations":[]}""")).forEach {
            assertThat(runCatching { parseWorkSession(it, now) }.isFailure).isTrue()
        }
    }
    @Test fun historicalStatesAreNotActiveReservations() {
        assertThat(parseWorkSession(state(reservations = "[${reservation("released")},${reservation("expired")}]"), now).reservations).isEmpty()
    }
}
