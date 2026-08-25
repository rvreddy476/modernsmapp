package com.us.android.core.creator.engine

import com.us.android.core.creator.model.CreateOutcome
import com.us.android.core.creator.model.PublishTransport
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import com.us.android.core.database.CreatorMigrationStagingEntity
import com.us.android.core.database.UsDatabase
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Resolves a `RETRYABLE_PUBLISH` recovery — the visible half of R-3.
 *
 * ## WHAT THIS DOES AND REFUSES TO DO
 *
 * A retryable recovery is a publish whose response was lost. The ONE action is
 * to replay the exact frozen bytes under the existing creation key and let the
 * server's idempotency authority answer. Before any network call the stored
 * SHA/length and the media-id/post_type coherence are re-verified
 * ([LegacyAdoption.verifyFrozenRequest]); a mismatch quarantines with no
 * request sent. Nothing here ever mints a key or rebuilds a request.
 *
 * On success, one transaction records the resolution and restores the legacy
 * fallback ([com.us.android.core.database.CreatorMigrationDao.completeRecoveredPublish]);
 * a redelivered response is refused there by identity, so double-tapping Retry
 * cannot double-clean.
 */
@Singleton
class LegacyRecoveryRetrier @Inject constructor(
    private val db: UsDatabase,
    private val transport: PublishTransport,
) {

    sealed interface RetryResult {
        data class Published(val postId: String) : RetryResult
        data class Retryable(val reason: String) : RetryResult
        data class Quarantined(val reason: String) : RetryResult
    }

    suspend fun pendingRecoveries(): List<CreatorLegacyRecoveryEntity> =
        db.creatorMigrationDao().recoveries()

    suspend fun retry(recoveryId: String): RetryResult {
        val recovery = db.creatorMigrationDao().recovery(recoveryId)
            ?: return RetryResult.Quarantined("recovery $recoveryId no longer exists")

        when (val verdict = LegacyAdoption.verifyFrozenRequest(recovery)) {
            is FrozenRequestVerdict.Quarantine -> return RetryResult.Quarantined(verdict.reason)
            FrozenRequestVerdict.Retry -> Unit
        }

        val bytes = recovery.frozenRequestJson!!.toByteArray(Charsets.UTF_8)
        return when (val outcome = transport.createPost(recovery.creationKey!!, bytes)) {
            is CreateOutcome.Created -> complete(recovery, outcome.postId)
            is CreateOutcome.AlreadyCreated -> complete(recovery, outcome.postId)
            is CreateOutcome.Retryable -> RetryResult.Retryable(outcome.reason)
            // A permanent create failure for frozen bytes is a state only the
            // user can resolve (discard); it stays a recovery row, not a crash.
            is CreateOutcome.Permanent -> RetryResult.Quarantined(outcome.reason)
        }
    }

    private suspend fun complete(
        recovery: CreatorLegacyRecoveryEntity,
        postId: String,
    ): RetryResult {
        db.creatorMigrationDao().completeRecoveredPublish(
            recoveryId = recovery.recoveryId,
            stagingId = CreatorMigrationStagingEntity.SINGLETON_ID,
            expectedCreationKey = recovery.creationKey!!,
            now = System.currentTimeMillis(),
        )
        return RetryResult.Published(postId)
    }
}
