package com.us.android.core.database

import android.content.Context
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.sqlite.db.SupportSQLiteDatabase
import androidx.sqlite.db.SupportSQLiteOpenHelper
import androidx.sqlite.db.framework.FrameworkSQLiteOpenHelperFactory
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

/**
 * Sign-out disk hygiene: after `wipeAll()` + [scrubDeletedRowsFromDisk] no
 * message plaintext may remain in ANY of the database's on-disk files.
 *
 * The failure this pins was captured live on a device: after sign-out the
 * main `us.db` file was a bare 4 KB header, but `us.db-wal` still carried
 * every wiped message body — the WAL retains superseded page images until a
 * checkpoint, and the main file's free list retains deleted pages' bytes
 * until a VACUUM. A file-backed database in WAL mode (what the installed app
 * runs) is therefore mandatory here; an in-memory database would prove
 * nothing.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatWipeDiskHygieneTest {

    private lateinit var context: Context
    private lateinit var database: UsDatabase

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        deleteDatabaseFiles()
        database = Room.databaseBuilder(context, UsDatabase::class.java, TEST_DB)
            .also { builder -> UsDatabaseMigrations.forEach { builder.addMigrations(it) } }
            .setJournalMode(RoomDatabase.JournalMode.WRITE_AHEAD_LOGGING)
            .allowMainThreadQueries()
            .build()
    }

    @After
    fun tearDown() {
        database.close()
        deleteDatabaseFiles()
    }

    private fun deleteDatabaseFiles() {
        val db = context.getDatabasePath(TEST_DB)
        listOf(db, File(db.path + "-wal"), File(db.path + "-shm")).forEach { it.delete() }
    }

    private fun diskFiles(): List<File> {
        val db = context.getDatabasePath(TEST_DB)
        return listOf(db, File(db.path + "-wal")).filter { it.exists() }
    }

    private fun diskContains(needle: String): Boolean = diskFiles().any { file ->
        String(file.readBytes(), Charsets.ISO_8859_1).contains(needle)
    }

    @Test
    fun `wiped message plaintext does not survive on disk`() = runBlocking {
        val dao = database.chatDao()
        dao.upsertConversations(
            listOf(
                ChatConversationEntity(
                    id = "c1",
                    type = "direct",
                    title = null,
                    isRequest = false,
                    membersJson = "[]",
                    updatedAt = "2026-08-26T10:00:00Z",
                    avatarMediaId = null,
                    lastMessageAt = null,
                    lastMessagePreview = SENTINEL,
                    lastMessageSender = null,
                    hasUnread = false,
                    syncedAtMillis = 1L,
                    isPinned = false,
                    isMuted = false,
                ),
            ),
        )
        dao.insertMessages(
            listOf(
                ChatMessageEntity(
                    id = "m1",
                    conversationId = "c1",
                    senderId = "u1",
                    senderDisplayName = null,
                    text = SENTINEL,
                    mediaId = null,
                    createdAt = "2026-08-26T10:00:00Z",
                ),
            ),
        )
        dao.enqueueSend(
            ChatPendingSendEntity(
                idempotencyKey = "k1",
                conversationId = "c1",
                text = SENTINEL,
                mediaId = null,
                createdAtMillis = 1L,
                attempts = 0,
                failed = false,
            ),
        )

        // Control: the sentinel is detectable on disk before the scrub —
        // otherwise the final assertion could pass vacuously.
        assertThat(diskContains(SENTINEL)).isTrue()

        dao.wipeAll()
        assertThat(dao.scrubDeletedRowsFromDisk()).isTrue()

        assertThat(diskContains(SENTINEL)).isFalse()
    }

    /** The scrub is safe to run on an already-empty database (idempotence). */
    @Test
    fun `scrub on an empty database is a no-op`() = runBlocking {
        val dao = database.chatDao()
        dao.wipeAll()
        assertThat(dao.scrubDeletedRowsFromDisk()).isTrue()
        assertThat(dao.scrubDeletedRowsFromDisk()).isTrue()
        assertThat(dao.pendingSends()).isEmpty()
    }

    /**
     * F2-LB-2: checkpoint contention must surface as a FAILED scrub. A second
     * connection holds a write transaction on the same file, so VACUUM cannot
     * acquire the database and `wal_checkpoint(TRUNCATE)` cannot complete —
     * conditions the pre-fix implementation reported as silent success while
     * the WAL still carried the deleted plaintext.
     */
    @Test
    fun `a held connection makes the scrub report failure, then succeed once released`() {
        val dao = database.chatDao()
        runBlocking {
            dao.insertMessages(
                listOf(
                    ChatMessageEntity(
                        id = "m-held",
                        conversationId = "c1",
                        senderId = "u1",
                        senderDisplayName = null,
                        text = SENTINEL,
                        mediaId = null,
                        createdAt = "2026-08-26T10:00:00Z",
                    ),
                ),
            )
        }

        // The rows are wiped BEFORE contention begins — the scrub is what
        // must detect the contended database, not the wipe.
        runBlocking { dao.wipeAll() }

        // The contender: a real second connection to the SAME file, holding
        // an open transaction across the scrub. SQLite refuses VACUUM and
        // cannot complete a TRUNCATE checkpoint while it is held.
        val contender = FrameworkSQLiteOpenHelperFactory().create(
            SupportSQLiteOpenHelper.Configuration.builder(context)
                .name(TEST_DB)
                .callback(object : SupportSQLiteOpenHelper.Callback(SCHEMA_CURRENT) {
                    override fun onCreate(db: SupportSQLiteDatabase) = Unit
                    override fun onUpgrade(db: SupportSQLiteDatabase, old: Int, new: Int) = Unit
                })
                .build(),
        )
        val held = contender.writableDatabase
        held.beginTransactionNonExclusive()
        try {
            runBlocking {
                // The old behavior surfaced nothing here — the contended
                // cleanup either threw (swallowed upstream) or reported a
                // busy checkpoint as a normal return. Now it is REPORTED.
                assertThat(dao.scrubDeletedRowsFromDisk()).isFalse()
            }
            // And the report is truthful: the plaintext is still on disk.
            assertThat(diskContains(SENTINEL)).isTrue()
        } finally {
            held.endTransaction()
            contender.close()
        }

        runBlocking { assertThat(dao.scrubDeletedRowsFromDisk()).isTrue() }
        assertThat(diskContains(SENTINEL)).isFalse()
    }

    private companion object {
        const val TEST_DB = "chat-wipe-hygiene.db"
        const val SENTINEL = "SCRUB_SENTINEL_do_not_survive_logout_7f3a"
        const val SCHEMA_CURRENT = 6
    }
}
