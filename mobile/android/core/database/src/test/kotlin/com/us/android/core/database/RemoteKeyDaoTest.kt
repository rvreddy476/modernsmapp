package com.us.android.core.database

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Proves the Room skeleton actually opens, that the schema is coherent, and
 * that cursor bookkeeping round-trips.
 *
 * Cheap now, load-bearing in Phase 3: `RemoteMediator` stores the platform's
 * opaque `meta.next_cursor` here, and a broken DAO surfaces as a feed that
 * silently stops paginating.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class RemoteKeyDaoTest {

    private lateinit var db: UsDatabase
    private lateinit var dao: RemoteKeyDao

    @Before
    fun setUp() {
        db = Room.inMemoryDatabaseBuilder(
            ApplicationProvider.getApplicationContext(),
            UsDatabase::class.java,
        ).allowMainThreadQueries().build()
        dao = db.remoteKeyDao()
    }

    @After
    fun tearDown() = db.close()

    @Test
    fun `database opens lazily, on first query rather than on build`() = runTest {
        // Room defers opening the underlying SQLite connection until it is
        // actually used, so `isOpen` is false straight after builder(). Worth
        // pinning: it is why DB construction is cheap enough to do in a DI
        // provider without hurting cold start.
        assertThat(db.isOpen).isFalse()

        dao.clearAll()

        assertThat(db.isOpen).isTrue()
    }

    @Test
    fun `upsert then read round-trips a cursor`() = runTest {
        dao.upsert(RemoteKeyEntity("k1", "feed:home", "cursor-abc", 1_000L))

        val stored = dao.byId("k1")

        assertThat(stored?.feedId).isEqualTo("feed:home")
        assertThat(stored?.nextCursor).isEqualTo("cursor-abc")
    }

    @Test
    fun `upsert replaces rather than duplicating`() = runTest {
        dao.upsert(RemoteKeyEntity("k1", "feed:home", "cursor-1", 1_000L))
        dao.upsert(RemoteKeyEntity("k1", "feed:home", "cursor-2", 2_000L))

        assertThat(dao.byId("k1")?.nextCursor).isEqualTo("cursor-2")
    }

    @Test
    fun `latestForFeed returns the most recently updated row`() = runTest {
        dao.upsert(RemoteKeyEntity("k1", "feed:home", "old", 1_000L))
        dao.upsert(RemoteKeyEntity("k2", "feed:home", "new", 5_000L))
        dao.upsert(RemoteKeyEntity("k3", "feed:reels", "other", 9_000L))

        assertThat(dao.latestForFeed("feed:home")?.nextCursor).isEqualTo("new")
    }

    @Test
    fun `a null cursor is legal and means the last page`() = runTest {
        dao.upsert(RemoteKeyEntity("k1", "feed:home", null, 1_000L))

        assertThat(dao.byId("k1")?.nextCursor).isNull()
    }

    @Test
    fun `clearFeed only clears the named feed`() = runTest {
        dao.upsert(RemoteKeyEntity("k1", "feed:home", "a", 1L))
        dao.upsert(RemoteKeyEntity("k2", "feed:reels", "b", 1L))

        dao.clearFeed("feed:home")

        assertThat(dao.byId("k1")).isNull()
        assertThat(dao.byId("k2")).isNotNull()
    }
}
