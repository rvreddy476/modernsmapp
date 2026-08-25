package com.us.android.core.creator.engine

import android.content.Context
import android.net.Uri
import com.us.android.core.creator.model.Accessibility
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.Canvas
import com.us.android.core.creator.model.ConfirmedMedia
import com.us.android.core.creator.model.CreatorReducer
import com.us.android.core.creator.model.ImageLayer
import com.us.android.core.creator.model.Page
import com.us.android.core.creator.model.PostText
import com.us.android.core.creator.model.SafeZone
import com.us.android.core.creator.model.SourceAsset
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import com.us.android.core.database.CreatorMigrationStagingEntity
import dagger.hilt.android.qualifiers.ApplicationContext
import java.security.SecureRandom
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Stage two and three of legacy adoption — runs after the database opens.
 *
 * ## WHERE THIS SITS IN THE THREE-STAGE DESIGN
 *
 * `MIGRATION_2_3` (stage one, pure SQL) classified the legacy draft into
 * `creator_migration_staging`. This runner performs the parts a SQL migration
 * cannot: the filesystem copy of the legacy image (stage two), and the one
 * database transaction that activates the outcome (stage three).
 *
 * It is IDEMPOTENT AND RESTARTABLE by construction. Every entry point re-reads
 * the staging row's `adoptionState`; a process death mid-run leaves `PENDING`
 * or `IMPORTING`, both of which the next launch simply retries. Nothing here
 * mints an idempotency key, invents content, or deletes the retained legacy
 * draft — those properties are what the whole staged design exists to protect.
 */
