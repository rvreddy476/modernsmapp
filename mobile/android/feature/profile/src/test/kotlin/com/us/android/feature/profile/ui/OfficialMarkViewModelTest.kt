package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.profile.data.CertificateReader
import com.us.android.feature.profile.data.OFFICIAL_FALLBACK_MESSAGE
import com.us.android.feature.profile.data.OfficialCertificateUploader
import com.us.android.feature.profile.data.OfficialMarkSource
import com.us.android.feature.profile.data.OrgApplicationBody
import com.us.android.feature.profile.data.OrgStatus
import com.us.android.feature.profile.data.PickedCertificate
import kotlinx.coroutines.CompletableDeferred
import org.junit.Rule
import org.junit.Test
import java.io.ByteArrayInputStream

/** Request verification › Official mark (2026-11-15): every step is the server's `next`. */
class OfficialMarkViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val source = FakeOfficialMarkSource()

    private val reader = FakeCertificateReader()
    private val uploader = FakeCertificateUploader()

    private fun vm() = OfficialMarkViewModel(source, reader, uploader)

    private fun certificateForm(): OfficialMarkViewModel {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.applyResult = AppResult.Success(OrgStatus(next = "email_code"))
        return vm().apply { fillForm() }
    }

    @Test
    fun `a certificate of the wrong type is refused before any upload`() {
        val vm = certificateForm()
        reader.picked = certificate(mime = "application/msword")

        vm.onCertificatePicked("content://doc/1")

        assertThat(vm.state.value.certificateError).isEqualTo("Upload a PDF, JPEG or PNG.")
        assertThat(vm.state.value.certificateMediaId).isNull()
        assertThat(vm.state.value.uploadingCertificate).isFalse()
        assertThat(uploader.uploads).isEmpty()
    }

    @Test
    fun `a certificate over 10 MB is refused before any upload, and exactly 10 MB is let through`() {
        val vm = certificateForm()
        reader.picked = certificate(size = 10_485_761L)

        vm.onCertificatePicked("content://doc/1")

        assertThat(vm.state.value.certificateError).isEqualTo("The certificate must be 10 MB or smaller.")
        assertThat(uploader.uploads).isEmpty()

        reader.picked = certificate(size = 10_485_760L)
        vm.onCertificatePicked("content://doc/2")
        assertThat(uploader.uploads).hasSize(1)
        assertThat(vm.state.value.certificateError).isNull()
    }

    @Test
    fun `an uploaded certificate is sent as document_media_id`() {
        val vm = certificateForm()
        reader.picked = certificate(name = "gst-certificate.pdf")
        uploader.result = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

        vm.onCertificatePicked("content://doc/1")

        assertThat(vm.state.value.certificateName).isEqualTo("gst-certificate.pdf")
        assertThat(uploader.uploads.single().mimeType).isEqualTo("application/pdf")
        vm.submit()
        assertThat(source.applied.single().documentMediaId).isEqualTo("7c9e6679-7425-40de-944b-e07fc1f90ae7")
    }

    @Test
    fun `a JPEG and a PNG are accepted too`() {
        val vm = certificateForm()
        reader.picked = certificate(mime = "image/jpeg")
        vm.onCertificatePicked("content://doc/1")
        vm.removeCertificate()
        reader.picked = certificate(mime = "image/png")
        vm.onCertificatePicked("content://doc/2")

        assertThat(uploader.uploads.map { it.mimeType }).containsExactly("image/jpeg", "image/png").inOrder()
    }

    @Test
    fun `send code is disabled while the certificate uploads`() {
        val vm = certificateForm()
        reader.picked = certificate()
        uploader.gate = CompletableDeferred()

        vm.onCertificatePicked("content://doc/1")

        assertThat(vm.state.value.uploadingCertificate).isTrue()
        assertThat(vm.state.value.canSubmit).isFalse()
        vm.submit()
        assertThat(source.applied).isEmpty()

        uploader.gate!!.complete(Unit)
        assertThat(vm.state.value.uploadingCertificate).isFalse()
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `a failed upload says so, attaches nothing and never submits`() {
        val vm = certificateForm()
        reader.picked = certificate()
        uploader.result = null

        vm.onCertificatePicked("content://doc/1")

        assertThat(vm.state.value.certificateError)
            .isEqualTo("The certificate didn't upload. Check your connection and try again.")
        assertThat(vm.state.value.certificateMediaId).isNull()
        assertThat(source.applied).isEmpty()
        vm.submit()
        assertThat(source.applied.single().documentMediaId).isNull()
    }

    @Test
    fun `an unreadable pick says the upload failed`() {
        val vm = certificateForm()
        reader.picked = null

        vm.onCertificatePicked("content://doc/1")

        assertThat(vm.state.value.certificateError)
            .isEqualTo("The certificate didn't upload. Check your connection and try again.")
        assertThat(uploader.uploads).isEmpty()
    }

    @Test
    fun `remove drops the certificate from the submit`() {
        val vm = certificateForm()
        reader.picked = certificate()

        vm.onCertificatePicked("content://doc/1")
        assertThat(vm.state.value.certificateMediaId).isNotNull()
        vm.removeCertificate()

        assertThat(vm.state.value.certificateName).isNull()
        assertThat(vm.state.value.certificateMediaId).isNull()
        vm.submit()
        assertThat(source.applied.single().documentMediaId).isNull()
    }

    @Test
    fun `an application with a certificate shows the review step after the code`() {
        val vm = certificateForm()
        reader.picked = certificate()
        vm.onCertificatePicked("content://doc/1")
        vm.submit()
        source.confirmResult = AppResult.Success(OrgStatus(next = "review", status = "in_review"))
        vm.onCode("123456")

        vm.confirm()

        assertThat(vm.state.value.step).isEqualTo(OfficialStep.Review)
    }

    private fun certificate(
        name: String = "certificate.pdf",
        mime: String = "application/pdf",
        size: Long = 200_000L,
    ) = PickedCertificate(name, mime, size, UploadSource { ByteArrayInputStream(ByteArray(0)) })

    private fun OfficialMarkViewModel.fillForm() {
        onLegalName("Acme Media Private Limited")
        onRegistration("29abcde1234f1z5")
        onDomain("acme.in")
        onEmail("legal@acme.in")
    }

    @Test
    fun `each next draws its own step`() {
        val cases = mapOf(
            "apply" to OfficialStep.Apply,
            "email_code" to OfficialStep.EmailCode,
            "review" to OfficialStep.Review,
            "" to OfficialStep.Done,
        )
        cases.forEach { (next, step) ->
            source.statuses += AppResult.Success(OrgStatus(next = next))
            assertThat(vm().state.value.step).isEqualTo(step)
        }
    }

    @Test
    fun `send code is held until all four fields are filled, and the registration is uppercased`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        val vm = vm()
        vm.onLegalName("Acme")
        vm.onRegistration("29abcde1234f1z5")
        vm.onDomain("acme.in")
        assertThat(vm.state.value.canSubmit).isFalse()
        vm.onEmail("legal@acme.in")
        assertThat(vm.state.value.canSubmit).isTrue()
        assertThat(vm.state.value.registration).isEqualTo("29ABCDE1234F1Z5")
    }

    @Test
    fun `the registration is capped at 15 for a GSTIN and 21 for a CIN`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        val vm = vm()
        vm.onRegistration("x".repeat(30))
        assertThat(vm.state.value.registration).hasLength(15)
        vm.onRegistrationKind(RegistrationKind.CIN)
        vm.onRegistration("u".repeat(30))
        assertThat(vm.state.value.registration).hasLength(21)
    }

    @Test
    fun `submit sends exactly one registration and moves to the code step`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.applyResult = AppResult.Success(OrgStatus(next = "email_code", status = "email_pending"))
        val vm = vm()
        vm.fillForm()

        vm.submit()

        val sent = source.applied.single()
        assertThat(sent.gstin).isEqualTo("29ABCDE1234F1Z5")
        assertThat(sent.cin).isNull()
        assertThat(sent.officialDomain).isEqualTo("acme.in")
        assertThat(vm.state.value.step).isEqualTo(OfficialStep.EmailCode)
    }

    @Test
    fun `a CIN application sends the CIN and no GSTIN`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.applyResult = AppResult.Success(OrgStatus(next = "email_code"))
        val vm = vm()
        vm.onRegistrationKind(RegistrationKind.CIN)
        vm.fillForm()

        vm.submit()

        assertThat(source.applied.single().gstin).isNull()
        assertThat(source.applied.single().cin).isEqualTo("29ABCDE1234F1Z5")
    }

    @Test
    fun `a refused submit shows the server's sentence under the button`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.applyResult = refusal("FREE_MAIL_DOMAIN", "Use an address on your organisation's own domain.")
        val vm = vm()
        vm.fillForm()

        vm.submit()

        assertThat(vm.state.value.error).isEqualTo("Use an address on your organisation's own domain.")
        assertThat(vm.state.value.step).isEqualTo(OfficialStep.Apply)
    }

    @Test
    fun `a refusal with no sentence falls back to the plain line`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.applyResult = AppResult.Failure(AppError.NoNetwork())
        val vm = vm()
        vm.fillForm()

        vm.submit()

        assertThat(vm.state.value.error).isEqualTo(OFFICIAL_FALLBACK_MESSAGE)
    }

    @Test
    fun `person verification required is an error card and the form stays usable`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.applyResult = refusal("PERSON_VERIFICATION_REQUIRED", "Verify yourself as a person first.")
        val vm = vm()
        vm.fillForm()

        vm.submit()

        assertThat(vm.state.value.personRequired).isEqualTo("Verify yourself as a person first.")
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `confirm success shows the mark`() {
        source.statuses += AppResult.Success(OrgStatus(next = "email_code"))
        source.confirmResult = AppResult.Success(
            OrgStatus(isVerified = true, verificationLevel = "organisation", organisationName = "Acme", next = ""),
        )
        val vm = vm()
        vm.onCode("12a3456")

        vm.confirm()

        assertThat(source.codes.single()).isEqualTo("123456")
        assertThat(vm.state.value.step).isEqualTo(OfficialStep.Done)
        assertThat(vm.state.value.isGovernment).isFalse()
    }

    @Test
    fun `a wrong code stays on the code step with the server's sentence`() {
        source.statuses += AppResult.Success(OrgStatus(next = "email_code"))
        source.confirmResult = refusal("CODE_MISMATCH", "That code is not right.")
        val vm = vm()
        vm.onCode("000000")

        vm.confirm()

        assertThat(vm.state.value.step).isEqualTo(OfficialStep.EmailCode)
        assertThat(vm.state.value.error).isEqualTo("That code is not right.")
        assertThat(source.statusReads).isEqualTo(1)
    }

    @Test
    fun `too many code attempts re-reads the status and falls back to the form`() {
        source.statuses += AppResult.Success(OrgStatus(next = "email_code"))
        source.statuses += AppResult.Success(OrgStatus(next = "apply", status = "refused"))
        source.confirmResult = refusal("TOO_MANY_CODE_ATTEMPTS", "Too many tries. Apply again.")
        val vm = vm()
        vm.onCode("000000")

        vm.confirm()

        assertThat(source.statusReads).isEqualTo(2)
        assertThat(vm.state.value.step).isEqualTo(OfficialStep.Apply)
        assertThat(vm.state.value.error).isEqualTo("Too many tries. Apply again.")
    }

    @Test
    fun `start again shows the form locally`() {
        source.statuses += AppResult.Success(OrgStatus(next = "email_code", status = "email_pending"))
        val vm = vm()

        vm.startAgain()

        assertThat(vm.state.value.step).isEqualTo(OfficialStep.Apply)
        assertThat(source.applied).isEmpty()
    }

    @Test
    fun `a refused or rejected last application shows the banner above the form`() {
        listOf("refused", "rejected").forEach { status ->
            source.statuses += AppResult.Success(OrgStatus(next = "apply", status = status, refusalCode = "X"))
            assertThat(vm().state.value.showRefused).isTrue()
        }
        source.statuses += AppResult.Success(OrgStatus(next = "apply", status = "superseded"))
        assertThat(vm().state.value.showRefused).isFalse()
    }

    @Test
    fun `accepting a government invitation re-renders with the returned status`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.acceptResult = AppResult.Success(
            OrgStatus(isVerified = true, verificationLevel = "government", organisationName = "City", next = ""),
        )
        val vm = vm()
        vm.openInvite()
        vm.onInviteCode(" INV-123 ")

        vm.accept()

        assertThat(source.invites.single()).isEqualTo("INV-123")
        assertThat(vm.state.value.step).isEqualTo(OfficialStep.Done)
        assertThat(vm.state.value.isGovernment).isTrue()
        assertThat(vm.state.value.inviteOpen).isFalse()
    }

    @Test
    fun `a refused invitation shows the server's sentence`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        source.acceptResult = refusal("GOVERNMENT_VERIFICATION_DISABLED", "Government marks are not open yet.")
        val vm = vm()
        vm.openInvite()
        vm.onInviteCode("INV-123")

        vm.accept()

        assertThat(vm.state.value.inviteError).isEqualTo("Government marks are not open yet.")
    }

    @Test
    fun `the settings row shows only when the status read answers 200`() {
        source.statuses += AppResult.Success(OrgStatus(next = "apply"))
        assertThat(OfficialMarkEntryViewModel(source).shown.value).isTrue()

        source.statuses += refusal("ORG_VERIFICATION_DISABLED", "Official marks are not open yet.", 503)
        assertThat(OfficialMarkEntryViewModel(source).shown.value).isFalse()

        source.statuses += AppResult.Failure(AppError.NoNetwork())
        assertThat(OfficialMarkEntryViewModel(source).shown.value).isFalse()
    }

    private fun refusal(code: String, message: String, status: Int = 422) =
        AppResult.Failure(AppError.Unknown(code = code, statusCode = status, message = message))
}

