package com.us.android.core.datastore

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.test.runTest
import org.junit.Test
import java.time.LocalDateTime
import java.time.ZoneId
import java.time.ZoneOffset

class UsageAccumulatorTest {

    private class FakeStore(var records: List<UsageRecord> = emptyList()) : UsageStore {
        var writes = 0
        override suspend fun read() = records
        override suspend fun write(records: List<UsageRecord>) {
            this.records = records
            writes++
        }
    }

    private val zone: ZoneId = ZoneOffset.UTC
    private var clock = 0L

    private fun at(text: String): Long = LocalDateTime.parse(text).atZone(zone).toInstant().toEpochMilli()

    private fun accumulator(store: FakeStore = FakeStore()) = UsageAccumulator(store, zone) { clock }

    @Test
    fun `foreground time lands on today and each entry is one session`() = runTest {
        val store = FakeStore()
        val accumulator = accumulator(store)

        clock = at("2026-09-03T10:00:00")
        accumulator.onForeground()
        clock = at("2026-09-03T10:05:00")
        accumulator.onBackground()
        clock = at("2026-09-03T11:00:00")
        accumulator.onForeground()
        clock = at("2026-09-03T11:00:30")

        val records = accumulator.snapshot()

        assertThat(records).containsExactly(UsageRecord("2026-09-03", 330_000L, sessions = 2))
        assertThat(accumulator.todaySeconds.value).isEqualTo(330L)
        assertThat(store.records).isEqualTo(records)
    }

    @Test
    fun `a session across midnight is split at the boundary`() = runTest {
        val accumulator = accumulator()

        clock = at("2026-09-03T23:50:00")
        accumulator.onForeground()
        clock = at("2026-09-04T00:20:00")

        val records = accumulator.snapshot()

        assertThat(records).containsExactly(
            UsageRecord("2026-09-03", 600_000L, sessions = 1),
            UsageRecord("2026-09-04", 1_200_000L, sessions = 1),
        ).inOrder()
        assertThat(accumulator.todaySeconds.value).isEqualTo(1_200L)
    }

    @Test
    fun `markFlushed drops a confirmed past day but keeps today`() = runTest {
        val accumulator = accumulator()
        clock = at("2026-09-03T23:50:00")
        accumulator.onForeground()
        clock = at("2026-09-04T00:20:00")
        accumulator.snapshot()

        accumulator.markFlushed("2026-09-03")
        accumulator.markFlushed("2026-09-04")

        assertThat(accumulator.snapshot().map { it.date }).containsExactly("2026-09-04")
    }

    @Test
    fun `the ledger survives a relaunch and keeps counting`() = runTest {
        val store = FakeStore(listOf(UsageRecord("2026-09-03", 60_000L, sessions = 3)))
        val accumulator = accumulator(store)

        clock = at("2026-09-03T12:00:00")
        accumulator.onForeground()
        clock = at("2026-09-03T12:01:00")

        assertThat(accumulator.snapshot()).containsExactly(UsageRecord("2026-09-03", 120_000L, sessions = 4))
    }

    @Test
    fun `a second onForeground without a background is not a new session`() = runTest {
        val accumulator = accumulator()
        clock = at("2026-09-03T12:00:00")
        accumulator.onForeground()
        clock = at("2026-09-03T12:00:10")
        accumulator.onForeground()

        assertThat(accumulator.snapshot().single().sessions).isEqualTo(1)
    }

    @Test
    fun `the ledger codec round-trips and skips corrupt entries`() {
        val records = listOf(
            UsageRecord("2026-09-03", 1_000L, 2),
            UsageRecord("2026-09-04", 5L, 1),
        )

        assertThat(UsageLedgerCodec.decode(UsageLedgerCodec.encode(records))).isEqualTo(records)
        assertThat(UsageLedgerCodec.decode("garbage;2026-09-05|x|1;2026-09-06|10|1"))
            .containsExactly(UsageRecord("2026-09-06", 10L, 1))
    }
}
