package com.us.android.core.media

import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.sound.FakeExoPlayer
import com.us.android.core.media.sound.FakePlayerFactory
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * reels-14: the REAL [PlayerPool], driven the way the Reels pager drives it
 * (settle → acquire, play only that page, warm both neighbours), over a fake
 * player factory. Fifty swipes: exactly one player is ever asked to play, the
 * pool never holds more than [PlayerPool.MAX_PLAYERS], and release lets every
 * player go.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@OptIn(UnstableApi::class)
class PlayerPoolTest {

    private val factory = FakePlayerFactory()

    /** Builds real media sources over a data source no test ever opens: the fakes never load bytes. */
    private val pool = PlayerPool(
        playerFactory = factory,
        sources = MediaSources(
            dataSourceFactory = DataSource.Factory { error("no network in this test") },
            offlineDataSourceFactory = dagger.Lazy { error("no offline copies in this test") },
        ),
        dataSaver = NoDataSaver,
    )

    private fun reel(index: Int) = Playback.original("https://media.test/reel-$index.mp4")

    /** What the pager does when page [index] settles. */
    private fun settle(index: Int, count: Int): FakeExoPlayer {
        val player = pool.acquire(index, reel(index), "r$index")
        pool.playOnly(index)
        listOf(index - 1, index + 1).filter { it in 0 until count }.forEach { pool.preload(it, reel(it), "r$it") }
        return factory.made.single { it.player === player }
    }

    private fun playing() = factory.made.filter { it.playWhenReady }

    @Test
    fun `fifty swipes down keep exactly one player playing and never more than four players`() {
        repeat(SWIPES) { index ->
            val current = settle(index, SWIPES)

            assertThat(playing()).containsExactly(current)
            assertThat(factory.made.size).isAtMost(PlayerPool.MAX_PLAYERS)
        }
        assertThat(factory.made).hasSize(PlayerPool.MAX_PLAYERS)
        assertThat(factory.made.none { it.released }).isTrue()
    }

    @Test
    fun `swiping back up the same way keeps one player playing`() {
        repeat(SWIPES) { settle(it, SWIPES) }
        for (index in SWIPES - 1 downTo 0) {
            val current = settle(index, SWIPES)
            assertThat(playing()).containsExactly(current)
        }
        assertThat(factory.made.size).isAtMost(PlayerPool.MAX_PLAYERS)
    }

    @Test
    fun `a recycled player is stopped and cleared before it is prepared again`() {
        repeat(PlayerPool.MAX_PLAYERS + 2) { settle(it, SWIPES) }

        val recycled = factory.made.filter { it.calls.contains("clearMediaItems") }
        assertThat(recycled).isNotEmpty()
        recycled.forEach { fake ->
            val stop = fake.calls.lastIndexOf("stop")
            assertThat(stop).isLessThan(fake.calls.lastIndexOf("prepare"))
        }
    }

    @Test
    fun `pauseAll leaves nothing playing`() {
        settle(0, SWIPES)
        pool.pauseAll()

        assertThat(playing()).isEmpty()
    }

    @Test
    fun `release lets every player go and the next acquire starts a new one`() {
        repeat(10) { settle(it, SWIPES) }
        val before = factory.made.toList()

        pool.release()

        assertThat(before.all { it.released }).isTrue()
        // Its HLS watcher stopped listening before the player went.
        assertThat(before.all { it.listeners.isEmpty() && it.analytics.isEmpty() }).isTrue()
        pool.acquire(0, reel(0), "r0")
        assertThat(factory.made).hasSize(before.size + 1)
    }

    private companion object {
        const val SWIPES = 50
    }
}
