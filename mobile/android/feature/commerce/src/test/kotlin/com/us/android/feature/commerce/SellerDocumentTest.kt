package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.SellerDocumentType
import com.us.android.feature.commerce.seller.AcceptedDocumentTypes
import com.us.android.feature.commerce.seller.DocumentUploadState
import com.us.android.feature.commerce.seller.MAX_DOCUMENT_BYTES
import org.junit.Test

/**
 * Sending an identity document for review.
 *
 * The last thing a seller could not do in the app. Everything else in
 * onboarding could be completed here; this one requirement sent them to
 * another channel, so the shop could not be submitted and no seller could be
 * approved without an operator stepping in.
 */
class SellerDocumentTest {

    // ─── The document vocabulary matches the server ────────────────────

    @Test
    fun `every document type matches the server allow-list`() {
        // The Kotlin enum cannot import the Go CHECK constraint, so the wire
        // strings are asserted here. A value outside
        // `seller_documents_document_type_check` is a 500 from the database,
        // which reaches the seller as a generic failure.
        val serverAllowList = setOf(
            "gst_certificate",
            "pan_card",
            "aadhaar",
            "passport",
            "business_registration",
            "address_proof",
            "cancelled_cheque",
            "other",
        )
        for (type in SellerDocumentType.entries) {
            assertThat(serverAllowList).contains(type.wire)
            assertThat(type.label).isNotEmpty()
        }
    }

    @Test
    fun `there is no free-text document type`() {
        // "other" exists in the schema and is deliberately NOT offered: a
        // reviewer needs to know what they are looking at, and a document
        // filed as "other" is one they have to open to find out.
        assertThat(SellerDocumentType.entries.map { it.wire }).doesNotContain("other")
    }

    // ─── What a reviewer can open ──────────────────────────────────────

    @Test
    fun `only formats a reviewer can open are accepted`() {
        // A PAN card is often a PDF, so this is not images-only. HEIC is
        // excluded because a reviewer's browser may not render it.
        assertThat(AcceptedDocumentTypes).containsExactly(
            "image/jpeg",
            "image/png",
            "application/pdf",
        )
        assertThat(AcceptedDocumentTypes).doesNotContain("image/heic")
    }

    @Test
    fun `the size cap is stated in bytes, not guessed`() {
        assertThat(MAX_DOCUMENT_BYTES).isEqualTo(10L * 1024 * 1024)
    }

    // ─── The stages ────────────────────────────────────────────────────

    private fun state(stage: DocumentUploadState.Stage) =
        DocumentUploadState(stage = stage)

    @Test
    fun `a fresh screen can pick a file`() {
        val s = DocumentUploadState()
        assertThat(s.busy).isFalse()
        assertThat(s.canPick).isTrue()
    }

    @Test
    fun `every in-flight stage blocks a second pick`() {
        // A second pick mid-upload reserves a second media row, and the
        // abandoned one sits in the store until the server's sweep reclaims
        // it.
        for (stage in listOf(
            DocumentUploadState.Stage.Starting,
            DocumentUploadState.Stage.Uploading,
            DocumentUploadState.Stage.Confirming,
            DocumentUploadState.Stage.Attaching,
        )) {
            assertThat(state(stage).busy).isTrue()
            assertThat(state(stage).canPick).isFalse()
        }
    }

    @Test
    fun `a finished upload is not busy and can send another`() {
        val s = state(DocumentUploadState.Stage.Done)
        assertThat(s.busy).isFalse()
        assertThat(s.canPick).isTrue()
    }

    @Test
    fun `attaching is a distinct stage, not part of uploading`() {
        // It is where the server verifies the media id belongs to THIS caller,
        // is ready, and has passed moderation — so an upload that completed
        // can still be refused, and folding it into "uploading" would leave a
        // seller watching a spinner stop with no explanation.
        assertThat(DocumentUploadState.Stage.entries).contains(DocumentUploadState.Stage.Attaching)
        assertThat(state(DocumentUploadState.Stage.Attaching).busy).isTrue()
    }

    @Test
    fun `a document number is optional`() {
        // A reviewer reads the number off the document itself; demanding it
        // typed as well adds a transcription error to a check that has the
        // original in front of it.
        val withoutNumber = DocumentUploadState(documentNumber = "")
        assertThat(withoutNumber.canPick).isTrue()
    }

    @Test
    fun `the default document type is one a reviewer expects first`() {
        assertThat(DocumentUploadState().type).isEqualTo(SellerDocumentType.PAN_CARD)
    }
}
