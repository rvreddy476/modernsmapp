package com.us.android.core.database

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
val UsDatabaseMigrations: List<Migration> = listOf(MIGRATION_1_2)