private class FakeCertificateReader : CertificateReader {
    var picked: PickedCertificate? = null
    override suspend fun read(uri: String): PickedCertificate? = picked
}

/** Records each upload; [gate] holds it in flight until completed. */
private class FakeCertificateUploader : OfficialCertificateUploader {
    var result: String? = "media-1"
    var gate: CompletableDeferred<Unit>? = null
    val uploads = mutableListOf<PickedCertificate>()

    override suspend fun upload(certificate: PickedCertificate): String? {
        uploads += certificate
        gate?.await()
        return result
    }
}

/** Answers each call from its queue; records what was sent. */
private class FakeOfficialMarkSource : OfficialMarkSource {
    val statuses = ArrayDeque<AppResult<OrgStatus>>()
    var statusReads = 0
    var applyResult: AppResult<OrgStatus> = AppResult.Failure(AppError.NoNetwork())
    var confirmResult: AppResult<OrgStatus> = AppResult.Failure(AppError.NoNetwork())
    var acceptResult: AppResult<OrgStatus> = AppResult.Failure(AppError.NoNetwork())
    val applied = mutableListOf<OrgApplicationBody>()
    val codes = mutableListOf<String>()
    val invites = mutableListOf<String>()

    override suspend fun status(): AppResult<OrgStatus> {
        statusReads++
        return statuses.removeFirstOrNull() ?: AppResult.Failure(AppError.NoNetwork())
    }

    override suspend fun apply(body: OrgApplicationBody): AppResult<OrgStatus> = applyResult.also { applied += body }

    override suspend fun confirmEmail(code: String): AppResult<OrgStatus> = confirmResult.also { codes += code }

    override suspend fun acceptGovernment(inviteCode: String): AppResult<OrgStatus> =
        acceptResult.also { invites += inviteCode }
}
