package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.Test
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.time.Instant

class CommerceWorkTest {
    @Test fun offersRequireAbsoluteDeadlineAndNeverRenewLocally() {
        val now = Instant.parse("2026-10-11T10:00:00Z")
        fun offer(expiry: String) = buildJsonObject { put("expires_at", expiry) }
        assertThat(commerceOfferSeconds(offer("2026-10-11T10:00:20Z"), now)).isEqualTo(20)
        assertThat(commerceOfferSeconds(offer("2026-10-11T10:00:00Z"), now)).isEqualTo(0)
        assertThat(commerceOfferSeconds(offer("2026-10-11T09:59:59Z"), now)).isEqualTo(0)
        assertThat(commerceOfferSeconds(offer(""), now)).isEqualTo(0)
        assertThat(commerceOfferSeconds(offer("not a date"), now)).isEqualTo(0)
    }
    @Test fun onlyValidTransitionsAreOfferedAndCompletionRequiresVerification() {
        assertThat(commerceJobActions("assigned")).containsExactly("pickup", "failed", "support")
        assertThat(commerceJobActions("picked_up")).containsExactly("deliver", "failed", "support")
        assertThat(commerceJobActions("delivered")).containsExactly("support")
        assertThat(commerceJobActions("cancelled")).containsExactly("support")
        assertThat(commerceJobActions("unassigned")).isEmpty()
        assertThat(commerceJobActions("unknown")).isEmpty()
    }
    @Test fun malformedSuccessCannotClearAcceptedWorkAsIfEmpty() {
        assertThat(runCatching { commerceData(Response.success(buildJsonObject {})) }.isFailure).isTrue()
        assertThat(commerceData(Response.success(buildJsonObject { put("data", JsonNull) }))).isNull()
        assertThat(runCatching { buildJsonObject {}.commerceRows() }.isFailure).isTrue()
        assertThat(runCatching { JsonPrimitive("broken").commerceObject(nullable = true) }.isFailure).isTrue()
        assertThat(runCatching { (null as JsonElement?).commerceObject() }.isFailure).isTrue()
        assertThat((null as JsonElement?).commerceObject(nullable = true)).isNull()
    }
    @Test fun payoutPreservesPaise() {
        assertThat(commerceMoney(12345)).contains("123.45")
    }
    @Test fun unconfiguredAreaCannotEnableDuty() {
        val areas = listOf(buildJsonObject { put("id", "enabled-area") })
        assertThat(commerceAreaSelected(areas, "00000000-0000-0000-0000-000000000000")).isFalse()
        assertThat(commerceAreaSelected(areas, "")).isFalse()
        assertThat(commerceAreaSelected(emptyList(), "enabled-area")).isFalse()
        assertThat(commerceAreaSelected(areas, "enabled-area")).isTrue()
    }
    @Test fun pausedOrStaleLocationNeverPromisesOngoingOffers() {
        val now=Instant.parse("2026-10-11T10:00:00Z")
        assertThat(commerceLocationMessage(false,CommerceLocationState(),now)).isNull()
        assertThat(commerceLocationMessage(true,CommerceLocationState(),now)).contains("paused")
        assertThat(commerceLocationMessage(true,CommerceLocationState(running=true),now)).contains("Checking")
        assertThat(commerceLocationMessage(true,CommerceLocationState(running=true,lastUpdated=now.minusSeconds(1800)),now)).contains("New requests are paused")
        assertThat(commerceLocationMessage(true,CommerceLocationState(running=true,lastUpdated=now.minusSeconds(30)),now)).isNull()
    }
    @Test fun workerEndpointsAndRevisionAreSentWithoutMerchantPaths() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            val api = Retrofit.Builder().baseUrl(server.url("/"))
                .addConverterFactory(NetworkModule.provideJson().asConverterFactory("application/json".toMediaType()))
                .build().create(CommerceWorkApi::class.java)
            fun reply() { server.enqueue(MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body("""{"data":null}""").build()) }
            reply(); api.action("job-1", "pickup", buildJsonObject { put("code", "123456"); put("expected_revision", 7) })
            val pickup = server.takeRequest()
            assertThat(pickup.target).isEqualTo("/v1/commerce/delivery/jobs/job-1/pickup")
            assertThat(pickup.body?.utf8()).contains("\"expected_revision\":7")
            reply(); api.decide("offer-1", "accept", buildJsonObject {})
            assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/delivery/offers/offer-1/accept")
            reply(); api.current(); assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/delivery/jobs/current")
            reply(); api.contact("job-1"); assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/delivery/jobs/job-1/contact")
        }
    }
}
