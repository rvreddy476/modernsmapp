package com.us.android.core.database

import androidx.room.Dao
import androidx.room.Entity
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Transaction

/**
 * Creator Studio P0-A persistence.
 *
 * ## THE SHAPE OF THE PROBLEM THIS SOLVES
 *
 * Slice C stored one composer draft in one row. A draft could carry a
 * `creationKey` + `frozenRequestJson` pair — a publish that was already sent to
 * the server but whose response was lost. Creator Studio adds multi-page
 * projects, so those legacy rows have to be adopted, and adopting them wrongly
 * is how a user gets two identical posts.
 *
 * The adoption path is deliberately three-staged and idempotent. Nothing here
 * decides anything by itself; the migration classifies, the engine adopts, and
 * both can be killed at any point without losing a row or minting a key.
 */

/** The v1 project document, stored as canonical JSON plus its fingerprint. */
@Entity(tableName = "creator_project")
data class CreatorProjectEntity(
    @PrimaryKey val projectId: String,
    val schemaVersion: Int,
    val profile: String,
    val revision: Int,
    val status: String,
    /** Canonical bytes from `Canonical.encode`, verbatim. */
    val document: String,
    /** SHA-256 of [document]. Recomputed and compared on load. */
    val documentSha256: String,
    val createdAtMillis: Long,
    val updatedAtMillis: Long,
)

/**
 * A publish operation. Its binding half never changes.
 *
 * `frozenRequestBase64` holds the EXACT request bytes the server minted the
 * idempotency key for. They are stored base64 and are never decoded-and-
 * re-encoded on the way through: one different byte turns a legitimate retry
 * into a 409, or into a second post.
 */
@Entity(tableName = "creator_publish_operation")
data class CreatorPublishOperationEntity(
    @PrimaryKey val operationId: String,
    val projectId: String,
    val boundRevision: Int,
    val projectDocumentSha256: String,
    /** JSON array of hashes, in page order. Empty for a text-only post. */
    val orderedOutputSha256: String,
    /** JSON array of media ids, in page order. Empty for a text-only post. */
    val orderedMediaIds: String,
    val creationKey: String,
    val frozenRequestBase64: String,
    val frozenRequestSha256: String,
    val frozenRequestBytes: Int,
    val state: String,
    val serverPostId: String? = null,
    val lastErrorCode: String? = null,
    val supersededByOperationId: String? = null,
    val createdAtMillis: Long,
    val updatedAtMillis: Long,
)

/**
 * The one-live-publication slot. At most one row per project, by construction.
 *
 * ## WHY THIS IS A TABLE AND NOT A PARTIAL INDEX
 *
 * The first implementation used
 * `CREATE UNIQUE INDEX … WHERE state IN ('publishing','failed')`. Two things
 * were wrong with it, and both were launch blockers:
 *
 *  1. **Room cannot generate a partial index.** So the migrated database had an
 *     index the freshly-created database did not, `TableInfo.equalsCommon`
 *     compares the two index sets, and a real Room open AFTER migrating would
 *     have been rejected — the app would not start for any upgrading user. The
 *     old test never noticed because it called `migrate()` directly and never
 *     opened the result through `Room.databaseBuilder`.
 *  2. **A fresh install had no protection at all**, because Room created the
 *     table without the index. Two live operations for one project were
 *     permitted, which is the shape of a duplicate post.
 *
 * A table with `projectId` as the PRIMARY KEY says the same thing in something
 * Room can generate identically for both cohorts. It is also strictly stronger:
 * the partial index only constrained states inside its predicate, so an
 * unrecognised `state` slipped past it entirely. Here the slot is held or it is
 * not, whatever the operation calls itself.
 */
@Entity(tableName = "creator_live_operation")
data class CreatorLiveOperationEntity(
    @PrimaryKey val projectId: String,
    val operationId: String,
)

/** A vault-backed source original. */
@Entity(tableName = "creator_source_asset", primaryKeys = ["projectId", "assetId"])
data class CreatorSourceAssetEntity(
    val projectId: String,
    val assetId: String,
    val vaultPath: String,
    val sha256: String,
    val bytes: Long,
    val mime: String,
    val widthPx: Int,
    val heightPx: Int,
)

