package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.window.SecureFlagCounter
import com.us.android.core.common.window.SecureFlagTarget
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.filters.Dealbreaker
import com.us.android.feature.dating.filters.FiltersViewModel
import com.us.android.feature.dating.home.IncomingSparkUi
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.NoteHidden
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.home.incomingSparkUi
import com.us.android.feature.dating.network.ClientConfigDto
import com.us.android.feature.dating.network.CommentFilterDto
import com.us.android.feature.dating.network.HideKnownDto
import com.us.android.feature.dating.network.KindCheckDto
import com.us.android.feature.dating.network.PassFiltersDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.privacy.CommentFilterCopy
import com.us.android.feature.dating.privacy.CommentFilterPhase
import com.us.android.feature.dating.privacy.CommentFilterRules
import com.us.android.feature.dating.privacy.CommentFilterViewModel
import com.us.android.feature.dating.privacy.HideKnownCopy
import com.us.android.feature.dating.privacy.HideKnownPhase
import com.us.android.feature.dating.privacy.HideKnownViewModel
import com.us.android.feature.dating.profile.ProfileOptionsStore
import com.us.android.feature.dating.safety.DatingConversationKindness
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportPersonPhase
import com.us.android.feature.dating.safety.ReportPersonViewModel
import com.us.android.feature.dating.safety.ReportReason
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.safety.ScreenProtectionConfig
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Rule
import org.junit.Test

/**
 * Pulse mechanics M13 (kind messages), M16 (hide from people I know) and M18
 * (screen protection), and the M12 change that keeps saved pass dealbreakers
 * without a pass: what switches each on or hides it, and how each refusal reads.
 */
class PulseKindnessAndPrivacyTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)

    // ── M13: kind messages, the chat seam ───────────────────────────────────

    private var now = 1_000_000L

    private fun kindness() = DatingConversationKindness(repository, session) { now }

    private fun unkind() = ok(fixture("kind_check_post_200_unkind.json", KindCheckDto.serializer()))

    private fun kind() = ok(fixture("kind_check_post_200_kind.json", KindCheckDto.serializer()))

    @Test
    fun `a conversation that is not a Pulse chat is left alone`() = runTest {
        api.matches = listOf(match("m1", "x"))
        api.kindCheckResponse = { unkind() }
        val k = kindness()

        assertThat(k.appliesTo("conv-other")).isFalse()
        assertThat(k.mightBeUnkind("conv-other", "you look stupid")).isFalse()
        assertThat(k.shouldCover("conv-other", "msg-1", "you look stupid")).isFalse()
        assertThat(k.bothered("conv-other", "msg-1", true)).isFalse()

        assertThat(api.kindChecks).isEmpty()
        assertThat(api.botheredWrites).isEmpty()
        // One look at the matches, then none until the interval has passed.
        assertThat(api.calls.count { it == "matches" }).isEqualTo(1)
    }

    @Test
    fun `a Pulse chat is recognised from the matches, and a later match after the interval`() = runTest {
        api.matches = listOf(match("m1", "x"))
        val k = kindness()

        assertThat(k.appliesTo("conv-m1")).isTrue()
        api.matches = listOf(match("m1", "x"), match("m2", "y"))
        assertThat(k.appliesTo("conv-m2")).isFalse()
        now += DatingConversationKindness.LOOKUP_INTERVAL_MS
        assertThat(k.appliesTo("conv-m2")).isTrue()
    }

    @Test
    fun `a match the dating screens already read needs no lookup`() = runTest {
        api.matches = listOf(match("m1", "x"))
        repository.match("m1")
        val k = kindness()

        assertThat(k.appliesTo("conv-m1")).isTrue()
        assertThat(api.calls.count { it == "matches" }).isEqualTo(0)
    }

    @Test
    fun `chat-service naming a Pulse chat needs no lookup, and any other source is ignored`() = runTest {
        val k = kindness()

        k.conversationSource("conv-p", sourceApp = "dating", sourceId = "m9")
        k.conversationSource("conv-q", sourceApp = "something_else", sourceId = "m8")
        k.conversationSource("conv-r", sourceApp = "dating", sourceId = "")

        assertThat(k.appliesTo("conv-p")).isTrue()
        assertThat(api.calls.count { it == "matches" }).isEqualTo(0)
        assertThat(repository.matchForConversation("conv-q")).isNull()
        assertThat(repository.matchForConversation("conv-r")).isNull()

        api.botheredResponse = { matchId, body -> ok(com.us.android.feature.dating.network.BotheredDto(matchId = matchId, bothered = body.bothered, offerReport = true)) }
        assertThat(k.bothered("conv-p", "msg-1", true)).isTrue()
        assertThat(api.botheredWrites.single().first).isEqualTo("m9")
    }

    @Test
    fun `outside the Dating pilot nothing is asked again`() = runTest {
        api.matchesResponse = { gatewayNotFound() }
        val k = kindness()

        assertThat(k.appliesTo("conv-a")).isFalse()
        now += DatingConversationKindness.LOOKUP_INTERVAL_MS * 10
        assertThat(k.appliesTo("conv-b")).isFalse()
        assertThat(api.calls.count { it == "matches" }).isEqualTo(1)
    }

    @Test
    fun `an unkind text is held before sending, a kind one is not`() = runTest {
        api.matches = listOf(match("m1", "x"))
        val k = kindness()

        api.kindCheckResponse = { unkind() }
        assertThat(k.mightBeUnkind("conv-m1", "  you look stupid ")).isTrue()
        api.kindCheckResponse = { kind() }
        assertThat(k.mightBeUnkind("conv-m1", "you look lovely")).isFalse()
        // The text goes trimmed, and is the only thing sent.
        assertThat(api.kindChecks).containsExactly("you look stupid", "you look lovely").inOrder()
    }

    @Test
    fun `any failure just sends`() = runTest {
        api.matches = listOf(match("m1", "x"))
        val k = kindness()

        api.kindCheckResponse = { refused(429, "KIND_CHECK_RATE_LIMITED") }
        assertThat(k.mightBeUnkind("conv-m1", "hello")).isFalse()
        api.kindCheckResponse = { refusedWithFixture(400, "kind_check_post_400_invalid.json") }
        assertThat(k.mightBeUnkind("conv-m1", "hello")).isFalse()
        api.kindCheckResponse = { offline() }
        assertThat(k.mightBeUnkind("conv-m1", "hello")).isFalse()
        // An answer without `kind` says nothing either.
        api.kindCheckResponse = { ok(KindCheckDto()) }
        assertThat(k.mightBeUnkind("conv-m1", "hello")).isFalse()
        // And the checks are still on.
        assertThat(k.appliesTo("conv-m1")).isTrue()
    }

    @Test
    fun `a text over the server's limit and a blank one are sent without asking`() = runTest {
        api.matches = listOf(match("m1", "x"))
        api.kindCheckResponse = { unkind() }
        val k = kindness()

        assertThat(k.mightBeUnkind("conv-m1", "a".repeat(DatingConversationKindness.MAX_TEXT + 1))).isFalse()
        assertThat(k.mightBeUnkind("conv-m1", "   ")).isFalse()
        assertThat(api.kindChecks).isEmpty()
    }

    @Test
    fun `404 MECHANIC_NOT_ENABLED switches the checks off for the session, silently`() = runTest {
        api.matches = listOf(match("m1", "x"))
        // The fake's default: the golden 404.
        val k = kindness()

        assertThat(k.mightBeUnkind("conv-m1", "hello")).isFalse()
        assertThat(k.appliesTo("conv-m1")).isFalse()
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isFalse()
        assertThat(k.bothered("conv-m1", "msg-1", true)).isFalse()
        assertThat(api.kindChecks).hasSize(1)
        assertThat(api.botheredWrites).isEmpty()
    }

    @Test
    fun `a received text is judged once by message id, and a failure is not a verdict`() = runTest {
        api.matches = listOf(match("m1", "x"))
        val k = kindness()

        api.kindCheckResponse = { offline() }
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isFalse()
        api.kindCheckResponse = { unkind() }
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isTrue()
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isTrue()
        api.kindCheckResponse = { kind() }
        assertThat(k.shouldCover("conv-m1", "msg-2", "see you at 7")).isFalse()
        assertThat(k.shouldCover("conv-m1", "msg-2", "see you at 7")).isFalse()

        assertThat(api.kindChecks).hasSize(3)
    }

    @Test
    fun `bothered yes offers the report flow, no does not, both with the match id`() = runTest {
        api.matches = listOf(match("m1", "x"))
        api.botheredResponse = { matchId, body -> ok(fixture("bothered_post_201.json", com.us.android.feature.dating.network.BotheredDto.serializer()).copy(matchId = matchId, bothered = body.bothered, offerReport = body.bothered)) }
        val k = kindness()

        assertThat(k.bothered("conv-m1", "msg-1", true)).isTrue()
        assertThat(k.bothered("conv-m1", "msg-1", false)).isFalse()
        assertThat(api.botheredWrites.map { it.first }).containsExactly("m1", "m1")
        assertThat(api.botheredWrites.map { it.second.bothered }).containsExactly(true, false).inOrder()
    }

    @Test
    fun `answering no uncovers that message for good`() = runTest {
        api.matches = listOf(match("m1", "x"))
        api.kindCheckResponse = { unkind() }
        val k = kindness()
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isTrue()

        k.bothered("conv-m1", "msg-1", false)

        // Opening the chat again does not cover it again, nor ask the server.
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isFalse()
        assertThat(api.kindChecks).hasSize(1)
    }

    @Test
    fun `a refused bothered answer offers nothing`() = runTest {
        api.matches = listOf(match("m1", "x"))
        api.botheredResponse = { _, _ -> refused(404, "NOT_FOUND") }
        val k = kindness()

        assertThat(k.bothered("conv-m1", "msg-1", true)).isFalse()
    }

    @Test
    fun `sign-out forgets which chats are Pulse chats and the verdicts`() = runTest {
        api.matches = listOf(match("m1", "x"))
        api.kindCheckResponse = { unkind() }
        val k = kindness()
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isTrue()

        repository.forgetConversations()
        k.forget()
        api.matches = emptyList()

        assertThat(k.appliesTo("conv-m1")).isFalse()
        assertThat(k.shouldCover("conv-m1", "msg-1", "you look stupid")).isFalse()
    }

    // ── M13: the report flow from a chat ────────────────────────────────────

    private fun reportPerson(messageId: String? = "msg-1", name: String? = "Asha") = ReportPersonViewModel(
        SavedStateHandle(mapOf("userId" to "x", "messageId" to messageId, "name" to name)),
        safety,
    )

    @Test
    fun `the chat's report carries the message as evidence and blocks them`() = runTest {
        val vm = reportPerson()
        val draft = vm.state.value.draft
        assertThat(draft.targetId).isEqualTo("x")
        assertThat(draft.messageIds).containsExactly("msg-1")
        assertThat(vm.state.value.name).isEqualTo("Asha")

        vm.submit(draft.copy(reason = ReportReason.HARASSMENT))

        val sent = api.reports.single()
        assertThat(sent.targetId).isEqualTo("x")
        assertThat(sent.evidence?.messageIds).containsExactly("msg-1")
        assertThat(vm.state.value.phase).isEqualTo(ReportPersonPhase.SENT)
        assertThat(session.isRemoved("x")).isTrue()
    }

    @Test
    fun `a failed report keeps the sheet and says why`() = runTest {
        api.reportResponse = { refused(429, "REPORT_RATE_LIMITED") }
        val vm = reportPerson(messageId = null, name = null)
        assertThat(vm.state.value.draft.messageIds).isEmpty()

        vm.submit(ReportDraft(targetId = "x", reason = ReportReason.SPAM))

        assertThat(vm.state.value.phase).isEqualTo(ReportPersonPhase.EDITING)
        assertThat(vm.state.value.message?.type).isEqualTo(UsMessageType.Error)
        assertThat(session.isRemoved("x")).isFalse()
    }

    @Test
    fun `closing the sheet sends nothing and can be undone`() = runTest {
        val vm = reportPerson()
        vm.close()
        assertThat(vm.state.value.phase).isEqualTo(ReportPersonPhase.CLOSED)
        vm.reopen()
        assertThat(vm.state.value.phase).isEqualTo(ReportPersonPhase.EDITING)
        assertThat(api.reports).isEmpty()
    }

    // ── M13: spark notes ────────────────────────────────────────────────────

    @Test
    fun `an incoming spark's hidden note arrives folded with its reason`() = runTest {
        api.incoming = fixture("sparks_incoming_get_200_note_hidden.json", ListSerializer(SparkDto.serializer()))
            .map { it.copy(fromUserId = "x", person = it.person?.copy(userId = "x")) }
        val vm = SparksViewModel(repository, session, safety, photoUrls())

        val row = (vm.state.value as ListState.Items<IncomingSparkUi>).items.single()
        assertThat(row.note).isEqualTo("you look stupid")
        assertThat(row.noteHidden).isEqualTo(NoteHidden.UNKIND)
    }

    @Test
    fun `each note reason has its own words, an unknown one folds too, and no note is never folded`() {
        fun ui(note: String?, hidden: String?) =
            incomingSparkUi(sparkId = "s", fromUserId = "x", person = person("x"), note = note, superSpark = false, urls = photoUrls(), noteHidden = hidden)

        assertThat(ui("hi", "your_words").noteHidden).isEqualTo(NoteHidden.YOUR_WORDS)
        assertThat(ui("hi", "something_new").noteHidden).isEqualTo(NoteHidden.OTHER)
        assertThat(ui("hi", "").noteHidden).isNull()
        assertThat(ui("hi", null).noteHidden).isNull()
        assertThat(ui(null, "unkind").noteHidden).isNull()
        assertThat(NoteHidden.entries.map { it.reason }.toSet()).hasSize(NoteHidden.entries.size)
    }

    // ── M13: the comment filter ─────────────────────────────────────────────

    private fun goldenFilter() = ok(fixture("comment_filter_get_200.json", CommentFilterDto.serializer()))

    @Test
    fun `the comment filter is hidden while the server flag is off`() = runTest {
        val vm = CommentFilterViewModel(repository)
        assertThat(vm.state.value.phase).isEqualTo(CommentFilterPhase.HIDDEN)
    }

    @Test
    fun `the filter loads from the golden`() = runTest {
        api.commentFilterResponse = { goldenFilter() }
        val vm = CommentFilterViewModel(repository)

        assertThat(vm.state.value.phase).isEqualTo(CommentFilterPhase.READY)
        assertThat(vm.state.value.filterUnkind).isTrue()
        assertThat(vm.state.value.words).containsExactly("ex", "cricket").inOrder()
    }

    @Test
    fun `a word is added lower-cased and trimmed, and the whole filter is sent`() = runTest {
        api.commentFilterResponse = { goldenFilter() }
        val vm = CommentFilterViewModel(repository)

        vm.setInput("  Tennis ")
        vm.addWord()

        val sent = api.commentFilterWrites.single()
        assertThat(sent.filterUnkind).isTrue()
        assertThat(sent.words).containsExactly("ex", "cricket", "tennis").inOrder()
        assertThat(vm.state.value.words).containsExactly("ex", "cricket", "tennis").inOrder()
        assertThat(vm.state.value.input).isEmpty()
    }

    @Test
    fun `a word the server would refuse is said under the field and not sent`() = runTest {
        api.commentFilterResponse = { goldenFilter() }
        val vm = CommentFilterViewModel(repository)

        vm.setInput("a")
        vm.addWord()
        assertThat(vm.state.value.inputError).isEqualTo(CommentFilterCopy.TOO_SHORT)
        vm.setInput("x".repeat(CommentFilterRules.MAX_LEN + 1))
        vm.addWord()
        assertThat(vm.state.value.inputError).isEqualTo(CommentFilterCopy.TOO_LONG)
        vm.setInput("CRICKET")
        vm.addWord()
        assertThat(vm.state.value.inputError).isEqualTo(CommentFilterCopy.ALREADY)

        assertThat(api.commentFilterWrites).isEmpty()
    }

    @Test
    fun `fifty words is the most`() = runTest {
        val fifty = (1..CommentFilterRules.MAX_WORDS).map { "word$it" }
        api.commentFilterResponse = { ok(CommentFilterDto(filterUnkind = true, words = fifty)) }
        val vm = CommentFilterViewModel(repository)
        assertThat(vm.state.value.full).isTrue()

        vm.setInput("another")
        vm.addWord()

        assertThat(vm.state.value.inputError).isEqualTo(CommentFilterCopy.FULL)
        assertThat(api.commentFilterWrites).isEmpty()
    }

    @Test
    fun `the switch and a removal each send the whole filter`() = runTest {
        api.commentFilterResponse = { goldenFilter() }
        val vm = CommentFilterViewModel(repository)

        vm.setFilterUnkind(false)
        vm.removeWord("ex")

        assertThat(api.commentFilterWrites.map { it.filterUnkind }).containsExactly(false, false).inOrder()
        assertThat(api.commentFilterWrites.first().words).containsExactly("ex", "cricket").inOrder()
        assertThat(api.commentFilterWrites.last().words).containsExactly("cricket")
        assertThat(vm.state.value.filterUnkind).isFalse()
    }

    @Test
    fun `400 INVALID_COMMENT_FILTER is said and the list stays as the server holds it`() = runTest {
        api.commentFilterResponse = { goldenFilter() }
        api.commentFilterWriteResponse = { refusedWithFixture(400, "comment_filter_put_400_invalid.json") }
        val vm = CommentFilterViewModel(repository)

        vm.setInput("tennis")
        vm.addWord()

        assertThat(vm.state.value.inputError).isEqualTo(CommentFilterCopy.INVALID)
        assertThat(vm.state.value.words).containsExactly("ex", "cricket").inOrder()
        assertThat(vm.state.value.busy).isFalse()
    }

    @Test
    fun `the flag switched off since the read hides the section`() = runTest {
        api.commentFilterResponse = { goldenFilter() }
        api.commentFilterWriteResponse = { refusedWithFixture(404, "kind_check_post_404_not_enabled.json") }
        val vm = CommentFilterViewModel(repository)

        vm.setFilterUnkind(false)

        assertThat(vm.state.value.phase).isEqualTo(CommentFilterPhase.HIDDEN)
    }

    @Test
    fun `a failed first read offers a retry`() = runTest {
        api.commentFilterResponse = { offline() }
        val vm = CommentFilterViewModel(repository)
        assertThat(vm.state.value.phase).isEqualTo(CommentFilterPhase.FAILED)

        api.commentFilterResponse = { goldenFilter() }
        vm.refresh()
        assertThat(vm.state.value.phase).isEqualTo(CommentFilterPhase.READY)
    }

    // ── M16: hide from people I know ────────────────────────────────────────

    private fun goldenHideKnown() = ok(fixture("hide_known_get_200.json", HideKnownDto.serializer()))

    @Test
    fun `hide known is hidden while the server flag is off`() = runTest {
        val vm = HideKnownViewModel(repository)
        assertThat(vm.state.value.phase).isEqualTo(HideKnownPhase.HIDDEN)
    }

    @Test
    fun `on, it says how many connections it hides`() = runTest {
        api.hideKnownResponse = { goldenHideKnown() }
        val vm = HideKnownViewModel(repository)

        assertThat(vm.state.value.enabled).isTrue()
        assertThat(vm.state.value.hiddenCount).isEqualTo(2)
        assertThat(HideKnownCopy.hiddenFrom(1)).isEqualTo("Hidden from 1 connection")
        assertThat(HideKnownCopy.hiddenFrom(0)).isEqualTo(HideKnownCopy.NONE_YET)
    }

    @Test
    fun `turning it on and off sends enabled`() = runTest {
        api.hideKnownResponse = { ok(HideKnownDto()) }
        val vm = HideKnownViewModel(repository)
        assertThat(vm.state.value.enabled).isFalse()

        vm.setEnabled(true)
        assertThat(vm.state.value.enabled).isTrue()
        assertThat(vm.state.value.hiddenCount).isEqualTo(2)
        vm.setEnabled(false)
        assertThat(vm.state.value.enabled).isFalse()

        assertThat(api.hideKnownWrites.map { it.enabled }).containsExactly(true, false).inOrder()
    }

    @Test
    fun `503 HIDE_KNOWN_UNAVAILABLE leaves it off and offers a retry`() = runTest {
        api.hideKnownResponse = { ok(HideKnownDto()) }
        api.hideKnownWriteResponse = { refusedWithFixture(503, "hide_known_put_503_unavailable.json") }
        val vm = HideKnownViewModel(repository)

        vm.setEnabled(true)

        assertThat(vm.state.value.enabled).isFalse()
        assertThat(vm.state.value.unavailable).isTrue()
        assertThat(vm.state.value.busy).isFalse()
        assertThat(vm.state.value.message).isNull()

        api.hideKnownWriteResponse = { ok(fixture("hide_known_put_200.json", HideKnownDto.serializer())) }
        vm.retry()

        assertThat(vm.state.value.enabled).isTrue()
        assertThat(vm.state.value.unavailable).isFalse()
        assertThat(api.hideKnownWrites.map { it.enabled }).containsExactly(true, true)
    }

    @Test
    fun `the flag switched off since the read hides it`() = runTest {
        api.hideKnownResponse = { ok(HideKnownDto()) }
        api.hideKnownWriteResponse = { refusedWithFixture(404, "hide_known_get_404_not_enabled.json") }
        val vm = HideKnownViewModel(repository)

        vm.setEnabled(true)

        assertThat(vm.state.value.phase).isEqualTo(HideKnownPhase.HIDDEN)
    }

    // ── M18: screen protection ──────────────────────────────────────────────

    @Test
    fun `the client config is read once per Dating session`() = runTest {
        api.clientConfigResponse = { ok(fixture("client_config_get_200.json", ClientConfigDto.serializer())) }
        val config = ScreenProtectionConfig(repository, session)

        assertThat(config.enabled()).isTrue()
        assertThat(config.enabled()).isTrue()
        assertThat(api.calls.count { it == "client-config" }).isEqualTo(1)

        // Dating opened again: read again, and the server may have switched it off.
        session.entered()
        api.clientConfigResponse = { ok(fixture("client_config_get_200_off.json", ClientConfigDto.serializer())) }
        assertThat(config.enabled()).isFalse()
        assertThat(api.calls.count { it == "client-config" }).isEqualTo(2)
    }

    @Test
    fun `a failed config read means off, and is asked again`() = runTest {
        api.clientConfigResponse = { offline() }
        val config = ScreenProtectionConfig(repository, session)

        assertThat(config.enabled()).isFalse()
        api.clientConfigResponse = { ok(fixture("client_config_get_200.json", ClientConfigDto.serializer())) }
        assertThat(config.enabled()).isTrue()
        assertThat(api.calls.count { it == "client-config" }).isEqualTo(2)
    }

    @Test
    fun `sign-out forgets the config`() = runTest {
        api.clientConfigResponse = { ok(fixture("client_config_get_200.json", ClientConfigDto.serializer())) }
        val config = ScreenProtectionConfig(repository, session)
        assertThat(config.enabled()).isTrue()

        config.forget()
        api.clientConfigResponse = { ok(fixture("client_config_get_200_off.json", ClientConfigDto.serializer())) }
        assertThat(config.enabled()).isFalse()
    }

    /** A window that only knows its secure flag, and counts how often it was changed. */
    private class FakeWindow(var secure: Boolean = false) : SecureFlagTarget {
        var sets = 0
        var clears = 0
        override val isSecure: Boolean get() = secure
        override fun setSecure() {
            sets++
            secure = true
        }
        override fun clearSecure() {
            clears++
            secure = false
        }
    }

    @Test
    fun `nested Pulse screens hold the flag until the last one leaves`() {
        val window = FakeWindow()
        val count = SecureFlagCounter(window)

        val home = count.acquire()
        val person = count.acquire()
        assertThat(window.secure).isTrue()
        assertThat(window.sets).isEqualTo(1)

        person.release()
        assertThat(window.secure).isTrue()
        // Releasing twice is releasing once.
        person.release()
        assertThat(window.secure).isTrue()

        home.release()
        assertThat(window.secure).isFalse()
        assertThat(window.clears).isEqualTo(1)
    }

    @Test
    fun `a flag that was on before Pulse arrived is never cleared by Pulse`() {
        // Someone outside the count (an older chat-lock build, say) set it first.
        val window = FakeWindow(secure = true)
        val count = SecureFlagCounter(window)

        val match = count.acquire()
        match.release()

        assertThat(window.secure).isTrue()
        assertThat(window.clears).isEqualTo(0)
    }

    @Test
    fun `the chat lock arriving as a Pulse screen leaves keeps the flag on`() {
        val window = FakeWindow()
        val count = SecureFlagCounter(window)

        // The match screen holds it; the chat (with the lock on) composes before the match screen is disposed.
        val match = count.acquire()
        val chatLock = count.acquire()
        match.release()
        assertThat(window.secure).isTrue()

        // Back from the chat to the match: the match holds again before the chat lets go.
        val matchAgain = count.acquire()
        chatLock.release()
        assertThat(window.secure).isTrue()

        matchAgain.release()
        assertThat(window.secure).isFalse()
        assertThat(window.sets).isEqualTo(1)
    }

    @Test
    fun `a flag cleared under a holder is set again by the next one`() {
        val window = FakeWindow()
        val count = SecureFlagCounter(window)
        val first = count.acquire()
        // Something outside the count cleared it.
        window.secure = false

        val second = count.acquire()
        assertThat(window.secure).isTrue()
        first.release()
        second.release()
        assertThat(window.secure).isFalse()
    }

    // ── M12: saved pass dealbreakers without a pass ─────────────────────────

    private fun filters() = FiltersViewModel(repository, ProfileOptionsStore(repository), session)

    private fun noPass(dealbreakers: List<String>) = PreferencesDto(
        userId = ME,
        minAge = 21,
        maxAge = 35,
        distanceKm = 25,
        distanceBucket = "km_10_25",
        passFilters = PassFiltersDto(active = false, verifiedOnly = true, languages = listOf("en")),
        dealbreakers = dealbreakers,
    )

    @Test
    fun `without a pass the saved pass dealbreakers go along when another changes`() = runTest {
        api.preferences = noPass(listOf("verified", "languages"))
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.AGE, true)
        vm.save()

        assertThat(vm.state.value.upsell).isFalse()
        assertThat(api.preferenceWrites.single().dealbreakers).containsExactly("age", "verified", "languages").inOrder()
    }

    @Test
    fun `without a pass a saved one stays while a new one is still refused here`() = runTest {
        api.preferences = noPass(listOf("verified"))
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.LANGUAGES, true)

        assertThat(vm.state.value.upsell).isTrue()
        assertThat(vm.state.value.draft.dealbreakers).containsExactly(Dealbreaker.VERIFIED)
        vm.dismissUpsell()
        vm.setAges(25, 35)
        vm.save()
        // The kept one is not sent as a change at all.
        assertThat(api.preferenceWrites.single().dealbreakers).isNull()
    }

    @Test
    fun `with the filters flag off a saved pass dealbreaker is kept, not dropped`() = runTest {
        api.preferences = fixture("preferences_get_200_dealbreakers.json", PreferencesDto.serializer()).copy(dealbreakers = listOf("age", "languages"))
        val vm = filters()
        assertThat(vm.state.value.flagOn).isFalse()

        vm.setDealbreaker(Dealbreaker.DISTANCE, true)
        vm.save()

        assertThat(api.preferenceWrites.single().dealbreakers).containsExactly("age", "distance", "languages").inOrder()
    }
}
