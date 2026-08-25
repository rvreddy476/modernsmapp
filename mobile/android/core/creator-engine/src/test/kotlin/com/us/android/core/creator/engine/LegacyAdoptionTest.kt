package com.us.android.core.creator.engine

import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.Canonical
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import com.us.android.core.database.CreatorMigrationStagingEntity
import org.junit.Test

/**
 * CS-LB-1L — legacy adoption never produces a duplicate post.
 *
 * ## THE BUG THESE TESTS EXIST TO PREVENT
 *
 * A composer draft can carry a `creationKey` and `frozenRequestJson` from a
 * publish the server ALREADY COMMITTED, whose response was lost. If adoption
 * asks "is the image still readable?" first, that row becomes an editable
 * project, the frozen operation is discarded, and the next publish mints a new
 * key — so the post appears twice and nothing in the system notices.
 *
 * Operation authority therefore outranks source availability, and the test
 * below named `...even when its source is still readable` is the one that pins
 * it.
 */
class LegacyAdoptionTest {

    private val textRequest =
        """{"text":"Notes from a slow morning","visibility":"public","content_type":"post",""" +
            """"post_type":"text","app_origin":"postbook","media_ids":[],"language":"en",""" +
            """"distribution":{"version":1,"main_feed":true,"notify_subscribers":false,""" +
            """"create_reel_preview":false}}"""

    private val mediaId = "6f3b1c58-2a41-4e0d-9c77-1b5a0d8e4f21"

    private val imageRequest =
        """{"text":"Terrace","visibility":"public","content_type":"post",""" +
            """"post_type":"image","app_origin":"postbook","media_ids":["$mediaId"],""" +
            """"language":"en","distribution":{"version":1,"main_feed":true,""" +
            """"notify_subscribers":false,"create_reel_preview":false}}"""

    private fun staging(
        text: String = "a draft",
        imageUri: String? = null,
        mediaId: String? = null,
        key: String? = null,
        frozen: String? = null,
        classification: String = CreatorMigrationStagingEntity.CLASSIFICATION_CLEAN,
    ) = CreatorMigrationStagingEntity(
        stagingId = CreatorMigrationStagingEntity.SINGLETON_ID,
        text = text,
        imageUri = imageUri,
        altText = "",
        decorative = false,
        language = "en",
        mediaId = mediaId,
        creationKey = key,
        frozenRequestJson = frozen,
        classification = classification,
        adoptionState = CreatorMigrationStagingEntity.STATE_PENDING,
        attempts = 0,
        updatedAtMillis = 0,
    )

    private fun recovery(
        frozen: String?,
        mediaId: String?,
        sha: String? = frozen?.let { Canonical.sha256Hex(it.toByteArray()) },
        len: Int? = frozen?.toByteArray()?.size,
    ) = CreatorLegacyRecoveryEntity(
        recoveryId = "r1",
        kind = CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH,
        text = "a draft",
        language = "en",
        mediaId = mediaId,
        creationKey = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f",
        frozenRequestJson = frozen,
        frozenRequestSha = sha,
        frozenRequestLen = len,
        createdAtMillis = 0,
    )

    // ------------------------------------------------------------------
    // Priority: operation before source
    // ------------------------------------------------------------------

    /**
     * THE duplicate-post test.
     *
     * Both a readable source AND a frozen operation. The source is deliberately
     * readable, because that is the case the old ordering got wrong.
     */
    @Test
    fun `a frozen operation wins even when its source is still readable`() {
        val outcome = LegacyAdoption.decide(
            staging(imageUri = "content://media/42", mediaId = mediaId, key = "k", frozen = imageRequest),
            sourceReadable = true,
        )

        assertThat(outcome).isInstanceOf(AdoptionOutcome.RetryablePublish::class.java)
        assertThat((outcome as AdoptionOutcome.RetryablePublish).creationKey).isEqualTo("k")
    }

    /** The same row with no readable source reaches the same conclusion. */
    @Test
    fun `a frozen operation is retryable with no source at all`() {
        val outcome = LegacyAdoption.decide(
            staging(mediaId = mediaId, key = "k", frozen = imageRequest),
            sourceReadable = false,
        )

        assertThat(outcome).isInstanceOf(AdoptionOutcome.RetryablePublish::class.java)
    }

    /** A legacy TEXT publish is retryable too, with no media id — approval R-3. */
    @Test
    fun `a text-only frozen operation with no media id is retryable`() {
        val outcome = LegacyAdoption.decide(
            staging(key = "k", frozen = textRequest),
            sourceReadable = false,
        )

        val retry = outcome as AdoptionOutcome.RetryablePublish
        assertThat(retry.mediaId).isNull()
        assertThat(retry.frozenRequestJson).isEqualTo(textRequest)
    }

