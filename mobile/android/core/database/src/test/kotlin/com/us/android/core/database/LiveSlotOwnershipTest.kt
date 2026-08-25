package com.us.android.core.database

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * CS-A-LB-1 — resolving an operation can only release the slot it owns.
 *
 * ## THE SEQUENCE THE REVIEW EXECUTED, MADE PERMANENT
 *
 * Two projects, each with a live operation. Resolve A's operation while naming
 * B's project. Before the fix this SUCCEEDED: A's operation went terminal, B's
 * slot was deleted, A's stale slot survived — and B could then start a second
 * publish while its first was still live. That second publish is a duplicate
 * post wearing a fresh idempotency key, which the server has no reason to
 * refuse.
 *
 * The stored operation row is now the authority: the caller's `projectId` is a
 * claim to be checked, the slot delete conditions on both identities, and every
 * mutation must affect exactly one row.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class LiveSlotOwnershipTest {

    private lateinit var context: Context
    private lateinit var db: UsDatabase

    private val dao get() = db.creatorPublishOperationDao()

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        context.getDatabasePath(TEST_DB).delete()
        db = Room.databaseBuilder(context, UsDatabase::class.java, TEST_DB)
            .also { builder -> UsDatabaseMigrations.forEach { builder.addMigrations(it) } }
            .also { builder -> UsDatabaseCallbacks.all.forEach { builder.addCallback(it) } }
            .allowMainThreadQueries()
            .build()
    }

    @After
    fun tearDown() {
        db.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    private fun operation(
        id: String,
        projectId: String,
        state: String = "publishing",
    ) = CreatorPublishOperationEntity(
        operationId = id,
        projectId = projectId,
        boundRevision = 1,
        projectDocumentSha256 = "b".repeat(64),
        orderedOutputSha256 = "[]",
        orderedMediaIds = "[]",
        creationKey = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f",
        frozenRequestBase64 = "e30=",
        frozenRequestSha256 = "a".repeat(64),
        frozenRequestBytes = 42,
        state = state,
        createdAtMillis = 1,
        updatedAtMillis = 1,
    )

    private fun startBoth() = runBlocking {
        dao.startOperation(operation(OP_A, PROJECT_A))
        dao.startOperation(operation(OP_B, PROJECT_B))
    }

    // ------------------------------------------------------------------
    // THE permanent review sequence
    // ------------------------------------------------------------------

    @Test
    fun `resolving one project's operation cannot release another project's slot`() {
        startBoth()

        val failure = runCatching {
            runBlocking {
                dao.resolveOperation(
                    operationId = OP_A,
                    projectId = PROJECT_B, // the lie
                    state = "superseded",
                    supersededBy = OP_B,
                    now = 2,
                )
            }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        // Both slots survive, still owned by their own operations.
        assertThat(runBlocking { dao.liveSlot(PROJECT_A) }?.operationId).isEqualTo(OP_A)
        assertThat(runBlocking { dao.liveSlot(PROJECT_B) }?.operationId).isEqualTo(OP_B)
        // And A's operation did not go terminal — the transaction rolled back whole.
        assertThat(runBlocking { dao.load(OP_A) }?.state).isEqualTo("publishing")
    }

    /** After the refused mismatch, neither project can start a second live publish. */
    @Test
    fun `a mismatched resolve does not open a slot for a second publish`() {
        startBoth()
        runCatching {
            runBlocking {
                dao.resolveOperation(OP_A, PROJECT_B, "superseded", supersededBy = OP_B, now = 2)
            }
        }

        val secondPublish = runCatching {
            runBlocking { dao.startOperation(operation("01J9Z4M0P1Q2R3S4T5V6W7X8Z1", PROJECT_B)) }
        }.exceptionOrNull()

        assertThat(secondPublish).isNotNull()
    }

    /** A resolve naming an operation that does not exist rolls back untouched. */
    @Test
    fun `resolving a missing operation is refused`() {
        startBoth()

        val failure = runCatching {
            runBlocking {
                dao.resolveOperation(
                    operationId = "01J9Z4M0P1Q2R3S4T5V6W7X8Z9",
                    projectId = PROJECT_A,
                    state = "published",
                    serverPostId = "post-1",
                    now = 2,
                )
            }
        }.exceptionOrNull()

        assertThat(failure).isNotNull()
        assertThat(runBlocking { dao.liveSlot(PROJECT_A) }).isNotNull()
    }

    // ------------------------------------------------------------------
    // Terminal payload validation (the second boundary defect)
    // ------------------------------------------------------------------

    /** `published` with no server post id is a record that contradicts itself. */
    @Test
    fun `resolving to published without a server post id is refused`() {
        startBoth()

        val failure = runCatching {
            runBlocking { dao.resolveOperation(OP_A, PROJECT_A, "published", serverPostId = null, now = 2) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
        assertThat(runBlocking { dao.load(OP_A) }?.state).isEqualTo("publishing")
        assertThat(runBlocking { dao.liveSlot(PROJECT_A) }).isNotNull()
    }

    /** `superseded` must name its successor. */
    @Test
    fun `resolving to superseded without a successor is refused`() {
        startBoth()

        val failure = runCatching {
            runBlocking { dao.resolveOperation(OP_A, PROJECT_A, "superseded", supersededBy = null, now = 2) }
        }.exceptionOrNull()

        assertThat(failure).isInstanceOf(IllegalArgumentException::class.java)
        assertThat(runBlocking { dao.load(OP_A) }?.state).isEqualTo("publishing")
    }

    // ------------------------------------------------------------------
    // The honest path still works
    // ------------------------------------------------------------------

    @Test
    fun `a correct resolve frees exactly its own slot`() {
        startBoth()

        runBlocking {
            dao.resolveOperation(OP_A, PROJECT_A, "published", serverPostId = "post-1", now = 2)
        }

        assertThat(runBlocking { dao.liveSlot(PROJECT_A) }).isNull()
        assertThat(runBlocking { dao.liveSlot(PROJECT_B) }?.operationId).isEqualTo(OP_B)
        assertThat(runBlocking { dao.load(OP_A) }?.serverPostId).isEqualTo("post-1")
    }

    private companion object {
        const val TEST_DB = "live-slot-ownership.db"
        const val PROJECT_A = "01J9Z4K7QW8XN2VB3M5R7T9Y0A"
        const val PROJECT_B = "01J9Z4K7QW8XN2VB3M5R7T9Y0B"
        const val OP_A = "01J9Z4M0P1Q2R3S4T5V6W7X8YA"
        const val OP_B = "01J9Z4M0P1Q2R3S4T5V6W7X8YB"
    }
}
