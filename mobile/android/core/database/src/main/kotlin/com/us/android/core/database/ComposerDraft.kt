package com.us.android.core.database

import androidx.room.Dao
import androidx.room.Entity
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Upsert
import kotlinx.coroutines.flow.Flow

/**
 * A post the user started writing and has not published.
 *
 * ## WHY THIS IS IN ROOM AND NOT IN `SavedStateHandle`
 *
 * `SavedStateHandle` survives rotation and process death, which sounds like
 * enough. It is not: it is scoped to a NAVIGATION BACK-STACK ENTRY, and popping
 * that entry destroys it. So a user who taps Back — deliberately or with the
 * system gesture — loses everything they wrote, and the frozen publish operation
 * goes with it.
 *
 * Losing the frozen operation is worse than losing the text. It carries the
 * creation key, and the creation key is the ONLY thing that stops a retry
 * duplicating a post the server already committed. A draft that survives to the
 * next attempt without its key is a draft that can publish twice.
 *
 * ## WHAT IS STORED, AND WHAT DELIBERATELY IS NOT
 *
 * Stored: what the user cannot retype and what recovery needs — the text, which
 * image they picked, their accessibility decision, their language, how far the
 * upload got, and the frozen request bytes plus creation key.
 *
 * Not stored: transient UI (a progress fraction, a discard dialog, validation
 * flags). Restoring "uploading, 40%" for an upload that died with the process
 * would be a lie, and the composer resumes from the durable facts instead.
 *
 * ## ONE ROW
 *
 * [SINGLETON_ID] is a fixed primary key. Slice C has one composer and no
 * multi-draft surface; a table that can hold many drafts implies a drafts list
 * that does not exist, and would silently accumulate rows nothing ever shows.
 */
@Entity(tableName = "composer_draft")
data class ComposerDraftEntity(
    @PrimaryKey val id: String = SINGLETON_ID,

    val text: String,
    /** Content URI of the picked image, as a string so it survives storage. */
    val imageUri: String?,
    val altText: String,
    val decorative: Boolean,
    val language: String,

    /**
     * A CONFIRMED, ready media id.
     *
     * Present means "reuse this asset", never "upload it again". Re-uploading a
     * confirmed image would leave the first one abandoned and double the bytes
     * the user pays for.
     */
    val mediaId: String?,

    /**
     * The `Idempotency-Key` of a frozen publish, and the exact request bytes it
     * was minted for.
     *
     * Both or neither. A key without its bytes cannot be safely retried, because
     * the retry would have to rebuild the request and might rebuild it
     * differently — which the server correctly refuses as key reuse.
     */
    val creationKey: String?,
    val frozenRequestJson: String?,

    val updatedAtMillis: Long,
) {
    companion object {
        /** The composer is a single surface, so its draft is a single row. */
        const val SINGLETON_ID = "composer"
    }
}

@Dao
interface ComposerDraftDao {

    /**
     * Observed, not read once.
     *
     * The ViewModel is not the only writer that matters — a restore has to see
     * whatever the last write left, including one made moments before the
     * process died.
     */
    @Query("SELECT * FROM composer_draft WHERE id = :id")
    fun observe(id: String = ComposerDraftEntity.SINGLETON_ID): Flow<ComposerDraftEntity?>

    @Query("SELECT * FROM composer_draft WHERE id = :id")
    suspend fun load(id: String = ComposerDraftEntity.SINGLETON_ID): ComposerDraftEntity?

    @Upsert
    suspend fun save(draft: ComposerDraftEntity)

    @Query("DELETE FROM composer_draft WHERE id = :id")
    suspend fun clear(id: String = ComposerDraftEntity.SINGLETON_ID)
}