/**
 * A legacy draft copied out of schema v2, classified but NOT yet adopted.
 *
 * ## WHY THE MIGRATION CLASSIFIES INSTEAD OF THE APP
 *
 * An earlier design read the v2 row with a separate read-only SQLite handle,
 * wrote a flag to preferences, and let a later Room transaction trust it. That
 * is a time-of-check/time-of-use gap: two different reads of the same row with
 * a write in between. [classification] is now computed by the migration's own
 * SQL, in the same transaction that copies the row, so there is nothing to
 * disagree with.
 */
@Entity(tableName = "creator_migration_staging")
data class CreatorMigrationStagingEntity(
    @PrimaryKey val stagingId: String,
    val text: String,
    val imageUri: String?,
    val altText: String,
    val decorative: Boolean,
    val language: String,
    val mediaId: String?,
    val creationKey: String?,
    val frozenRequestJson: String?,
    /** `CLEAN` or `HALF_FROZEN_OPERATION`. */
    val classification: String,
    /** `PENDING`, `IMPORTING`, `ADOPTED` or `QUARANTINED`. */
    val adoptionState: String,
    val attempts: Int,
    val updatedAtMillis: Long,
) {
    companion object {
        const val SINGLETON_ID = "composer"

        const val CLASSIFICATION_CLEAN = "CLEAN"
        const val CLASSIFICATION_HALF_FROZEN = "HALF_FROZEN_OPERATION"

        const val STATE_PENDING = "PENDING"
        const val STATE_IMPORTING = "IMPORTING"
        const val STATE_ADOPTED = "ADOPTED"
        const val STATE_QUARANTINED = "QUARANTINED"
    }
}

/**
 * Whether the legacy composer may still be used as a rollback surface.
 *
 * ## WHY THIS IS ITS OWN TABLE
 *
 * The obvious design is a column on `composer_draft`. It cannot be: the case
 * this state exists to describe is "the legacy row was cleared", and a column of
 * a deleted row deletes with it. A separate singleton survives the clear and can
 * say why it happened.
 */
@Entity(tableName = "composer_draft_fallback_state")
data class ComposerDraftFallbackStateEntity(
    @PrimaryKey val id: String = SINGLETON_ID,
    val state: String,
    val reason: String? = null,
    val updatedAtMillis: Long,
) {
    companion object {
        const val SINGLETON_ID = "singleton"

        const val AVAILABLE = "AVAILABLE"
        const val UNAVAILABLE = "UNAVAILABLE"

        const val REASON_UNREPRESENTABLE = "PROJECT_UNREPRESENTABLE"
        const val REASON_QUARANTINED = "LEGACY_QUARANTINED"
        const val REASON_RECOVERY_REQUIRED = "LEGACY_RECOVERY_REQUIRED"
    }
}

/**
 * A legacy row that must NOT become an editable project.
 *
 * ## WHY THIS TYPE EXISTS AT ALL
 *
 * A draft can hold a confirmed server `mediaId` whose local source is gone. The
 * tempting move is to build a project for it anyway. That is impossible without
 * lying: the schema requires a `renderedOutput` with a vault path, byte count,
 * hash and dimensions, and none of those exist for an asset that only lives on
 * the server. Manufacturing them would put invented facts into the document
 * every later decision is based on.
 *
 * ## AND WHY OPERATION AUTHORITY OUTRANKS SOURCE AVAILABILITY
 *
 * A row can have BOTH a readable `imageUri` AND a frozen operation, when a
 * publish committed on the server but the response was lost. Adopting it as an
 * editable project drops the operation, and the next publish mints a fresh key
 * and duplicates a post that already exists. So a frozen operation always wins:
 * the exact bytes are retried first, and editing waits.
 */
@Entity(tableName = "creator_legacy_recovery")
data class CreatorLegacyRecoveryEntity(
    @PrimaryKey val recoveryId: String,
    /** `RETRYABLE_PUBLISH`, `TEXT_ONLY` or `UNUSABLE`. */
    val kind: String,
    val text: String,
    val language: String,
    val mediaId: String?,
    val creationKey: String?,
    val frozenRequestJson: String?,
    val frozenRequestSha: String?,
    val frozenRequestLen: Int?,
    /** Optional recovery material. Never a substitute for the frozen operation. */
    val recoveredSourcePath: String? = null,
    val createdAtMillis: Long,
) {
    companion object {
        const val KIND_RETRYABLE_PUBLISH = "RETRYABLE_PUBLISH"
        const val KIND_TEXT_ONLY = "TEXT_ONLY"
        const val KIND_UNUSABLE = "UNUSABLE"
    }
}