    /** Half a frozen operation is never interpreted, whatever else is present. */
    @Test
    fun `a half-frozen row quarantines regardless of a readable source`() {
        val outcome = LegacyAdoption.decide(
            staging(
                imageUri = "content://media/42",
                key = "k",
                classification = CreatorMigrationStagingEntity.CLASSIFICATION_HALF_FROZEN,
            ),
            sourceReadable = true,
        )

        assertThat(outcome).isInstanceOf(AdoptionOutcome.Quarantine::class.java)
    }

    // ------------------------------------------------------------------
    // No operation: the source finally matters
    // ------------------------------------------------------------------

    @Test
    fun `a row with no operation and a readable source becomes a project`() {
        val outcome = LegacyAdoption.decide(
            staging(imageUri = "content://media/42"),
            sourceReadable = true,
        )

        assertThat(outcome).isEqualTo(AdoptionOutcome.AdoptAsProject)
    }

    /**
     * A confirmed remote asset with no local source is NOT made into a project.
     *
     * The schema requires a rendered output with a vault path, byte count, hash
     * and dimensions. None of those exist for an asset that only lives on the
     * server, so building a project would mean inventing them — and every later
     * decision would rest on invented facts.
     */
    @Test
    fun `a confirmed remote asset with no source is unusable, not a project`() {
        val outcome = LegacyAdoption.decide(
            staging(mediaId = mediaId),
            sourceReadable = false,
        )

        assertThat(outcome).isEqualTo(AdoptionOutcome.UnusableRecovery)
    }

    /**
     * Recovery is for a LOST image, not a never-present one.
     *
     * A draft whose `imageUri` existed but no longer reads keeps its text via
     * recovery. A draft that never had an image is the fixture-1 shape and
     * adopts straight into a text project — there is nothing to lose.
     */
    @Test
    fun `text whose image was lost is kept as text-only recovery`() {
        val outcome = LegacyAdoption.decide(
            staging(text = "worth keeping", imageUri = "content://media/42"),
            sourceReadable = false,
        )

        assertThat(outcome).isEqualTo(AdoptionOutcome.TextOnlyRecovery)
    }

    @Test
    fun `text that never had an image adopts directly as a project`() {
        val outcome = LegacyAdoption.decide(staging(text = "worth keeping"), sourceReadable = false)

        assertThat(outcome).isEqualTo(AdoptionOutcome.AdoptAsProject)
    }

    @Test
    fun `an empty unrecoverable row quarantines rather than inventing content`() {
        val outcome = LegacyAdoption.decide(staging(text = "  "), sourceReadable = false)

        assertThat(outcome).isInstanceOf(AdoptionOutcome.Quarantine::class.java)
    }

    // ------------------------------------------------------------------
    // Pre-retry verification: no network call until these pass
    // ------------------------------------------------------------------

    @Test
    fun `matching image bytes and media id verify`() {
        assertThat(LegacyAdoption.verifyFrozenRequest(recovery(imageRequest, mediaId)))
            .isEqualTo(FrozenRequestVerdict.Retry)
    }

    @Test
    fun `a text request with no media id verifies`() {
        assertThat(LegacyAdoption.verifyFrozenRequest(recovery(textRequest, mediaId = null)))
            .isEqualTo(FrozenRequestVerdict.Retry)
    }

    /** A stored hash that does not describe the stored bytes is unretryable. */
    @Test
    fun `a sha mismatch quarantines without a network call`() {
        val verdict = LegacyAdoption.verifyFrozenRequest(
            recovery(imageRequest, mediaId, sha = "0".repeat(64)),
        )

        assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
        assertThat((verdict as FrozenRequestVerdict.Quarantine).reason).contains("sha256")
    }

    @Test
    fun `a length mismatch quarantines without a network call`() {
        val verdict = LegacyAdoption.verifyFrozenRequest(
            recovery(imageRequest, mediaId, len = 1),
        )

        assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
        assertThat((verdict as FrozenRequestVerdict.Quarantine).reason).contains("length")
    }

    /**
     * The media id must actually appear in the request being replayed.
     *
     * Replaying a request for a DIFFERENT asset under this row's key would
     * publish something the user never composed.
     */
    @Test
    fun `a media id absent from the request quarantines`() {
        val verdict = LegacyAdoption.verifyFrozenRequest(
            recovery(imageRequest, mediaId = "b27d9a10-88c5-4f3e-a1d6-3e9c7f204b8a"),
        )

        assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
    }

    /** A text request paired with a media id is incoherent and must not be sent. */
    @Test
    fun `a media id on a text request quarantines`() {
        val verdict = LegacyAdoption.verifyFrozenRequest(recovery(textRequest, mediaId))

        assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
    }

    /** An image request with no media id is equally incoherent. */
    @Test
    fun `an image request with no media id quarantines`() {
        val verdict = LegacyAdoption.verifyFrozenRequest(recovery(imageRequest, mediaId = null))

        assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
    }
}
