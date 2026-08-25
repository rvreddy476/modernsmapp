package com.us.android.core.database

import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Index
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.RoomDatabase
import androidx.room.Upsert

/**
 * Paging 3's bookmark table.
 *
 * `RemoteMediator` needs to know, per feed, which cursor comes next and when
 * the cache was last filled. Storing it in the DB rather than in memory is
 * what lets the feed survive process death and open instantly from cache —
 * the offline-first behaviour Phase 3 depends on.
 *
 * The platform paginates by opaque cursor (`meta.next_cursor`), never by page
 * number, so [nextCursor] is a String and is never parsed or constructed.
 */
@Entity(
    tableName = "remote_keys",
    indices = [Index(value = ["feedId"])],
)
data class RemoteKeyEntity(
    @PrimaryKey val id: String,
    val feedId: String,
    val nextCursor: String?,
    val lastUpdatedMillis: Long,
)

@Dao
interface RemoteKeyDao {

    @Upsert
    suspend fun upsert(key: RemoteKeyEntity)

    @Query("SELECT * FROM remote_keys WHERE id = :id")
    suspend fun byId(id: String): RemoteKeyEntity?

    @Query("SELECT * FROM remote_keys WHERE feedId = :feedId ORDER BY lastUpdatedMillis DESC LIMIT 1")
    suspend fun latestForFeed(feedId: String): RemoteKeyEntity?

    @Query("DELETE FROM remote_keys WHERE feedId = :feedId")
    suspend fun clearFeed(feedId: String)

    @Query("DELETE FROM remote_keys")
    suspend fun clearAll()
}

/**
 * Phase 1 ships the database skeleton only — one table, so that the schema is
 * exported, migrations are testable, and the DI wiring is proven before any
 * feature depends on it. Feed, chat and upload-queue tables land with their
 * own phases.
 *
 * `exportSchema = true` is not optional: without the checked-in schema JSON
 * there is nothing for a migration test to migrate *from*.
 */
@Database(
    entities = [
        RemoteKeyEntity::class,
        ComposerDraftEntity::class,
        // Creator Studio P0-A.
        CreatorProjectEntity::class,
        CreatorPublishOperationEntity::class,
        CreatorLiveOperationEntity::class,
        CreatorSourceAssetEntity::class,
        CreatorMigrationStagingEntity::class,
        ComposerDraftFallbackStateEntity::class,
        CreatorLegacyRecoveryEntity::class,
        // Production chat pass — the durable chat boundary.
        ChatConversationEntity::class,
        ChatMessageEntity::class,
        ChatPendingSendEntity::class,
    ],
    version = 5,
    exportSchema = true,
)
abstract class UsDatabase : RoomDatabase() {
    abstract fun remoteKeyDao(): RemoteKeyDao

    abstract fun composerDraftDao(): ComposerDraftDao

    abstract fun chatDao(): ChatDao

    abstract fun creatorProjectDao(): CreatorProjectDao

    abstract fun creatorPublishOperationDao(): CreatorPublishOperationDao

    abstract fun creatorMigrationDao(): CreatorMigrationDao

    companion object {
        const val NAME = "us.db"
    }
}
