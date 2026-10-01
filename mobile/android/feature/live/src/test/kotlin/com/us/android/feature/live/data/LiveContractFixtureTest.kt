package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * live-service-v2's golden fixtures decode into the live DTOs.
 *
 * Copied byte for byte from `internal/http/testdata/contracts/mtube` into
 * src/test/resources/contracts/live. The app's Json is used, not a strict
 * one: [LiveStreamDto] deliberately does not claim the whole row
 * (`cover_media_id`, `scheduled_at`, `updated_at` are not read by Android).
 * `ended_reason: null` must still decode to the empty default.
 *
 * [parsers] must name every fixture, and every parser must have one.
 */
class LiveContractFixtureTest {

    private val json = NetworkModule.provideJson()

    private val contractsDir = File("src/test/resources/contracts/live")

    private val parsers: Map<String, (String) -> Unit> = mapOf(
        "livestreams_scheduled.json" to { raw ->
            val serializer = ApiEnvelope.serializer(ListSerializer(LiveStreamDto.serializer()))
            val envelope = json.decodeFromString(serializer, raw)
            val rows = checkNotNull(envelope.data)
            assertThat(rows).hasSize(2)
            rows.forEach { row ->
                assertThat(liveStatusOf(row.status)).isEqualTo(LiveStatus.Scheduled)
                assertThat(endedReasonOf(row.endedReason)).isEqualTo(EndedReason.Unknown)
                assertThat(row.viewerCount).isEqualTo(0)
                assertThat(row.statusChangedAt).isNotEmpty()
                assertThat(row.moderatorUserIds).isNull()
            }
            assertThat(rows.first().title).isEqualTo("Kafka AMA")
        },
    )

    @Test
    fun `every fixture has a parser and every parser has a fixture`() {
        val fixtures = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()
        assertThat(fixtures).isEqualTo(parsers.keys)
    }

    @Test
    fun `every fixture decodes into its DTO`() {
        parsers.forEach { (name, parse) -> parse(File(contractsDir, name).readText()) }
    }

    @Test
    fun `the copies are byte-identical to the live-service-v2 goldens`() {
        val source = File("../../../../Architecture/services/live-service-v2/internal/http/testdata/contracts/mtube")
        assumeTrue("live-service-v2 is not checked out beside the app", source.isDirectory)

        for (copy in contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty()) {
            val original = File(source, copy.name)
            assertThat(original.exists()).isTrue()
            assertThat(copy.readBytes()).isEqualTo(original.readBytes())
        }
    }
}
