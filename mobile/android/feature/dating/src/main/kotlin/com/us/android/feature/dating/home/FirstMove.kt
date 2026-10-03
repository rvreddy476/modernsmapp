package com.us.android.feature.dating.home

import com.us.android.feature.dating.network.MatchFirstMoveDto
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/*
 * Mechanic M5, "first move", on a match.
 *
 * A match carries `first_move` only while it waits for its first message under
 * the rule. Who writes first is the SERVER's answer (`you_move_first`); the app
 * never works it out from the opt-in setting.
 */

/** One opening question the first mover wrote, in display terms. */
data class OpeningQuestionUi(val id: String, val text: String)

/**
 * A pending first-move match, as the viewer sees it.
 *
 * [youMoveFirst]: the viewer writes first, through the chat as usual.
 * Otherwise the other person does, chat refuses the viewer's own first
 * message, and the viewer may answer one of [questions] or, while
 * [canExtend], give the other person more time for free.
 */
data class FirstMoveUi(
    val youMoveFirst: Boolean,
    /** When the match ends if nobody writes. Null when the server sent none. */
    val deadline: Instant?,
    /** Always empty for the person who writes first. */
    val questions: List<OpeningQuestionUi> = emptyList(),
    val canExtend: Boolean = false,
) {
    /** The viewer is the one waiting: the match screen offers answers, not the chat. */
    val waiting: Boolean get() = !youMoveFirst
}

/** The server's `first_move`, or null for an ordinary match. Blank questions are dropped. */
internal fun MatchFirstMoveDto?.toUi(): FirstMoveUi? {
    val dto = this ?: return null
    return FirstMoveUi(
        youMoveFirst = dto.youMoveFirst,
        deadline = parseInstant(dto.deadline),
        questions = if (dto.youMoveFirst) {
            emptyList()
        } else {
            dto.openingQuestions
                .filter { it.id.isNotBlank() && it.text.isNotBlank() }
                .map { OpeningQuestionUi(it.id, it.text.trim()) }
        },
        // Only the waiting person has the free extend.
        canExtend = !dto.youMoveFirst && dto.canExtend,
    )
}

/** The answer composer and the extend action on the match screen. */
data class FirstMoveActionsUi(
    /** The question being answered, or null while no composer is open. */
    val answeringId: String? = null,
    val answer: String = "",
    val sending: Boolean = false,
    /** A refusal that belongs under the answer field. */
    val answerError: String? = null,
    val extending: Boolean = false,
    /** Set by `EXTEND_LIMIT_REACHED`: the free extend comes back at its reset time. */
    val extendLimit: SparkLimitUi? = null,
)

/** First move's own words. Pure, so the tests can read them. */
object FirstMoveCopy {

    const val SETTING_TITLE = "I'll send the first message"
    const val SETTING_BODY =
        "When you match, the chat opens with you. They wait for your message, and can answer one of your questions while they do."
    const val QUESTIONS_TITLE = "Your opening questions"
    const val QUESTIONS_BODY =
        "Optional. Someone waiting on you can answer one, and their answer starts your chat."

    const val YOU_START_TAG = "You start"
    const val WAITING_TAG = "Waiting for them"

    const val YOU_START_TITLE = "You start this one"
    const val YOU_START_BODY = "They're waiting to hear from you. Send the first message before the time runs out, or the match ends."

    const val WAITING_TITLE = "They start this one"
    const val WAITING_NO_QUESTIONS = "They haven't added any questions. Their first message will arrive in your chat."
    const val EXTEND = "Give them 24 more hours"
    const val ANSWER = "Answer"
    const val ANSWER_SENT = "Answer sent. It's the first message in your chat now."
    const val ANSWER_EMPTY = "Write an answer first."
    const val MAX_ANSWER_LENGTH = 500

    fun waitingBody(name: String?): String {
        val who = name?.takeIf { it.isNotBlank() } ?: "Your match"
        return "$who sends the first message here. While you wait, answer one of their questions and your answer opens the chat."
    }

    /** The extend confirmation: the free 24 hours, or the premium days. */
    fun extended(free: Boolean, extraHours: Int, extraDays: Int): String = when {
        free || (extraDays <= 0 && extraHours > 0) -> "Done. They have ${count(extraHours.takeIf { it > 0 } ?: HOURS_PER_DAY, "more hour", "more hours")}."
        extraDays > 0 -> "Done. Your match has ${count(extraDays, "more day", "more days")}."
        else -> "Done. Your match has more time."
    }

    /** Where the free extend stands once it is spent. */
    fun extendSpent(limit: SparkLimitUi?, now: Instant, zone: ZoneId): String {
        val back = limit?.resetsAt?.let { DeckCopy.whenLabel(it, now, zone) }
        return when {
            back != null -> "You've given extra time once today. You can do it again from $back."
            (limit?.windowHours ?: 0) > 0 -> "You've given extra time once today. You can again within ${limit?.windowHours} hours."
            else -> "You've given extra time once today. You can do it again tomorrow."
        }
    }

    /** "You start · 5h 12m left" for a match row, or null for an ordinary match. */
    fun rowLabel(firstMove: FirstMoveUi?, now: Instant): String? {
        val move = firstMove ?: return null
        val tag = if (move.youMoveFirst) YOU_START_TAG else WAITING_TAG
        return listOfNotNull(tag, timeLeft(move.deadline, now)).joinToString(" · ")
    }

    /**
     * The countdown to [deadline]: "1d 3h left", "5h 12m left", "5h left",
     * "12m left", "Under a minute left", or "Time's up" once it has passed.
     * Null when the server sent no deadline.
     */
    fun timeLeft(deadline: Instant?, now: Instant): String? {
        val end = deadline ?: return null
        val left = Duration.between(now, end)
        if (left.isNegative || left.isZero) return "Time's up"
        val days = left.toDays()
        val hours = left.toHours() % HOURS_PER_DAY
        val minutes = left.toMinutes() % MINUTES_PER_HOUR
        return when {
            days > 0 -> if (hours > 0) "${days}d ${hours}h left" else "${days}d left"
            left.toHours() > 0 -> if (minutes > 0) "${left.toHours()}h ${minutes}m left" else "${left.toHours()}h left"
            minutes > 0 -> "${minutes}m left"
            else -> "Under a minute left"
        }
    }

    private fun count(n: Int, one: String, many: String): String = if (n == 1) "1 $one" else "$n $many"

    private const val HOURS_PER_DAY = 24
    private const val MINUTES_PER_HOUR = 60
}
