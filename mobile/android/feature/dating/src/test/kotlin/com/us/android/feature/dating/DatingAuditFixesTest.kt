package com.us.android.feature.dating

import android.Manifest
import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.outcomeUnknown
import com.us.android.feature.dating.home.MatchDetailState
import com.us.android.feature.dating.home.MatchDetailViewModel
import com.us.android.feature.dating.home.NoteHidden
import com.us.android.feature.dating.home.NoteReveal
import com.us.android.feature.dating.home.SparkNoteCopy
import com.us.android.feature.dating.home.SparkNoteReveals
import com.us.android.feature.dating.home.incomingSparkUi
import com.us.android.feature.dating.location.Coordinates
import com.us.android.feature.dating.location.DatingLocationPermissions
import com.us.android.feature.dating.network.BlockedPersonDto
import com.us.android.feature.dating.network.ClientConfigDto
import com.us.android.feature.dating.network.DatingApi
import com.us.android.feature.dating.network.PanicLocationConsentDto
import com.us.android.feature.dating.network.SafetyAlertDto
import com.us.android.feature.dating.network.SafetyAlertLocationDto
import com.us.android.feature.dating.network.SparkNoteDto
import com.us.android.feature.dating.network.TrustedContactRequest
import com.us.android.feature.dating.network.UnblockedDto
import com.us.android.feature.dating.privacy.BlocksViewModel
import com.us.android.feature.dating.privacy.PrivacyViewModel
import com.us.android.feature.dating.safety.DatingConversationKindness
import com.us.android.feature.dating.safety.DatingTeardown
import com.us.android.feature.dating.safety.PanicAlertState
import com.us.android.feature.dating.safety.PanicAlertViewModel
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportReason
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.safety.SafetyCopy
import com.us.android.feature.dating.safety.SafetyViewModel
import com.us.android.feature.dating.safety.ScreenProtectionConfig
import com.us.android.feature.dating.safety.boundedCoordinates
import com.us.android.feature.dating.safety.clockAndAgo
import com.us.android.feature.dating.safety.sharedAtLabel
import com.us.android.feature.dating.safety.validAlertId
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