@Singleton
class AdoptionRunner @Inject constructor(
    @ApplicationContext private val context: Context,
    private val db: com.us.android.core.database.UsDatabase,
    private val vault: SourceVault,
    private val projectStore: ProjectStore,
) {

    sealed interface Outcome {
        data object NothingToAdopt : Outcome
        data class ProjectCreated(val projectId: String) : Outcome
        data class RoutedToRecovery(val kind: String) : Outcome
        data class Quarantined(val reason: String) : Outcome
    }

    /**
     * Run adoption for whatever the migration staged. Safe to call on every
     * app start; a completed or empty staging table returns immediately.
     */
    suspend fun runIfNeeded(now: Long): Outcome {
        val staging = db.creatorMigrationDao()
            .staging(CreatorMigrationStagingEntity.SINGLETON_ID)
            ?: return Outcome.NothingToAdopt
        if (staging.adoptionState == CreatorMigrationStagingEntity.STATE_ADOPTED) {
            return Outcome.NothingToAdopt
        }
        // QUARANTINED rows stay exactly where they are: the recovery screen owns
        // them, and this runner has no authority to interpret one.
        if (staging.adoptionState == CreatorMigrationStagingEntity.STATE_QUARANTINED) {
            return Outcome.Quarantined(staging.classification)
        }

        return when (val decision = LegacyAdoption.decide(staging, sourceReadable(staging))) {
            is AdoptionOutcome.Quarantine -> quarantine(staging, decision.reason, now)
            is AdoptionOutcome.RetryablePublish -> routeRetryable(staging, decision, now)
            AdoptionOutcome.AdoptAsProject -> adoptAsProject(staging, now)
            AdoptionOutcome.TextOnlyRecovery -> route(
                staging,
                kind = CreatorLegacyRecoveryEntity.KIND_TEXT_ONLY,
                now = now,
            )
            AdoptionOutcome.UnusableRecovery -> route(
                staging,
                kind = CreatorLegacyRecoveryEntity.KIND_UNUSABLE,
                now = now,
            )
        }
    }

    /** Can the legacy content:// grant still actually deliver bytes? */
    private fun sourceReadable(staging: CreatorMigrationStagingEntity): Boolean {
        val uri = staging.imageUri ?: return false
        return runCatching {
            context.contentResolver.openInputStream(Uri.parse(uri))?.use { true } ?: false
        }.getOrDefault(false)
    }

    /**
     * Stage two + three for the ordinary case: copy the source into the vault,
     * then activate a real editable project.
     *
     * The staging row moves to IMPORTING before the copy so a kill mid-copy is
     * distinguishable from never-started; the vault import itself is atomic
     * (temp + fsync + rename), so a retry after a kill re-imports cleanly.
     */
    private suspend fun adoptAsProject(
        staging: CreatorMigrationStagingEntity,
        now: Long,
    ): Outcome {
        val dao = db.creatorMigrationDao()
        dao.markState(staging.stagingId, CreatorMigrationStagingEntity.STATE_IMPORTING, now)

        val imageUri = staging.imageUri
        val entry = imageUri?.let { vault.importSource(Uri.parse(it), assetId = LEGACY_ASSET_ID) }

        // The grant died between the readability check and the copy — an
        // ordinary TOCTOU on a resource another app owns. Fall back exactly as
        // if it had been unreadable from the start.
        if (imageUri != null && entry == null) {
            return route(staging, CreatorLegacyRecoveryEntity.KIND_TEXT_ONLY, now)
        }

        val project = buildProject(staging, entry, now)
        projectStore.save(project, now)
        dao.markState(staging.stagingId, CreatorMigrationStagingEntity.STATE_ADOPTED, now)
        return Outcome.ProjectCreated(project.projectId)
    }

    private suspend fun routeRetryable(
        staging: CreatorMigrationStagingEntity,
        decision: AdoptionOutcome.RetryablePublish,
        now: Long,
    ): Outcome {
        db.creatorMigrationDao().routeToRecovery(
            recovery = CreatorLegacyRecoveryEntity(
                recoveryId = newId(),
                kind = CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH,
                text = staging.text,
                language = staging.language,
                mediaId = decision.mediaId,
                creationKey = decision.creationKey,
                frozenRequestJson = decision.frozenRequestJson,
                frozenRequestSha = decision.frozenRequestSha,
                frozenRequestLen = decision.frozenRequestLen,
                createdAtMillis = now,
            ),
            stagingId = staging.stagingId,
            now = now,
        )
        return Outcome.RoutedToRecovery(CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH)
    }

    private suspend fun route(
        staging: CreatorMigrationStagingEntity,
        kind: String,
        now: Long,
    ): Outcome {
        db.creatorMigrationDao().routeToRecovery(
            recovery = CreatorLegacyRecoveryEntity(
                recoveryId = newId(),
                kind = kind,
                text = staging.text,
                language = staging.language,
                mediaId = if (kind == CreatorLegacyRecoveryEntity.KIND_UNUSABLE) staging.mediaId else null,
                creationKey = null,
                frozenRequestJson = null,
                frozenRequestSha = null,
                frozenRequestLen = null,
                createdAtMillis = now,
            ),
            stagingId = staging.stagingId,
            now = now,
        )
        return Outcome.RoutedToRecovery(kind)
    }

    private suspend fun quarantine(
        staging: CreatorMigrationStagingEntity,
        reason: String,
        now: Long,
    ): Outcome {
        db.creatorMigrationDao()
            .markState(staging.stagingId, CreatorMigrationStagingEntity.STATE_QUARANTINED, now)
        return Outcome.Quarantined(reason)
    }

    /**
     * The v1 project a clean legacy draft becomes — fixture-1 shape when there
     * is no image, fixture-2 shape when the source copied.
     */
    private fun buildProject(
        staging: CreatorMigrationStagingEntity,
        entry: VaultEntry?,
        now: Long,
    ): AndroidCreatorProject {
        val base = AndroidCreatorProject(
            projectId = newId(),
            revision = 1,
            status = AndroidCreatorProject.STATUS_EDITING,
            createdAtMillis = now,
            updatedAtMillis = now,
            postText = PostText(staging.text, staging.language),
            canvas = if (entry == null) null else DEFAULT_CANVAS,
        )
        if (entry == null) return base

        return base.copy(
            sourceAssets = listOf(
                SourceAsset(
                    assetId = entry.assetId,
                    kind = "image",
                    vaultPath = entry.relativePath,
                    sha256 = entry.sha256,
                    bytes = entry.bytes,
                    // The legacy draft never recorded these; honest placeholders
                    // are refreshed on first decode by the editor, and claiming
                    // real dimensions here would be inventing facts.
                    mime = "image/jpeg",
                    widthPx = 1,
                    heightPx = 1,
                    origin = "legacyDraft",
                ),
            ),
            pages = listOf(
                Page(
                    pageId = "p1",
                    accessibility = Accessibility(staging.altText, staging.decorative),
                    layers = listOf(
                        ImageLayer(
                            layerId = "l1",
                            z = 0,
                            transform = CreatorReducer.IDENTITY_TRANSFORM,
                            assetRef = entry.assetId,
                            crop = CreatorReducer.FULL_CROP,
                            adjustments = CreatorReducer.NO_ADJUSTMENTS,
                        ),
                    ),
                    confirmedMedia = staging.mediaId?.let {
                        ConfirmedMedia(
                            mediaId = it,
                            processingState = "ready",
                            moderationState = "passed",
                            boundOutputSha256 = entry.sha256,
                        )
                    },
                ),
            ),
        )
    }

    private fun newId(): String {
        val alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
        val random = SecureRandom()
        return buildString(ULID_LENGTH) {
            repeat(ULID_LENGTH) { append(alphabet[random.nextInt(alphabet.length)]) }
        }
    }

    private companion object {
        const val LEGACY_ASSET_ID = "legacy1"
        const val ULID_LENGTH = 26
        val DEFAULT_CANVAS = Canvas(
            widthPx = 1080,
            heightPx = 1350,
            aspect = "4:5",
            safeZone = SafeZone(0, 0, 0, 0),
        )
    }
}