@Dao
interface CreatorProjectDao {

    @Query("SELECT * FROM creator_project WHERE projectId = :projectId")
    suspend fun load(projectId: String): CreatorProjectEntity?

    @Query("SELECT * FROM creator_project ORDER BY updatedAtMillis DESC")
    suspend fun all(): List<CreatorProjectEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsert(project: CreatorProjectEntity)

    @Query("DELETE FROM creator_project WHERE projectId = :projectId")
    suspend fun delete(projectId: String)
}

// TooManyFunctions: the guarded @Transaction methods must live beside the raw
// primitives they wrap — Room resolves them against `this`, and splitting the
// interface would put the guard and the thing it guards in different files.
@Suppress("TooManyFunctions")
@Dao
interface CreatorPublishOperationDao {

    @Query("SELECT * FROM creator_publish_operation WHERE operationId = :operationId")
    suspend fun load(operationId: String): CreatorPublishOperationEntity?

    /**
     * The one live operation for a project, if any.
     *
     * `published` and `superseded` are terminal and hold no slot. A `failed`
     * operation IS still live — it may only retry byte-identical bytes, and it
     * must be explicitly superseded before an edited revision gets a new one.
     */
    @Query(
        """
        SELECT * FROM creator_publish_operation
        WHERE projectId = :projectId AND state IN ('publishing', 'failed')
        """,
    )
    suspend fun liveFor(projectId: String): CreatorPublishOperationEntity?

    /**
     * RAW PRIMITIVES — reachable only from the guarded transactions below.
     *
     * Room requires DAO methods to be public, so the compiler cannot hide these.
     * The boundary is enforced two other ways instead: the `raw` prefix makes
     * every call site conspicuous, and `RawDaoBoundaryGuardTest` scans all
     * production sources and fails the build if any file other than this one
     * mentions a raw mutation. Calling one directly would bypass
     * [CreatorInvariants] — which is exactly how an invalid state or an orphaned
     * slot gets persisted.
     */
    @Insert(onConflict = OnConflictStrategy.ABORT)
    suspend fun rawInsertOperation(operation: CreatorPublishOperationEntity)

    @Insert(onConflict = OnConflictStrategy.ABORT)
    suspend fun rawClaimLiveSlot(slot: CreatorLiveOperationEntity)

    @Query("SELECT * FROM creator_live_operation WHERE projectId = :projectId")
    suspend fun liveSlot(projectId: String): CreatorLiveOperationEntity?

    /**
     * Releases a slot only when BOTH identities match.
     *
     * Conditioning on `projectId` alone was the CS-A-LB-1 defect: resolving
     * operation A while naming project B deleted B's slot and left A's stale,
     * so B could start a second publish while its first was still live — the
     * duplicate-post shape this table exists to prevent.
     */
    @Query(
        """
        DELETE FROM creator_live_operation
        WHERE projectId = :projectId AND operationId = :operationId
        """,
    )
    suspend fun rawReleaseLiveSlot(projectId: String, operationId: String): Int

    /**
     * Lifecycle-only update. The binding half is not a parameter here, which is
     * the cheapest possible way to make "immutable" true rather than aspirational.
     */
    @Query(
        """
        UPDATE creator_publish_operation
        SET state = :state, serverPostId = :serverPostId, lastErrorCode = :lastErrorCode,
            supersededByOperationId = :supersededBy, updatedAtMillis = :updatedAtMillis
        WHERE operationId = :operationId
        """,
    )
    suspend fun rawUpdateLifecycle(
        operationId: String,
        state: String,
        serverPostId: String?,
        lastErrorCode: String?,
        supersededBy: String?,
        updatedAtMillis: Long,
    ): Int

    @Query("DELETE FROM creator_publish_operation WHERE projectId = :projectId")
    suspend fun rawDeleteOperationsForProject(projectId: String): Int

