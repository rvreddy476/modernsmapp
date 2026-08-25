package com.us.android.core.creator.engine

import androidx.room.withTransaction
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.Canonical
import com.us.android.core.creator.model.ImageLayer
import com.us.android.core.creator.model.ProjectReadResult
import com.us.android.core.creator.model.ProjectReader
import com.us.android.core.creator.model.Validators
import com.us.android.core.database.ComposerDraftEntity
import com.us.android.core.database.ComposerDraftFallbackStateEntity
import com.us.android.core.database.CreatorProjectEntity
import com.us.android.core.database.UsDatabase
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Durable storage for project documents.
 *
 * ## WHAT "SAVED" MEANS HERE
 *
 * A project is stored as its CANONICAL bytes plus their SHA-256. On load the
 * hash is recomputed and compared before the document is parsed — a row that
 * fails either check surfaces as [LoadResult.Corrupt], never silently dropped
 * and never "best effort" repaired, because every publish decision downstream
 * trusts these bytes.
 *
 * ## THE DUAL-WRITE RULE (rollback surface)
 *
 * While a project is REPRESENTABLE as a legacy single-image draft — at most one
 * page, no text layers — every save also refreshes `composer_draft`, so a
 * feature-flag-off in this same build still shows the user their work. The save
 * that makes a project unrepresentable clears the legacy row and marks the
 * fallback UNAVAILABLE **in the same database transaction**: a stale legacy row
 * surviving past that point is how flag-off would publish content the user has
 * since edited away.
 *
 * A fallback made unavailable by QUARANTINE or RECOVERY is never overridden
 * here — those states belong to the adoption flow, and a project save has no
 * authority over them.
 */
@Singleton
class ProjectStore @Inject constructor(
    private val db: UsDatabase,
) {

    sealed interface LoadResult {
        data class Loaded(val project: AndroidCreatorProject) : LoadResult
        data class NeedsNewerApp(val minReaderVersion: Int) : LoadResult
        data class Corrupt(val reason: String) : LoadResult
        data object Missing : LoadResult
    }

    /**
     * Persist a project, atomically with its legacy-mirror consequences.
     *
     * Validation runs first: a document that breaks its own invariants is a bug
     * in the caller, and persisting it would poison every later load.
     */
    suspend fun save(project: AndroidCreatorProject, now: Long) {
        val violations = Validators.validate(project)
        require(violations.isEmpty()) {
            "refusing to persist an invalid project: ${violations.joinToString("; ")}"
        }

        val bytes = Canonical.encode(project)
        val entity = CreatorProjectEntity(
            projectId = project.projectId,
            schemaVersion = project.schemaVersion,
            profile = project.profile,
            revision = project.revision,
            status = project.status,
            document = bytes.decodeToString(),
            documentSha256 = Canonical.sha256Hex(bytes),
            createdAtMillis = project.createdAtMillis,
            updatedAtMillis = now,
        )
        val mirror = legacyMirrorOf(project, now)

        db.withTransaction {
            db.creatorProjectDao().upsert(entity)
            applyDualWrite(mirror, now)
        }
    }

    suspend fun load(projectId: String): LoadResult {
        val row = db.creatorProjectDao().load(projectId) ?: return LoadResult.Missing

        val bytes = row.document.toByteArray(Charsets.UTF_8)
        if (Canonical.sha256Hex(bytes) != row.documentSha256) {
            return LoadResult.Corrupt("stored fingerprint does not match the stored bytes")
        }

        return when (val read = runCatching { ProjectReader.read(bytes) }.getOrNull()) {
            is ProjectReadResult.Supported -> LoadResult.Loaded(read.project)
            is ProjectReadResult.UpdateRequired -> LoadResult.NeedsNewerApp(read.minReaderVersion)
            null -> LoadResult.Corrupt("stored bytes are not a readable project document")
        }
    }

    suspend fun all(): List<CreatorProjectEntity> = db.creatorProjectDao().all()

    /**
     * Inside the save transaction: refresh, clear, or leave the legacy mirror.
     *
     *  - representable → refresh the draft; if the fallback was UNAVAILABLE only
     *    because the project had grown past the legacy shape, restore AVAILABLE
     *    (the project shrank back — editing is allowed to go both ways);
     *  - unrepresentable → clear the draft and mark PROJECT_UNREPRESENTABLE;
     *  - a QUARANTINED or RECOVERY_REQUIRED fallback is adoption's to manage
     *    and is never touched from here, in either direction.
     */
    private suspend fun applyDualWrite(mirror: ComposerDraftEntity?, now: Long) {
        val migration = db.creatorMigrationDao()
        val fallback = migration.fallbackState()
        val adoptionOwnsFallback =
            fallback?.reason == ComposerDraftFallbackStateEntity.REASON_QUARANTINED ||
                fallback?.reason == ComposerDraftFallbackStateEntity.REASON_RECOVERY_REQUIRED
        if (adoptionOwnsFallback) return

        if (mirror != null) {
            db.composerDraftDao().save(mirror)
            if (fallback?.state != ComposerDraftFallbackStateEntity.AVAILABLE) {
                migration.setFallbackState(
                    ComposerDraftFallbackStateEntity(
                        state = ComposerDraftFallbackStateEntity.AVAILABLE,
                        reason = null,
                        updatedAtMillis = now,
                    ),
                )
            }
        } else {
            migration.clearLegacyDraft()
            migration.setFallbackState(
                ComposerDraftFallbackStateEntity(
                    state = ComposerDraftFallbackStateEntity.UNAVAILABLE,
                    reason = ComposerDraftFallbackStateEntity.REASON_UNREPRESENTABLE,
                    updatedAtMillis = now,
                ),
            )
        }
    }

    /**
     * The legacy `composer_draft` this project maps to, or null when it cannot.
     *
     * Representable = at most one page, and that page carries no text layer.
     * That is exactly what the legacy composer can show; anything more would be
     * silently flattened, which is worse than declaring incompatibility.
     */
    private fun legacyMirrorOf(project: AndroidCreatorProject, now: Long): ComposerDraftEntity? {
        if (project.pages.size > 1) return null
        val page = project.pages.firstOrNull()
        if (page != null && page.layers.size > 1) return null
        if (page != null && page.layers.firstOrNull() !is ImageLayer) return null

        return ComposerDraftEntity(
            text = project.postText.value,
            // The vault path is app-internal, not an openable content URI; the
            // mirror carries the confirmed media id when one exists and never a
            // local path.
            imageUri = null,
            altText = page?.accessibility?.altText.orEmpty(),
            decorative = page?.accessibility?.decorative ?: false,
            language = project.postText.language,
            mediaId = page?.confirmedMedia?.mediaId,
            creationKey = null,
            frozenRequestJson = null,
            updatedAtMillis = now,
        )
    }
}
