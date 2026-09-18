package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.mopedu.captain.data.AcceptOfferResponseDto
import com.us.android.feature.mopedu.captain.data.CaptainEarningsDto
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainErrorEnvelopeDto
import com.us.android.feature.mopedu.captain.data.CaptainOfferDto
import com.us.android.feature.mopedu.captain.data.PartnerProfileDto
import com.us.android.feature.mopedu.captain.data.RidePaymentDto
import com.us.android.feature.mopedu.captain.data.toOffer
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture from rider-service's partner routes decodes
 * STRICTLY into its DTO. No fixtures exist yet: until they are dropped into
 * `src/test/resources/contracts`, every test here is ASSUMED away.
 */
class CaptainContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    private val parsers: Map<String, (name: String, raw: String) -> Unit> = mapOf(
        "partners_me_get_200.json" to data(PartnerProfileDto.serializer()) { assertThat(it.id).isNotEmpty() },
        "partners_me_get_404.json" to error { assertThat(it).isEqualTo(CaptainError.NotFound) },
        "offers_incoming_get_200.json" to data(ListSerializer(CaptainOfferDto.serializer())) { list ->
            list.forEach { assertThat(it.toOffer().estimatedEarnings.paise).isGreaterThan(0L) }
        },
        "offers_accept_post_200.json" to data(AcceptOfferResponseDto.serializer()) { assertThat(it.rideId).isNotEmpty() },
        "rides_payment_get_200.json" to data(RidePaymentDto.serializer()) { assertThat(it.status).isNotEmpty() },
        "partners_me_earnings_get_200.json" to data(CaptainEarningsDto.serializer()) { assertThat(it.todayEarningsPaise).isAtLeast(0L) },
    )

    private fun fixtures(): Set<String> = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser`() {
        assumeTrue("no rider-service partner fixtures yet", fixtures().isNotEmpty())
        assertThat(fixtures() - parsers.keys).isEmpty()
    }

    @Test
    fun `every fixture decodes strictly into its DTO`() {
        assumeTrue("no rider-service partner fixtures yet", fixtures().isNotEmpty())
        for (name in fixtures()) {
            val raw = File(contractsDir, name).readText()
            try {
                parsers.getValue(name)(name, raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String, String) -> Unit = { _, raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        assertThat(envelope.error).isNull()
        check(checkNotNull(envelope.data))
    }

    private fun error(check: (CaptainError) -> Unit): (String, String) -> Unit = { name, raw ->
        val status = checkNotNull(STATUS.find(name)) { "fixture name $name carries no HTTP status" }.groupValues[1].toInt()
        strict.decodeFromString(CaptainErrorEnvelopeDto.serializer(), raw)
        check(CaptainError.from(status, raw, strict))
    }

    private companion object {
        val STATUS = Regex("""_(\d{3})(?:_|\.json)""")
    }
}
