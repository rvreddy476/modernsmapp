package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.CardUi
import com.us.android.feature.dating.home.FirstMoveActionsUi
import com.us.android.feature.dating.home.FirstMoveCopy
import com.us.android.feature.dating.home.HelloTarget
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.MatchDetailState
import com.us.android.feature.dating.home.MatchDetailViewModel
import com.us.android.feature.dating.home.MatchUi
import com.us.android.feature.dating.home.MatchesViewModel
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparkLimitUi
import com.us.android.feature.dating.home.toUi
import com.us.android.feature.dating.network.ExtendDto
import com.us.android.feature.dating.network.FirstMoveRequest
import com.us.android.feature.dating.network.FirstMoveSettingsDto
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.MatchFirstMoveDto
import com.us.android.feature.dating.network.OpeningAnswerRequest
import com.us.android.feature.dating.network.OpeningQuestionDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.privacy.FirstMovePhase
import com.us.android.feature.dating.privacy.FirstMoveSettingsViewModel
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.jsonObject
import org.junit.Rule
import org.junit.Test
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/**
 * Mechanic M5, "first move": the setting and its opening questions, how a
 * first-move match is shown to each side, the waiting person's answer, the
 * free extend, and the countdown's words.
 */
class FirstMoveTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = photoUrls()
    private val other = "user-other"
    private val kolkata = ZoneId.of("Asia/Kolkata")
    private val morning = Instant.parse("2026-10-02T10:00:00Z")

    // ── Settings ────────────────────────────────────────────────────────────

    private fun settings() = FirstMoveSettingsViewModel(repository)

    private fun settingsOn(dto: FirstMoveSettingsDto = fixture("first_move_get_200.json", FirstMoveSettingsDto.serializer())) {
        api.firstMoveStored = dto
        api.firstMoveResponse = { ok(api.firstMoveStored) }
    }

    @Test
    fun `the section is hidden while the server flag is off`() = runTest {
        // The fake's default is the golden 404 MECHANIC_NOT_ENABLED.
        val vm = settings()

        assertThat(vm.state.value.phase).isEqualTo(FirstMovePhase.HIDDEN)
        // Nothing can be written from a hidden section.
        vm.setEnabled(true)
        vm.saveQuestions()
        assertThat(api.firstMoveWrites).isEmpty()
    }

    @Test
    fun `settings load the switch, the questions and the server's limits`() = runTest {
        settingsOn()
        val state = settings().state.value

        assertThat(state.phase).isEqualTo(FirstMovePhase.READY)
        assertThat(state.enabled).isTrue()
        assertThat(state.saved).containsExactly("What does your perfect Sunday look like?", "Tea or coffee, and why?").inOrder()
        assertThat(state.drafts).isEqualTo(state.saved)
        assertThat(state.maxQuestions).isEqualTo(3)
        assertThat(state.maxLength).isEqualTo(140)
        assertThat(state.dirty).isFalse()
        assertThat(state.canAdd).isTrue()
    }

    @Test
    fun `absent limits fall back to three questions of 140 characters`() = runTest {
        settingsOn(FirstMoveSettingsDto())
        val state = settings().state.value

        assertThat(state.phase).isEqualTo(FirstMovePhase.READY)
        assertThat(state.enabled).isFalse()
        assertThat(state.drafts).isEmpty()
        assertThat(state.maxQuestions).isEqualTo(3)
        assertThat(state.maxLength).isEqualTo(140)
    }

    @Test
    fun `the switch sends only enabled and leaves the questions alone`() = runTest {
        settingsOn()
        val vm = settings()

        vm.setEnabled(false)

        val write = api.firstMoveWrites.single()
        assertThat(write).isEqualTo(FirstMoveRequest(enabled = false))
        // The body has no questions key, so the server keeps them.
        val body = NetworkModule.provideJson().encodeToJsonElement(FirstMoveRequest.serializer(), write).jsonObject
        assertThat(body.keys).containsExactly("enabled")
        assertThat(vm.state.value.enabled).isFalse()
        assertThat(vm.state.value.saved).hasSize(2)
        assertThat(vm.state.value.busy).isFalse()
    }

    @Test
    fun `saving questions sends the whole list, trimmed and without blanks`() = runTest {
        settingsOn()
        val vm = settings()

        vm.editQuestion(0, "  What are you reading?  ")
        vm.addQuestion()
        vm.editQuestion(2, "   ")
        assertThat(vm.state.value.dirty).isTrue()
        vm.saveQuestions()

        val write = api.firstMoveWrites.single()
        assertThat(write.enabled).isNull()
        assertThat(write.questions).containsExactly("What are you reading?", "Tea or coffee, and why?").inOrder()
        val state = vm.state.value
        assertThat(state.saved).containsExactly("What are you reading?", "Tea or coffee, and why?").inOrder()
        assertThat(state.drafts).isEqualTo(state.saved)
        assertThat(state.dirty).isFalse()
        assertThat(state.message?.type).isEqualTo(UsMessageType.Success)
    }

    @Test
    fun `removing every question sends an empty list`() = runTest {
        settingsOn()
        val vm = settings()

        vm.removeQuestion(1)
        vm.removeQuestion(0)
        vm.saveQuestions()

        val write = api.firstMoveWrites.single()
        assertThat(write.questions).isEmpty()
        val body = NetworkModule.provideJson().encodeToJsonElement(FirstMoveRequest.serializer(), write).jsonObject
        assertThat(body.keys).containsExactly("questions")
        assertThat(vm.state.value.saved).isEmpty()
    }

    @Test
    fun `a question is capped at the server's length and the editor stops at the maximum`() = runTest {
        settingsOn()
        val vm = settings()

        vm.editQuestion(0, "x".repeat(200))
        assertThat(vm.state.value.drafts[0]).hasLength(140)

        vm.addQuestion()
        assertThat(vm.state.value.drafts).hasSize(3)
        assertThat(vm.state.value.canAdd).isFalse()
        vm.addQuestion()
        assertThat(vm.state.value.drafts).hasSize(3)
    }

    @Test
    fun `a refused question shows under the editor and keeps the drafts`() = runTest {
        settingsOn()
        val vm = settings()
        vm.editQuestion(0, "Call me on 98480 22338")

        api.firstMoveWriteResponse = { refused(400, "OPENING_QUESTION_REFUSED") }
        vm.saveQuestions()
        assertThat(vm.state.value.error).isEqualTo("Questions can't include phone numbers, emails or links.")
        assertThat(vm.state.value.drafts[0]).isEqualTo("Call me on 98480 22338")
        assertThat(vm.state.value.message).isNull()
        assertThat(vm.state.value.busy).isFalse()

        api.firstMoveWriteResponse = { refusedWithFixture(400, "first_move_put_400_too_many_questions.json") }
        vm.saveQuestions()
        assertThat(vm.state.value.error).isEqualTo("You can have up to 3 opening questions.")

        api.firstMoveWriteResponse = { refused(400, "OPENING_QUESTION_INVALID", """{"max_length":140}""") }
        vm.saveQuestions()
        assertThat(vm.state.value.error).isEqualTo("Each question needs 1 to 140 characters.")

        // Editing clears the line.
        vm.editQuestion(0, "What are you reading?")
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `MECHANIC_NOT_ENABLED on a save hides the section`() = runTest {
        settingsOn()
        val vm = settings()

        api.firstMoveWriteResponse = { refusedWithFixture(404, "first_move_get_404_not_enabled.json") }
        vm.setEnabled(false)

        assertThat(vm.state.value.phase).isEqualTo(FirstMovePhase.HIDDEN)
    }

    @Test
    fun `a failed load offers a retry, then the section`() = runTest {
        api.firstMoveResponse = { offline() }
        val vm = settings()
        assertThat(vm.state.value.phase).isEqualTo(FirstMovePhase.FAILED)

        settingsOn()
        vm.refresh()
        assertThat(vm.state.value.phase).isEqualTo(FirstMovePhase.READY)
    }

    // ── A first-move match ──────────────────────────────────────────────────

    private val inFiveHours = "2026-10-02T15:00:00Z"

    private fun firstMoveMatch(
        youFirst: Boolean,
        deadline: String? = inFiveHours,
        canExtend: Boolean = !youFirst,
        questions: List<OpeningQuestionDto> = listOf(
            OpeningQuestionDto("q-1", "What does your perfect Sunday look like?"),
            OpeningQuestionDto("q-2", "Tea or coffee, and why?"),
        ),
    ): MatchDto = match("m-1", other, person(other, name = "Asha")).copy(
        firstMove = MatchFirstMoveDto(
            youMoveFirst = youFirst,
            deadline = deadline,
            openingQuestions = if (youFirst) emptyList() else questions,
            canExtend = canExtend,
        ),
    )

    private fun ordinaryMatch() = match("m-1", other, person(other, name = "Asha"))

    private fun detail(openChat: Boolean = false) = MatchDetailViewModel(
        SavedStateHandle(mapOf("matchId" to "m-1", "openChat" to openChat)),
        repository,
        session,
        safety,
        urls,
    )

    private fun loaded(vm: MatchDetailViewModel): MatchUi = (vm.state.value as MatchDetailState.Loaded).match

    private fun rows(vm: MatchesViewModel): List<MatchUi> = (vm.state.value as ListState.Items).items

    @Test
    fun `the list marks who starts and the time left`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val waiting = rows(MatchesViewModel(repository, session, urls)).single()
        assertThat(waiting.firstMove?.waiting).isTrue()
        assertThat(FirstMoveCopy.rowLabel(waiting.firstMove, morning)).isEqualTo("Waiting for them · 5h left")

        api.matches = listOf(firstMoveMatch(youFirst = true, deadline = "2026-10-03T12:00:00Z"))
        val yours = rows(MatchesViewModel(repository, session, urls)).single()
        assertThat(yours.firstMove?.youMoveFirst).isTrue()
        assertThat(FirstMoveCopy.rowLabel(yours.firstMove, morning)).isEqualTo("You start · 1d 2h left")

        api.matches = listOf(ordinaryMatch())
        val plain = rows(MatchesViewModel(repository, session, urls)).single()
        assertThat(plain.firstMove).isNull()
        assertThat(FirstMoveCopy.rowLabel(plain.firstMove, morning)).isNull()
    }

    @Test
    fun `a match without a deadline is marked without a countdown`() {
        val move = checkNotNull(firstMoveMatch(youFirst = true, deadline = null).firstMove.toUi())
        assertThat(move.deadline).isNull()
        assertThat(FirstMoveCopy.rowLabel(move, morning)).isEqualTo("You start")
        // Blank questions are dropped rather than drawn as empty rows.
        val waiting = firstMoveMatch(youFirst = false, questions = listOf(OpeningQuestionDto("q-1", " "), OpeningQuestionDto("", "No id")))
        assertThat(checkNotNull(waiting.firstMove.toUi()).questions).isEmpty()
    }

    @Test
    fun `the first mover opens the chat as usual`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = true, canExtend = true))
        val vm = detail()

        val move = checkNotNull(loaded(vm).firstMove)
        assertThat(move.youMoveFirst).isTrue()
        assertThat(move.deadline).isEqualTo(Instant.parse(inFiveHours))
        // Nothing to answer and no extend for the person who writes first.
        assertThat(move.questions).isEmpty()
        assertThat(move.canExtend).isFalse()
        vm.extend()
        assertThat(api.extends).isEmpty()

        vm.openChat()
        assertThat(vm.chat.value?.conversationId).isEqualTo("conv-m-1")
    }

    @Test
    fun `the waiting person is not sent into the chat`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail(openChat = true)

        val move = checkNotNull(loaded(vm).firstMove)
        assertThat(move.waiting).isTrue()
        assertThat(move.questions.map { it.id }).containsExactly("q-1", "q-2").inOrder()
        assertThat(move.canExtend).isTrue()
        // Neither the push's openChat nor the button opens a chat that would refuse them.
        assertThat(vm.chat.value).isNull()
        vm.openChat()
        assertThat(vm.chat.value).isNull()
        assertThat(vm.message.value?.type).isEqualTo(UsMessageType.Info)
    }

    @Test
    fun `an answer is sent and the match becomes an ordinary one`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        vm.startAnswer("q-1")
        vm.editAnswer("  Dosa, a bookshop and no alarm.  ")
        vm.sendAnswer()

        assertThat(api.openingAnswers.single()).isEqualTo("m-1" to OpeningAnswerRequest("q-1", "Dosa, a bookshop and no alarm."))
        assertThat(vm.message.value?.text).isEqualTo(FirstMoveCopy.ANSWER_SENT)
        assertThat(vm.firstMove.value).isEqualTo(FirstMoveActionsUi())
        // The reload still lists the rule (the server is catching up): the app
        // does not put the questions back, and the chat is the way in now.
        assertThat(api.calls.count { it == "opening-answer" }).isEqualTo(1)
        assertThat(loaded(vm).firstMove).isNull()
        vm.openChat()
        assertThat(vm.chat.value?.conversationId).isEqualTo("conv-m-1")
    }

    @Test
    fun `an empty answer is not sent and an answer is capped at 500`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        vm.sendAnswer()
        assertThat(api.openingAnswers).isEmpty()

        vm.startAnswer("q-1")
        vm.editAnswer("   ")
        vm.sendAnswer()
        assertThat(vm.firstMove.value.answerError).isEqualTo(FirstMoveCopy.ANSWER_EMPTY)
        assertThat(api.openingAnswers).isEmpty()

        vm.editAnswer("y".repeat(700))
        assertThat(vm.firstMove.value.answer).hasLength(500)
        assertThat(vm.firstMove.value.answerError).isNull()
    }

    @Test
    fun `switching questions starts a fresh answer and cancel closes it`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        vm.startAnswer("q-1")
        vm.editAnswer("Coffee")
        vm.startAnswer("q-1")
        assertThat(vm.firstMove.value.answer).isEqualTo("Coffee")
        vm.startAnswer("q-2")
        assertThat(vm.firstMove.value).isEqualTo(FirstMoveActionsUi(answeringId = "q-2"))
        vm.cancelAnswer()
        assertThat(vm.firstMove.value.answeringId).isNull()
    }

    @Test
    fun `a refused answer keeps the draft and says why under the field`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()
        vm.startAnswer("q-1")
        vm.editAnswer("Mail me at a@b.co")

        api.openingAnswerResponse = { _, _ -> refused(400, "OPENING_ANSWER_REFUSED") }
        vm.sendAnswer()
        val refusedState = vm.firstMove.value
        assertThat(refusedState.answerError).isEqualTo("Answers can't include phone numbers, emails or links.")
        assertThat(refusedState.answer).isEqualTo("Mail me at a@b.co")
        assertThat(refusedState.sending).isFalse()
        assertThat(refusedState.answeringId).isEqualTo("q-1")

        api.openingAnswerResponse = { _, _ -> refused(400, "OPENING_ANSWER_INVALID", """{"max_length":500}""") }
        vm.sendAnswer()
        assertThat(vm.firstMove.value.answerError).isEqualTo("Your answer needs 1 to 500 characters.")
        // Still waiting: the questions stay.
        assertThat(loaded(vm).firstMove?.waiting).isTrue()
    }

    @Test
    fun `FIRST_MOVE_NOT_PENDING reads the match again`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()
        vm.startAnswer("q-1")
        vm.editAnswer("Tea")

        api.openingAnswerResponse = { _, _ ->
            // They wrote first in the meantime.
            api.matches = listOf(ordinaryMatch().copy(status = "conversing"))
            refusedWithFixture(409, "match_opening_answer_409_not_pending.json")
        }
        vm.sendAnswer()

        assertThat(vm.message.value?.text).isEqualTo("This match isn't waiting for an answer from you any more.")
        assertThat(vm.firstMove.value.answeringId).isNull()
        assertThat(loaded(vm).firstMove).isNull()
        assertThat(loaded(vm).status).isEqualTo("conversing")
    }

    @Test
    fun `an unknown question reads the match again`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()
        vm.startAnswer("q-2")
        vm.editAnswer("Tea")

        api.openingAnswerResponse = { _, _ ->
            api.matches = listOf(firstMoveMatch(youFirst = false, questions = listOf(OpeningQuestionDto("q-1", "Only this one"))))
            refused(404, "OPENING_QUESTION_UNKNOWN")
        }
        vm.sendAnswer()

        assertThat(vm.message.value?.text).isEqualTo("That question isn't there any more.")
        assertThat(loaded(vm).firstMove?.questions?.map { it.id }).containsExactly("q-1")
    }

    @Test
    fun `chat being unavailable keeps the draft for another try`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()
        vm.startAnswer("q-1")
        vm.editAnswer("Tea")

        api.openingAnswerResponse = { _, _ -> refused(503, "CHAT_UNAVAILABLE") }
        vm.sendAnswer()

        assertThat(vm.message.value?.text).isEqualTo("Chat isn't reachable right now. Try again in a moment.")
        assertThat(vm.firstMove.value.answer).isEqualTo("Tea")
        assertThat(vm.firstMove.value.sending).isFalse()
        assertThat(loaded(vm).firstMove?.waiting).isTrue()
    }

    @Test
    fun `a match that is gone ends the screen`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()
        vm.startAnswer("q-1")
        vm.editAnswer("Tea")

        api.openingAnswerResponse = { _, _ -> refused(404, "NOT_FOUND") }
        vm.sendAnswer()

        assertThat(vm.state.value).isEqualTo(MatchDetailState.Gone("This match isn't available any more."))
    }

    @Test
    fun `the free extend gives them 24 more hours`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        api.extendResponse = {
            api.matches = listOf(firstMoveMatch(youFirst = false, deadline = "2026-10-03T15:00:00Z", canExtend = false))
            ok(ExtendDto(extended = true, extraHours = 24, expiresAt = "2026-10-03T15:00:00Z", free = true))
        }
        vm.extend()

        assertThat(api.extends).containsExactly("m-1")
        assertThat(vm.message.value?.text).isEqualTo("Done. They have 24 more hours.")
        val move = checkNotNull(loaded(vm).firstMove)
        assertThat(move.canExtend).isFalse()
        assertThat(move.deadline).isEqualTo(Instant.parse("2026-10-03T15:00:00Z"))
        assertThat(vm.firstMove.value.extending).isFalse()

        // Not offered again: no second call.
        vm.extend()
        assertThat(api.extends).hasSize(1)
    }

    @Test
    fun `the golden free extend is understood`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        vm.extend()

        // The golden redacts expires_at: the deadline the app had stays.
        assertThat(vm.message.value?.text).isEqualTo("Done. They have 24 more hours.")
        assertThat(vm.message.value?.type).isEqualTo(UsMessageType.Success)
    }

    @Test
    fun `a spent free extend shows when it comes back`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        api.extendResponse = { refused(429, "EXTEND_LIMIT_REACHED", """{"limit":1,"window_hours":24,"resets_at":"2026-10-02T13:30:00Z"}""") }
        vm.extend()

        val limit = checkNotNull(vm.firstMove.value.extendLimit)
        assertThat(limit).isEqualTo(SparkLimitUi(limit = 1, windowHours = 24, resetsAt = Instant.parse("2026-10-02T13:30:00Z")))
        assertThat(loaded(vm).firstMove?.canExtend).isFalse()
        assertThat(vm.firstMove.value.extending).isFalse()
        assertThat(FirstMoveCopy.extendSpent(limit, morning, kolkata))
            .isEqualTo("You've given extra time once today. You can do it again from 7:00 PM today.")
    }

    @Test
    fun `the golden extend limit falls back to its window`() = runTest {
        api.matches = listOf(firstMoveMatch(youFirst = false))
        val vm = detail()

        api.extendResponse = { refusedWithFixture(429, "match_extend_429_limit_reached.json") }
        vm.extend()

        val limit = checkNotNull(vm.firstMove.value.extendLimit)
        assertThat(limit.resetsAt).isNull()
        assertThat(FirstMoveCopy.extendSpent(limit, morning, kolkata))
            .isEqualTo("You've given extra time once today. You can again within 24 hours.")
        assertThat(FirstMoveCopy.extendSpent(null, morning, kolkata))
            .isEqualTo("You've given extra time once today. You can do it again tomorrow.")
    }

    @Test
    fun `a premium extend reads in days`() {
        assertThat(FirstMoveCopy.extended(free = false, extraHours = 168, extraDays = 7)).isEqualTo("Done. Your match has 7 more days.")
        assertThat(FirstMoveCopy.extended(free = false, extraHours = 0, extraDays = 0)).isEqualTo("Done. Your match has more time.")
    }

    @Test
    fun `say hello on a match where they start leads to the match, not the chat`() = runTest {
        api.pulse = listOf(card(other))
        api.matches = listOf(firstMoveMatch(youFirst = false))
        api.sparkResponse = { ok(SparkCreatedDto(matched = true, matchId = "m-1")) }
        val pulse = PulseViewModel(repository, session, safety, urls)

        pulse.spark(other)
        assertThat(pulse.celebration.value?.waitingForThem).isTrue()
        pulse.sayHello()

        assertThat(pulse.hello.value).isEqualTo(HelloTarget.Match("m-1"))
        // The deck is untouched by any of this.
        assertThat((pulse.state.value as ListState.Items<CardUi>).items).isEmpty()
    }

    @Test
    fun `say hello on a match where you start opens the chat`() = runTest {
        api.pulse = listOf(card(other))
        api.matches = listOf(firstMoveMatch(youFirst = true))
        api.sparkResponse = { ok(SparkCreatedDto(matched = true, matchId = "m-1")) }
        val pulse = PulseViewModel(repository, session, safety, urls)

        pulse.spark(other)
        pulse.sayHello()

        assertThat(pulse.hello.value).isEqualTo(HelloTarget.Chat("conv-m-1", "Asha"))
    }

    // ── The countdown ───────────────────────────────────────────────────────

    @Test
    fun `the countdown reads in days, hours and minutes`() {
        fun left(d: Duration) = FirstMoveCopy.timeLeft(morning.plus(d), morning)

        assertThat(FirstMoveCopy.timeLeft(null, morning)).isNull()
        assertThat(left(Duration.ofMinutes(-1))).isEqualTo("Time's up")
        assertThat(left(Duration.ZERO)).isEqualTo("Time's up")
        assertThat(left(Duration.ofSeconds(30))).isEqualTo("Under a minute left")
        assertThat(left(Duration.ofMinutes(12))).isEqualTo("12m left")
        assertThat(left(Duration.ofHours(5))).isEqualTo("5h left")
        assertThat(left(Duration.ofHours(5).plusMinutes(12))).isEqualTo("5h 12m left")
        assertThat(left(Duration.ofHours(23).plusMinutes(59))).isEqualTo("23h 59m left")
        assertThat(left(Duration.ofHours(24))).isEqualTo("1d left")
        assertThat(left(Duration.ofHours(27).plusMinutes(30))).isEqualTo("1d 3h left")
    }
}