    /**
     * Start a publish, taking the project's one live slot.
     *
     * The slot insert is what makes "one publication in flight" true. Its
     * primary key is `projectId`, so a second live operation for the same
     * project fails on the constraint and the whole transaction rolls back —
     * which is exactly the moment a duplicate post would otherwise be created.
     *
     * Identical on a freshly created database and a migrated one, because Room
     * generates this table from the entity in both.
     */
    @Transaction
    suspend fun startOperation(operation: CreatorPublishOperationEntity) {
        CreatorInvariants.requireValidOperation(operation)
        require(operation.state in CreatorInvariants.LIVE_OPERATION_STATES) {
            "startOperation may only begin a live state, got '${operation.state}'"
        }
        rawInsertOperation(operation)
        rawClaimLiveSlot(CreatorLiveOperationEntity(operation.projectId, operation.operationId))
    }

    /**
     * Move an operation to a terminal state and free the project's slot.
     *
     * ## THE STORED ROW IS THE AUTHORITY, NOT THE CALLER
     *
     * The caller still passes `projectId`, but only as a claim to be checked.
     * The operation is loaded first, and every later step is bound to what the
     * database says: a `projectId` that disagrees with the stored row fails the
     * whole transaction, and the slot delete conditions on BOTH identities so a
     * mismatch that somehow got this far would delete zero rows and roll back.
     *
     * The transition is also validated as a complete row before persistence —
     * `published` without a server post id, or `superseded` without a successor,
     * is a record that contradicts itself and must never exist on disk.
     */
    @Transaction
    suspend fun resolveOperation(
        operationId: String,
        projectId: String,
        state: String,
        serverPostId: String? = null,
        supersededBy: String? = null,
        now: Long,
    ) {
        require(state in CreatorInvariants.TERMINAL_OPERATION_STATES) {
            "resolveOperation only moves to a terminal state, got '$state'"
        }

        val stored = load(operationId)
            ?: throw UnexpectedRowCountException("resolveOperation lookup", 1, 0)
        require(stored.projectId == projectId) {
            "operation $operationId belongs to project ${stored.projectId}, " +
                "not $projectId; refusing to release a slot this resolution does not own"
        }

        // Validate the COMPLETE post-transition row, not just the state word.
        CreatorInvariants.requireValidOperation(
            stored.copy(
                state = state,
                serverPostId = serverPostId,
                supersededByOperationId = supersededBy,
                updatedAtMillis = now,
            ),
        )

        val updated = rawUpdateLifecycle(
            operationId = operationId,
            state = state,
            serverPostId = serverPostId,
            lastErrorCode = null,
            supersededBy = supersededBy,
            updatedAtMillis = now,
        )
        requireAffected("resolveOperation update", updated)

        val released = rawReleaseLiveSlot(stored.projectId, operationId)
        requireAffected("resolveOperation slot release", released)
    }

    /**
     * Record a failed attempt WITHOUT freeing the slot.
     *
     * `failed` is a live state on purpose: the operation may only ever retry
     * its exact frozen bytes, and the held slot is what stops a second publish
     * starting for the same project while this one is unresolved.
     */
    @Transaction
    suspend fun failOperation(operationId: String, errorCode: String?, now: Long) {
        val stored = load(operationId)
            ?: throw UnexpectedRowCountException("failOperation lookup", 1, 0)
        require(stored.state in CreatorInvariants.LIVE_OPERATION_STATES) {
            "only a live operation can fail; ${stored.operationId} is '${stored.state}'"
        }
        val updated = rawUpdateLifecycle(
            operationId = operationId,
            state = "failed",
            serverPostId = null,
            lastErrorCode = errorCode,
            supersededBy = null,
            updatedAtMillis = now,
        )
        requireAffected("failOperation", updated)
    }
}

// TooManyFunctions: the two @Transaction methods below must live in the same DAO
// as the queries they call — Room resolves them against `this`. Splitting the
// interface to satisfy a count would split an atomic write in half, which is
// precisely the failure these transactions exist to prevent.
@Suppress("TooManyFunctions")
@Dao
interface CreatorMigrationDao {

    @Query("SELECT * FROM creator_migration_staging")
    suspend fun staged(): List<CreatorMigrationStagingEntity>

    @Query("SELECT * FROM creator_migration_staging WHERE stagingId = :id")
    suspend fun staging(id: String): CreatorMigrationStagingEntity?

    @Query(
        """
        UPDATE creator_migration_staging
        SET adoptionState = :state, attempts = attempts + 1, updatedAtMillis = :now
        WHERE stagingId = :id
        """,
    )
    suspend fun markState(id: String, state: String, now: Long): Int

