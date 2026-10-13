package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.SellerDocument
import com.us.android.core.commerce.model.SellerDocumentType
import com.us.android.core.commerce.network.SaveDocumentsRequest
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.AADHAAR_NUMBER_NOT_ACCEPTED
import com.us.android.feature.commerce.seller.AADHAAR_NUMBER_REFUSED_COPY
import com.us.android.feature.commerce.seller.AcceptedDocumentTypes
import com.us.android.feature.commerce.seller.DocumentUploadState
import com.us.android.feature.commerce.seller.MASKED_AADHAAR_GUIDANCE
import com.us.android.feature.commerce.seller.MAX_DOCUMENT_BYTES
import com.us.android.feature.commerce.seller.isAadhaarNumberRefusal
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.Response

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

    // ─── Aadhaar: an upload reference, never a number ──────────────────
    //
    // commerce migration 035. The platform may not store an Aadhaar number, so
    // the server keeps an Aadhaar document as its media id only and answers
    // 400 AADHAAR_NUMBER_NOT_ACCEPTED to any number sent with it — and to an
    // Aadhaar-shaped number under any other type except a cancelled cheque.

    private val aadhaarShaped = "234567890124"

    @Test
    fun `aadhaar does not ask for a number, every other type does`() {
        for (type in SellerDocumentType.entries) {
            val asks = DocumentUploadState(type = type).asksForNumber
            assertThat(asks).isEqualTo(type != SellerDocumentType.AADHAAR)
        }
    }

    @Test
    fun `nothing is ever sent as an aadhaar number, even a stale one`() {
        // A number left in state from before a type switch must not ride along.
        val stale = DocumentUploadState(type = SellerDocumentType.AADHAAR, documentNumber = aadhaarShaped)
        assertThat(stale.numberToSend).isNull()
    }

    @Test
    fun `a number is sent trimmed for other types, and a blank is not sent`() {
        assertThat(DocumentUploadState(documentNumber = " ABCDE1234F ").numberToSend).isEqualTo("ABCDE1234F")
        assertThat(DocumentUploadState(documentNumber = "   ").numberToSend).isNull()
    }

    @Test
    fun `choosing aadhaar clears the typed number and its error`() {
        val typed = DocumentUploadState(documentNumber = "ABCDE1234F", numberError = "refused")
        val aadhaar = typed.withType(SellerDocumentType.AADHAAR)
        assertThat(aadhaar.documentNumber).isEmpty()
        assertThat(aadhaar.numberError).isNull()

        // Any other type keeps what the seller typed.
        assertThat(typed.withType(SellerDocumentType.PASSPORT).documentNumber).isEqualTo("ABCDE1234F")
    }

    @Test
    fun `typing is ignored while aadhaar is selected`() {
        val aadhaar = DocumentUploadState(type = SellerDocumentType.AADHAAR)
        assertThat(aadhaar.withNumber(aadhaarShaped).documentNumber).isEmpty()
    }

    @Test
    fun `editing the number clears its refusal`() {
        val refused = DocumentUploadState(documentNumber = aadhaarShaped, numberError = AADHAAR_NUMBER_REFUSED_COPY)
        val edited = refused.withNumber("p1234567")
        assertThat(edited.numberError).isNull()
        assertThat(edited.documentNumber).isEqualTo("P1234567")
    }

    @Test
    fun `only the aadhaar refusal code is read as a refused number`() {
        assertThat(CommerceError.Unexpected(AADHAAR_NUMBER_NOT_ACCEPTED, "x").isAadhaarNumberRefusal()).isTrue()
        assertThat(CommerceError.Unexpected("INVALID_DOCUMENT_TYPE", "x").isAadhaarNumberRefusal()).isFalse()
        assertThat(CommerceError.Network(null).isAadhaarNumberRefusal()).isFalse()
    }

    @Test
    fun `the copy asks for a masked aadhaar and repeats no number`() {
        assertThat(MASKED_AADHAAR_GUIDANCE).contains("masked")
        assertThat(MASKED_AADHAAR_GUIDANCE).contains("last 4")
        // A refusal that echoed digits would put the number back on screen.
        assertThat(AADHAAR_NUMBER_REFUSED_COPY.any { it.isDigit() }).isFalse()
        assertThat(AADHAAR_NUMBER_REFUSED_COPY).contains("masked")
    }

    // ─── The wire ──────────────────────────────────────────────────────

    private class CapturingApi(private val refuse: Boolean = false) : FakeCommerceApi() {
        var sent: SaveDocumentsRequest? = null

        override suspend fun saveDocuments(body: SaveDocumentsRequest): Response<ApiEnvelope<Unit>> {
            sent = body
            return if (refuse) {
                Response.error(
                    400,
                    """{"error":{"code":"AADHAAR_NUMBER_NOT_ACCEPTED","message":"an Aadhaar number cannot be stored"}}"""
                        .toResponseBody("application/json".toMediaType()),
                )
            } else {
                Response.success(null)
            }
        }
    }

    @Test
    fun `the repository never puts an aadhaar number on the wire`() = runBlocking {
        val api = CapturingApi()
        val result = CommerceRepository(api).saveDocuments(
            listOf(
                SellerDocument(SellerDocumentType.AADHAAR, mediaId = "m-1", documentNumber = aadhaarShaped),
                SellerDocument(SellerDocumentType.PAN_CARD, mediaId = "m-2", documentNumber = " abcde1234f "),
            ),
        )
        assertThat(result).isInstanceOf(CommerceResult.Success::class.java)
        val docs = api.sent!!.documents
        assertThat(docs[0].documentType).isEqualTo("aadhaar")
        assertThat(docs[0].documentNumber).isNull()
        assertThat(docs[1].documentNumber).isEqualTo("ABCDE1234F")
    }

    @Test
    fun `a 400 AADHAAR_NUMBER_NOT_ACCEPTED reaches the screen as a refused number`() = runBlocking {
        val result = CommerceRepository(CapturingApi(refuse = true)).saveDocuments(
            listOf(SellerDocument(SellerDocumentType.ADDRESS_PROOF, mediaId = "m-1", documentNumber = aadhaarShaped)),
        )
        assertThat(result).isInstanceOf(CommerceResult.Failure::class.java)
        assertThat((result as CommerceResult.Failure).error.isAadhaarNumberRefusal()).isTrue()
    }
}
