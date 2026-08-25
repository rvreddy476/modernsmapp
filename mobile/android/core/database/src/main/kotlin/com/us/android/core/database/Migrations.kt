package com.us.android.core.database

import androidx.room.RoomDatabase
import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase

/**
 * Schema history.
 *
 * `DatabaseModule` deliberately does NOT call `fallbackToDestructiveMigration`.
 * Silently wiping a user's device data on a version bump is convenient during
 * development and catastrophic in production — here it would delete the very
 * thing this table exists to protect, an unpublished draft. So every version
 * bump writes a real migration, and a missing one crashes loudly at open rather
 * than discarding data quietly.
 */

/**
 * 1 → 2: the composer draft (Slice C, C-P0-3).
 *
 * Purely additive: one new table, nothing altered, nothing dropped. An existing
 * install keeps its paging keys untouched and simply gains somewhere to store an
 * unpublished post.
 *
 * Column types mirror `ComposerDraftEntity` exactly. Room verifies this against
 * the exported schema on every open in debug, so a mismatch here is caught at
 * the first launch rather than at the first crash.
 */
val MIGRATION_1_2 = object : Migration(1, 2) {
    override fun migrate(db: SupportSQLiteDatabase) {
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `composer_draft` (
                `id` TEXT NOT NULL,
                `text` TEXT NOT NULL,
                `imageUri` TEXT,
                `altText` TEXT NOT NULL,
                `decorative` INTEGER NOT NULL,
                `language` TEXT NOT NULL,
                `mediaId` TEXT,
                `creationKey` TEXT,
                `frozenRequestJson` TEXT,
                `updatedAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`id`)
            )
            """.trimIndent(),
        )
    }
}

/**
 * Every migration, in order. Fed to the Room builder one at a time.
 *
 * A List rather than an Array so the builder call needs no spread: spreading
 * copies the whole array on every construction, and detekt rejects it.
 */
/**
 * Production chat pass: the durable chat boundary (inbox cache, message
 * cache, send outbox). DDL matches the Room-generated schema for v4 exactly —
 * the fresh-vs-migrated parity test is what holds the two together.
 */
@Suppress("MagicNumber") // Schema version pair, same shape as MIGRATION_1_2 above.
val MIGRATION_3_4 = object : Migration(3, 4) {
    override fun migrate(db: SupportSQLiteDatabase) {
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `chat_conversation` (
                `id` TEXT NOT NULL,
                `type` TEXT NOT NULL,
                `title` TEXT,
                `isRequest` INTEGER NOT NULL,
                `membersJson` TEXT NOT NULL,
                `updatedAt` TEXT NOT NULL,
                `avatarMediaId` TEXT,
                `lastMessageAt` TEXT,
                `lastMessagePreview` TEXT NOT NULL,
                `lastMessageSender` TEXT,
                `hasUnread` INTEGER NOT NULL,
                `syncedAtMillis` INTEGER NOT NULL,
                PRIMARY KEY(`id`)
            )
            """.trimIndent(),
        )
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `chat_message` (
                `id` TEXT NOT NULL,
                `conversationId` TEXT NOT NULL,
                `senderId` TEXT NOT NULL,
                `senderDisplayName` TEXT,
                `text` TEXT NOT NULL,
                `mediaId` TEXT,
                `createdAt` TEXT NOT NULL,
                PRIMARY KEY(`id`)
            )
            """.trimIndent(),
        )
        db.execSQL(
            "CREATE INDEX IF NOT EXISTS `index_chat_message_conversationId_createdAt` " +
                "ON `chat_message` (`conversationId`, `createdAt`)",
        )
        db.execSQL(
            """
            CREATE TABLE IF NOT EXISTS `chat_pending_send` (
                `idempotencyKey` TEXT NOT NULL,
                `conversationId` TEXT NOT NULL,
                `text` TEXT NOT NULL,
                `mediaId` TEXT,
                `createdAtMillis` INTEGER NOT NULL,
                `attempts` INTEGER NOT NULL,
                `failed` INTEGER NOT NULL,
                PRIMARY KEY(`idempotencyKey`)
            )
            """.trimIndent(),
        )
    }
}

/**
 * Android chat completion pass: the viewer's pin/mute switches join the inbox
 * cache so the offline inbox renders them. Plain additive ALTERs with the
 * SAME defaults Room's generated schema declares — the open-through-Room
 * parity test is what holds the two together.
 */
@Suppress("MagicNumber") // Schema version pair, same shape as the migrations above.
val MIGRATION_4_5 = object : Migration(4, 5) {
    override fun migrate(db: SupportSQLiteDatabase) {
        db.execSQL(
            "ALTER TABLE `chat_conversation` ADD COLUMN `isPinned` INTEGER NOT NULL DEFAULT 0",
        )
        db.execSQL(
            "ALTER TABLE `chat_conversation` ADD COLUMN `isMuted` INTEGER NOT NULL DEFAULT 0",
        )
    }
}

val UsDatabaseMigrations: List<Migration> =
    listOf(MIGRATION_1_2, MIGRATION_2_3, MIGRATION_3_4, MIGRATION_4_5)

/**
 * Callbacks the production database builder installs.
 *
 * ## WHY THIS IS NOT IN THE DI MODULE
 *
 * It was, and that was wrong: anything that builds the database another way —
 * including the test that proves production Room can open a migrated file —
 * silently got a DIFFERENT configuration. A seeding rule that only some builders
 * apply is not an invariant, it is a coincidence. Both now use this object.
 */
object UsDatabaseCallbacks {

    /**
     * Writes the one row a fresh schema-3 database would otherwise lack.
     *
     * A migrating install receives `composer_draft_fallback_state` from
     * `MIGRATION_2_3`. A fresh install runs no migration, so without this the
     * singleton would not exist and "is the legacy composer still usable?" would
     * answer null on one cohort and a real value on the other.
     */
    val seedFallbackState = object : RoomDatabase.Callback() {
        override fun onCreate(db: SupportSQLiteDatabase) {
            db.execSQL(
                "INSERT OR REPLACE INTO composer_draft_fallback_state " +
                    "(id, state, reason, updatedAtMillis) VALUES ('singleton', 'AVAILABLE', NULL, ?)",
                arrayOf<Any>(System.currentTimeMillis()),
            )
        }
    }

    val all: List<RoomDatabase.Callback> = listOf(seedFallbackState)
}