    @Query("DELETE FROM creator_migration_staging WHERE stagingId = :id")
    suspend fun deleteStaging(id: String): Int

    @Query("SELECT * FROM creator_legacy_recovery")
    suspend fun recoveries(): List<CreatorLegacyRecoveryEntity>

    @Query("SELECT * FROM creator_legacy_recovery WHERE recoveryId = :id")
    suspend fun recovery(id: String): CreatorLegacyRecoveryEntity?

    @Insert(onConflict = OnConflictStrategy.ABORT)
    suspend fun insertRecovery(recovery: CreatorLegacyRecoveryEntity)

    @Query("DELETE FROM creator_legacy_recovery WHERE recoveryId = :id")
    suspend fun deleteRecovery(id: String): Int

    @Query("SELECT * FROM composer_draft_fallback_state WHERE id = 'singleton'")
    suspend fun fallbackState(): ComposerDraftFallbackStateEntity?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun setFallbackState(state: ComposerDraftFallbackStateEntity)

    @Query("DELETE FROM composer_draft")
    suspend fun clearLegacyDraft()

    /**
     * Route a staging row into recovery, atomically.
     *
     * The insert, the staging transition and the fallback-state write are one
     * transaction on purpose. If they were three, a kill between them could
     * leave a recovery row that the legacy composer still thinks it can
     * override, and the user would publish stale content over a post that had
     * already committed.
     */
    @Transaction
    suspend fun routeToRecovery(
        recovery: CreatorLegacyRecoveryEntity,
        stagingId: String,
        now: Long,
    ) {
        CreatorInvariants.requireValidRecovery(recovery)
        insertRecovery(recovery)
        // The staging transition must actually happen. Without this check the
        // method could insert a recovery row, update NOTHING because the caller
        // named a staging row that is not there, and still go on to disable the
        // legacy fallback — turning a stale callback into a user-visible loss of
        // their draft.
        requireAffected(
            "routeToRecovery staging",
            markState(stagingId, CreatorMigrationStagingEntity.STATE_ADOPTED, now),
        )

        val fallback = ComposerDraftFallbackStateEntity(
            state = ComposerDraftFallbackStateEntity.UNAVAILABLE,
            reason = ComposerDraftFallbackStateEntity.REASON_RECOVERY_REQUIRED,
            updatedAtMillis = now,
        )
        CreatorInvariants.requireValidFallbackState(fallback)
        setFallbackState(fallback)
    }

    /**
     * Everything that happens after a lost-response retry finally resolves.
     *
     * One transaction, in this order, so a kill anywhere leaves either the
     * pre-retry state or the fully-cleaned one — never a half-cleaned state that
     * would offer the user a stale draft of a post that is now live.
     */
    @Transaction
    suspend fun completeRecoveredPublish(
        recoveryId: String,
        stagingId: String,
        /**
         * The creation key the caller believes it just resolved.
         *
         * Identity, not just an id. A stale callback carrying a recycled
         * `recoveryId` would otherwise clean up a DIFFERENT recovery — and then
         * clear the legacy draft belonging to work that never published.
         */
        expectedCreationKey: String,
        now: Long,
    ) {
        val existing = recovery(recoveryId)
            ?: throw UnexpectedRowCountException("completeRecoveredPublish recovery lookup", 1, 0)
        require(existing.creationKey == expectedCreationKey) {
            "recovery $recoveryId holds a different creation key than the caller resolved; " +
                "refusing to clean up work this response does not belong to"
        }

        requireAffected("completeRecoveredPublish recovery", deleteRecovery(recoveryId))
        requireAffected("completeRecoveredPublish staging", deleteStaging(stagingId))

        // Only now — after both named rows are proven to have existed and been
        // removed — is it safe to clear the user's retained legacy draft. Every
        // check above exists so that a mismatched or replayed callback rolls the
        // whole transaction back with that draft still on disk.
        clearLegacyDraft()

        val fallback = ComposerDraftFallbackStateEntity(
            state = ComposerDraftFallbackStateEntity.AVAILABLE,
            reason = null,
            updatedAtMillis = now,
        )
        CreatorInvariants.requireValidFallbackState(fallback)
        setFallbackState(fallback)
    }
}