/**
 * The fixes for the Android dating audit of 30 Oct 2026
 * (docs/harness/audit-2026-10-30-dating-android.md), against the server
 * contract as it stands on 8 Nov: H1, M1, M4, L1, L2, L4, L6 and the
 * server-side hidden-note change. M2 is pinned in :core:notifications, M3 in
 * PulseKindnessAndPrivacyTest and :feature:chat, L3 in PremiumResendTest, L5
 * and the push routing in :app.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class DatingAuditFixesTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()

    // ── H1: a trusted contact's panic alert ─────────────────────────────────

    private fun alert(id: String = ALERT) = PanicAlertViewModel(
        SavedStateHandle(mapOf("alertId" to id)),
        repository,
        photoUrls(),
    )

    @Test
    fun `a panic alert opens by its id and shows who and where, from the server`() = runTest {
        val vm = alert()

        val loaded = vm.state.value as PanicAlertState.Loaded
        assertThat(api.calls).contains("safety-alert:$ALERT")
        assertThat(loaded.alert.name).isEqualTo("Asha")
        assertThat(loaded.alert.location).isEqualTo(Coordinates(17.44, 78.39))
    }

    @Test
    fun `an alert whose sender did not share location shows no point`() = runTest {
        api.safetyAlertResponse = { ok(fixture("safety_alert_get_200.json", SafetyAlertDto.serializer())) }

        val loaded = alert().state.value as PanicAlertState.Loaded

        assertThat(loaded.alert.location).isNull()
    }

    @Test
    fun `a resolved alert, or one sharing a point off the globe, shows no point`() = runTest {
        val shared = fixture("safety_alert_get_200_location_shared.json", SafetyAlertDto.serializer())
        api.safetyAlertResponse = { ok(shared.copy(status = "resolved")) }
        assertThat((alert().state.value as PanicAlertState.Loaded).alert.location).isNull()

        val offGlobe = SafetyAlertLocationDto(latitude = 91.0, longitude = 78.39)
        api.safetyAlertResponse = { ok(shared.copy(location = offGlobe)) }
        assertThat((alert().state.value as PanicAlertState.Loaded).alert.location).isNull()

        // location_shared false wins over a point that came anyway.
        api.safetyAlertResponse = { ok(shared.copy(locationShared = false)) }
        assertThat((alert().state.value as PanicAlertState.Loaded).alert.location).isNull()
    }

    @Test
    fun `404 and 410 are their own answers, and other failures can be retried`() = runTest {
        api.safetyAlertResponse = { refusedWithFixture(404, "safety_alert_get_404_not_found.json") }
        assertThat(alert().state.value).isEqualTo(PanicAlertState.NotFound)

        api.safetyAlertResponse = { refusedWithFixture(410, "safety_alert_get_410_expired.json") }
        assertThat(alert().state.value).isEqualTo(PanicAlertState.Expired)

        // Outside the pilot the gateway answers a bare 404: still "not available".
        api.safetyAlertResponse = { gatewayNotFound() }
        assertThat(alert().state.value).isEqualTo(PanicAlertState.NotFound)

        api.safetyAlertResponse = { offline() }
        val vm = alert()
        assertThat(vm.state.value).isInstanceOf(PanicAlertState.Failed::class.java)
        api.safetyAlertResponse = { ok(fixture("safety_alert_get_200.json", SafetyAlertDto.serializer())) }
        vm.refresh()
        assertThat(vm.state.value).isInstanceOf(PanicAlertState.Loaded::class.java)
    }

    @Test
    fun `an alert id that is not a UUID is never sent to the server`() = runTest {
        // "1-2-3-4-5" is a UUID to java.util.UUID, but not the canonical form the server mints.
        listOf("../matches", "abc", "$ALERT/../x", "", "1-2-3-4-5").forEach { bad ->
            assertThat(alert(bad).state.value).isEqualTo(PanicAlertState.NotFound)
        }
        assertThat(api.calls.filter { it.startsWith("safety-alert") }).isEmpty()
        assertThat(validAlertId(ALERT.uppercase())).isEqualTo(ALERT)
        assertThat(validAlertId(" $ALERT ")).isEqualTo(ALERT)
        assertThat(validAlertId(null)).isNull()
    }

    @Test
    fun `only a point on the globe is a point`() {
        assertThat(boundedCoordinates(17.44, 78.39)).isEqualTo(Coordinates(17.44, 78.39))
        assertThat(boundedCoordinates(90.0, 180.0)).isEqualTo(Coordinates(90.0, 180.0))
        assertThat(boundedCoordinates(-90.0, -180.0)).isEqualTo(Coordinates(-90.0, -180.0))
        assertThat(boundedCoordinates(90.0001, 0.0)).isNull()
        assertThat(boundedCoordinates(0.0, -180.0001)).isNull()
        assertThat(boundedCoordinates(Double.NaN, 0.0)).isNull()
        assertThat(boundedCoordinates(0.0, Double.POSITIVE_INFINITY)).isNull()
        assertThat(boundedCoordinates(null, 0.0)).isNull()
        assertThat(boundedCoordinates(0.0, null)).isNull()
    }

    // ── M4: panic location is per contact, opt in, with the server's words ──

    private fun safety() = SafetyViewModel(repository, session, FakeLocation(), photoUrls())

    @Test
    fun `adding a trusted contact never opts them in to the panic point`() = runTest {
        api.matches = listOf(match("m-1", "friend"))
        val vm = safety()

        vm.addContact("friend")

        assertThat(api.trustedAdds.single()).isEqualTo("friend" to TrustedContactRequest(shareLocationOnPanic = false))
        assertThat(api.panicLocationWrites).isEmpty()
        assertThat(vm.state.value.panicLocationTo).isEmpty()
    }

    @Test
    fun `the switch shows the server's effective answer for each contact`() = runTest {
        api.trusted = listOf(trustedContact("friend").copy(shareLocationOnPanic = true), trustedContact("other"))

        val vm = safety()

        assertThat(vm.state.value.panicLocationTo).containsExactly("friend")
        assertThat(vm.state.value.panicLocationConsent?.version).isEqualTo(PANIC_CONSENT_VERSION)
    }

    @Test
    fun `turning it on asks first, and only agreeing sends it with the wording's version`() = runTest {
        api.trusted = listOf(trustedContact("friend", person("friend", name = "Asha")))
        val vm = safety()

        vm.setPanicLocation("friend", on = true)

        assertThat(api.panicLocationWrites).isEmpty()
        assertThat(vm.state.value.askingConsentFor).isEqualTo("friend")
        assertThat(vm.state.value.consentName).isEqualTo("Asha")

        vm.confirmPanicLocation()

        val (contact, body) = api.panicLocationWrites.single()
        assertThat(contact).isEqualTo("friend")
        assertThat(body.shareLocationOnPanic).isTrue()
        assertThat(body.consentVersion).isEqualTo(PANIC_CONSENT_VERSION)
        assertThat(vm.state.value.askingConsentFor).isNull()
        assertThat(vm.state.value.panicLocationTo).containsExactly("friend")
    }

    @Test
    fun `dismissing the wording sends nothing`() = runTest {
        api.trusted = listOf(trustedContact("friend"))
        val vm = safety()

        vm.setPanicLocation("friend", on = true)
        vm.dismissConsent()
        vm.confirmPanicLocation()

        assertThat(api.panicLocationWrites).isEmpty()
        assertThat(vm.state.value.panicLocationTo).isEmpty()
    }

    @Test
    fun `turning it off revokes at once, with no version`() = runTest {
        api.trusted = listOf(trustedContact("friend").copy(shareLocationOnPanic = true))
        val vm = safety()

        vm.setPanicLocation("friend", on = false)

        val (_, body) = api.panicLocationWrites.single()
        assertThat(body.shareLocationOnPanic).isFalse()
        assertThat(body.consentVersion).isNull()
        assertThat(vm.state.value.askingConsentFor).isNull()
        assertThat(vm.state.value.panicLocationTo).isEmpty()
    }

    @Test
    fun `a revoke never carries a consent version, and a grant always does`() = runTest {
        repository.setPanicLocation("friend", share = false, consentVersion = PANIC_CONSENT_VERSION)
        repository.setPanicLocation("friend", share = true, consentVersion = PANIC_CONSENT_VERSION)

        val writes = api.panicLocationWrites.map { it.second }
        assertThat(writes.map { it.consentVersion }).containsExactly(null, PANIC_CONSENT_VERSION).inOrder()
        assertThat(writes.map { it.shareLocationOnPanic }).containsExactly(false, true).inOrder()
    }

    @Test
    fun `without the server's wording the switch cannot be turned on`() = runTest {
        api.panicConsent = null
        api.trusted = listOf(trustedContact("friend"))
        val vm = safety()

        vm.setPanicLocation("friend", on = true)

        assertThat(vm.state.value.askingConsentFor).isNull()
        assertThat(api.panicLocationWrites).isEmpty()
    }

    @Test
    fun `someone who is not a trusted contact cannot be switched`() = runTest {
        api.trusted = listOf(trustedContact("friend"))
        val vm = safety()

        vm.setPanicLocation("stranger", on = true)
        vm.setPanicLocation("stranger", on = false)

        assertThat(vm.state.value.askingConsentFor).isNull()
        assertThat(api.panicLocationWrites).isEmpty()
    }

    @Test
    fun `changed wording keeps the dialog up with the new words, and nothing is shared`() = runTest {
        api.trusted = listOf(trustedContact("friend"))
        api.panicConsent = PanicLocationConsentDto(version = "panic_location_v0", text = "old words")
        api.panicLocationResponse = { _, _ ->
            refusedWithFixture(422, "trusted_contact_patch_422_consent_required.json")
        }
        val vm = safety()

        vm.setPanicLocation("friend", on = true)
        vm.confirmPanicLocation()

        val state = vm.state.value
        assertThat(state.askingConsentFor).isEqualTo("friend")
        assertThat(state.panicLocationConsent?.version).isEqualTo("panic_location_v1")
        assertThat(state.panicLocationConsent?.text).startsWith("If I press panic, send this person my exact location")
        assertThat(state.message?.text).isEqualTo(SafetyCopy.CONSENT_UPDATED)
        assertThat(state.panicLocationTo).isEmpty()
        assertThat(state.consentBusy).isFalse()
        assertThat(api.panicLocationWrites.single().second.consentVersion).isEqualTo("panic_location_v0")
    }

    @Test
    fun `any other refusal closes the dialog and leaves the switch where the server has it`() = runTest {
        api.trusted = listOf(trustedContact("friend"))
        api.panicLocationResponse = { _, _ -> refused(404, "NOT_FOUND") }
        val vm = safety()

        vm.setPanicLocation("friend", on = true)
        vm.confirmPanicLocation()

        assertThat(vm.state.value.askingConsentFor).isNull()
        assertThat(vm.state.value.panicLocationTo).isEmpty()
        assertThat(vm.state.value.message).isNotNull()
    }

    @Test
    fun `the server's effective answer wins over what was asked`() = runTest {
        api.trusted = listOf(trustedContact("friend"))
        // The server accepted the PATCH but did not record a consent (say, an older wording).
        api.panicLocationResponse = { id, _ -> ok(trustedContact(id).copy(shareLocationOnPanic = false)) }
        val vm = safety()

        vm.setPanicLocation("friend", on = true)
        vm.confirmPanicLocation()

        assertThat(vm.state.value.panicLocationTo).isEmpty()
    }

    // ── M1: a share is one point, said plainly, with when it was taken ──────

    @Test
    fun `a share is labelled with when it was taken and how long ago`() {
        val zone = ZoneId.of("Asia/Kolkata")
        val now = Instant.parse("2026-11-08T14:12:00Z")

        assertThat(sharedAtLabel("2026-11-08T13:14:00Z", now, zone)).isEqualTo("Shared at 18:44 (58 min ago)")
        assertThat(sharedAtLabel("2026-11-08T12:07:00Z", now, zone)).isEqualTo("Shared at 17:37 (2 h 5 min ago)")
        assertThat(sharedAtLabel("2026-11-08T14:11:30Z", now, zone)).isEqualTo("Shared at 19:41 (just now)")
        // A skewed clock that puts the share ahead of now still reads "just now".
        assertThat(clockAndAgo(Instant.parse("2026-11-08T14:20:00Z"), now, zone)).isEqualTo("19:50 (just now)")
        assertThat(sharedAtLabel("", now, zone)).isNull()
        assertThat(sharedAtLabel(null, now, zone)).isNull()
        assertThat(sharedAtLabel("not a time", now, zone)).isNull()
    }

    @Test
    fun `nothing on the safety screen calls a share live`() {
        listOf(
            SafetyCopy.SHARE_SECTION,
            SafetyCopy.SHARE_HINT,
            SafetyCopy.SHARE_RATIONALE_TITLE,
            SafetyCopy.SHARE_RATIONALE_BODY,
            SafetyCopy.SHARED_WITH_YOU_TITLE,
            SafetyCopy.SHARED_WITH_YOU_BODY,
            SafetyCopy.sharingFor(30),
            SafetyCopy.sharedBy("Asha"),
        ).forEach { assertThat(it).ignoringCase().doesNotContain("live") }
        assertThat(SafetyCopy.PANIC_BODY).contains("trusted contacts")
    }

    // ── L1: sign-out and profile deletion forget the account ────────────────

    @Test
    fun `the teardown forgets the session's people, the kind answers and the Pulse chats`() = runTest {
        val kindAnswers = FakeKindAnswers().also { it.stored["msg-1"] = true }
        api.matches = listOf(match("m1", "x"))
        repository.matches()
        session.removePerson("blocked-1")
        assertThat(repository.matchForConversation("conv-m1")).isEqualTo("m1")

        teardown(repository, session, kindAnswers).forgetLocalData()

        assertThat(session.removed.value).isEmpty()
        assertThat(session.profile.value).isNull()
        assertThat(kindAnswers.stored).isEmpty()
        assertThat(repository.matchForConversation("conv-m1")).isNull()
    }

    @Test
    fun `sign-out is the same teardown`() = runTest {
        session.removePerson("blocked-1")

        teardown(repository, session).onSignOut()

        assertThat(session.removed.value).isEmpty()
        assertThat(session.profile.value).isNull()
    }

    @Test
    fun `a failed read on a later visit is ON, even after an explicit off`() = runTest {
        api.clientConfigResponse = { ok(fixture("client_config_get_200_off.json", ClientConfigDto.serializer())) }
        val config = ScreenProtectionConfig(repository, session)
        assertThat(config.enabled()).isFalse()

        // Dating opened again: the old answer is stale, and this read fails.
        session.entered()
        api.clientConfigResponse = { offline() }

        assertThat(config.enabled()).isTrue()
    }

    @Test
    fun `the teardown forgets the screen-protection answer too`() = runTest {
        api.clientConfigResponse = { ok(fixture("client_config_get_200_off.json", ClientConfigDto.serializer())) }
        val config = ScreenProtectionConfig(repository, session)
        assertThat(config.enabled()).isFalse()
        val teardown = DatingTeardown(
            repository,
            DatingConversationKindness(repository, session, FakeKindAnswers()) { 0L },
            config,
            session,
        )

        teardown.forgetLocalData()
        api.clientConfigResponse = { offline() }

        // Asked again, and a failed read is ON.
        assertThat(config.enabled()).isTrue()
    }

    @Test
    fun `deleting the dating profile runs the sign-out teardown`() = runTest {
        val kindAnswers = FakeKindAnswers().also { it.stored["msg-1"] = false }
        session.removePerson("blocked-1")
        val vm = PrivacyViewModel(
            repository,
            session,
            UnconfinedTestDispatcher(testScheduler),
            teardown(repository, session, kindAnswers),
        )

        vm.deleteProfile(null)

        assertThat(vm.state.value.deleted).isTrue()
        assertThat(session.removed.value).isEmpty()
        assertThat(kindAnswers.stored).isEmpty()
    }

    // ── L2: onboarding asks for approximate location only ───────────────────

    @Test
    fun `onboarding asks for approximate only, and only safety asks for precise`() {
        assertThat(DatingLocationPermissions.ONBOARDING.toList())
            .containsExactly(Manifest.permission.ACCESS_COARSE_LOCATION)
        assertThat(DatingLocationPermissions.SAFETY.toList())
            .containsExactly(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION)
    }

    // ── L3 helper: which failures may have left something behind ────────────

    @Test
    fun `an outcome is unknown only for the network, a 5xx and an unreadable answer`() {
        assertThat(DatingError.Network(null).outcomeUnknown()).isTrue()
        assertThat(DatingError.Unexpected(null, null).outcomeUnknown()).isTrue()
        assertThat(DatingError.Unexpected(200, null).outcomeUnknown()).isTrue()
        assertThat(DatingError.Unexpected(502, null).outcomeUnknown()).isTrue()
        assertThat(DatingError.Unexpected(500, null).outcomeUnknown()).isTrue()
        assertThat(DatingError.Unexpected(499, null).outcomeUnknown()).isFalse()
        assertThat(DatingError.Unexpected(400, null).outcomeUnknown()).isFalse()
        assertThat(DatingError.Refused(500, "X", "", null).outcomeUnknown()).isTrue()
        assertThat(DatingError.Refused(499, "X", "", null).outcomeUnknown()).isFalse()
        assertThat(DatingError.Refused(409, "IDEMPOTENCY_KEY_REUSED", "", null).outcomeUnknown()).isFalse()
        assertThat(DatingError.NotAvailable.outcomeUnknown()).isFalse()
        assertThat(DatingError.Unauthorized.outcomeUnknown()).isFalse()
    }

    // ── L4: matches are the open buckets, not status=all ────────────────────

    private fun matchWith(id: String, status: String) = match(id, "u-$id").copy(status = status)

    @Test
    fun `matches are read as the active then the quiet bucket, never all`() = runTest {
        api.matches = listOf(
            matchWith("a", "matched"),
            matchWith("b", "quiet"),
            matchWith("c", "conversing"),
            matchWith("d", "expired"),
            matchWith("e", "closed"),
        )

        val open = (repository.matches() as DatingResult.Success).value

        assertThat(open.map { it.id }).containsExactly("a", "c", "b").inOrder()
        assertThat(api.calls.filter { it.startsWith("matches:") })
            .containsExactly("matches:active", "matches:quiet")
            .inOrder()
    }

    @Test
    fun `either bucket failing fails the list, rather than showing half of it`() = runTest {
        api.matches = listOf(matchWith("a", "matched"), matchWith("b", "quiet"))
        api.matchesBucketResponse = { status -> if (status == "quiet") offline() else null }
        assertThat(repository.matches()).isInstanceOf(DatingResult.Failure::class.java)

        api.matchesBucketResponse = { status -> if (status == "active") refused(500, "QUERY_FAILED") else null }
        assertThat(repository.matches()).isInstanceOf(DatingResult.Failure::class.java)
        // The active bucket failing asks nothing more.
        assertThat(api.calls.last()).isEqualTo("matches:active")
    }

    // ── L6: unblock, and a report that failed from match detail ─────────────

    @Test
    fun `unblocking lets the person back into this session's lists`() = runTest {
        api.blocked = listOf(BlockedPersonDto(userId = "user-bad", firstName = "Asha"))
        session.removePerson("user-bad")
        session.removePerson("user-other")
        val vm = BlocksViewModel(repository, session)

        vm.unblock("user-bad")

        assertThat(session.isRemoved("user-bad")).isFalse()
        assertThat(session.isRemoved("user-other")).isTrue()
    }

    @Test
    fun `a failed unblock keeps the person out`() = runTest {
        session.removePerson("user-bad")
        val failing = object : DatingApi by api {
            override suspend fun unblock(userId: String) = offline<UnblockedDto>()
        }
        val vm = BlocksViewModel(DatingRepository(failing, testJson), session)

        vm.unblock("user-bad")

        assertThat(session.isRemoved("user-bad")).isTrue()
    }

    private fun TestScope.detail(): MatchDetailViewModel {
        api.matches = listOf(match("m-1", "user-bad"))
        val vm = MatchDetailViewModel(
            SavedStateHandle(mapOf("matchId" to "m-1")),
            repository,
            session,
            SafetyActions(repository, session),
            photoUrls(),
        )
        assertThat(vm.state.value).isInstanceOf(MatchDetailState.Loaded::class.java)
        return vm
    }

    @Test
    fun `a report that fails from match detail keeps what was written`() = runTest {
        api.reportResponse = { offline() }
        val vm = detail()
        vm.openReport()
        val draft = checkNotNull(vm.reporting.value)
            .copy(reason = ReportReason.HARASSMENT, details = "said awful things")

        vm.report(draft)

        assertThat(vm.reporting.value).isEqualTo(draft)
        assertThat(vm.message.value).isNotNull()
    }

    @Test
    fun `a report that goes through closes the sheet`() = runTest {
        val vm = detail()
        vm.openReport()
        assertThat(vm.reporting.value).isEqualTo(ReportDraft(targetId = "user-bad"))

        vm.report(ReportDraft(targetId = "user-bad", reason = ReportReason.HARASSMENT))

        assertThat(vm.reporting.value).isNull()
        assertThat(api.reports.single().targetId).isEqualTo("user-bad")
    }

    // ── The server's hidden-note change (its audit L4) ──────────────────────

    @Test
    fun `a folded note arrives without its words and still folds`() {
        val ui = incomingSparkUi(
            "s-1",
            "u-1",
            person("u-1"),
            note = null,
            superSpark = false,
            urls = photoUrls(),
            noteHidden = "your_words",
        )

        assertThat(ui.note).isNull()
        assertThat(ui.noteHidden).isEqualTo(NoteHidden.YOUR_WORDS)
        // No reason: an ordinary note, shown as it is.
        assertThat(incomingSparkUi("s-2", "u-1", person("u-1"), "hi", false, photoUrls()).noteHidden).isNull()
    }

    @Test
    fun `a folded note's words are read once, on tap`() = runTest {
        val reveals = SparkNoteReveals(repository, this)

        reveals.reveal("s-1")
        advanceUntilIdle()
        reveals.reveal("s-1")
        advanceUntilIdle()

        assertThat(reveals.state.value["s-1"]).isEqualTo(NoteReveal.Shown("the folded words"))
        assertThat(api.calls.count { it == "spark-note:s-1" }).isEqualTo(1)
    }

    @Test
    fun `a note that is gone says so, and a failed read can be tried again`() = runTest {
        val reveals = SparkNoteReveals(repository, this)

        api.sparkNoteResponse = { refused(404, "NOT_FOUND") }
        reveals.reveal("s-1")
        advanceUntilIdle()
        assertThat(reveals.state.value["s-1"]).isEqualTo(NoteReveal.Failed(SparkNoteCopy.GONE))

        api.sparkNoteResponse = { offline() }
        reveals.reveal("s-2")
        advanceUntilIdle()
        assertThat(reveals.state.value["s-2"]).isInstanceOf(NoteReveal.Failed::class.java)
        assertThat(reveals.state.value["s-2"]).isNotEqualTo(NoteReveal.Failed(SparkNoteCopy.GONE))

        api.sparkNoteResponse = { ok(SparkNoteDto(sparkId = it, note = "now readable")) }
        reveals.reveal("s-2")
        advanceUntilIdle()
        assertThat(reveals.state.value["s-2"]).isEqualTo(NoteReveal.Shown("now readable"))
        assertThat(api.calls.count { it == "spark-note:s-2" }).isEqualTo(2)
    }

    @Test
    fun `a note read that answers blank words is not shown as a note`() = runTest {
        api.sparkNoteResponse = { ok(SparkNoteDto(sparkId = it, note = "  ")) }
        val reveals = SparkNoteReveals(repository, this)

        reveals.reveal("s-1")
        advanceUntilIdle()

        assertThat(reveals.state.value["s-1"]).isEqualTo(NoteReveal.Failed(SparkNoteCopy.GONE))
    }

    private companion object {
        const val ALERT = "0b6f3c1e-8a2d-4c7e-9f10-2a3b4c5d6e7f"
    }
}
