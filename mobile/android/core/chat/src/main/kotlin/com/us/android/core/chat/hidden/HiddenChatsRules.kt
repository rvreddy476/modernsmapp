package com.us.android.core.chat.hidden

/*
 * Hidden chats (contract pinned 24 Oct 2026): what a secret key and a chat
 * password may be, and the words the screens say. The server checks the same
 * rules; these only stop a form from sending something it would refuse.
 *
 *   Secret key     4–12 letters or digits (A–Z, a–z, 0–9), typed into search.
 *   Chat password  6–64 characters, and not the same as the secret key.
 *
 * Pure: no Android, no I/O, so every rule is a JVM unit test.
 */

const val SECRET_KEY_MIN = 4
const val SECRET_KEY_MAX = 12
const val CHAT_PASSWORD_MIN = 6
const val CHAT_PASSWORD_MAX = 64

/** The contract's own words (24 Oct 2026), shown wherever hidden chats are set up. */
const val HIDDEN_CHATS_COPY =
    "Hidden chats protect your chats from people using your phone or computer. " +
        "They don't change how messages are encrypted."

const val STRICT_MODE_WARNING =
    "With strict mode on, resetting a forgotten secret key or chat password deletes your hidden chats " +
        "for you instead of keeping them. The other people in those chats keep their messages. " +
        "This cannot be undone."

/** ASCII letters and digits only — `Char.isLetterOrDigit` would also pass "é" and "٣". */
private fun Char.isKeyChar(): Boolean = this in 'A'..'Z' || this in 'a'..'z' || this in '0'..'9'

/** Characters as a person counts them: a surrogate pair (an emoji) is one, not two. */
fun charCount(value: String): Int = value.codePointCount(0, value.length)

fun secretKeyProblem(key: String): String? = when {
    key.isEmpty() -> "Enter a secret key."
    !key.all { it.isKeyChar() } -> "Use letters and numbers only, with no spaces."
    key.length < SECRET_KEY_MIN -> "Use at least $SECRET_KEY_MIN letters or numbers."
    key.length > SECRET_KEY_MAX -> "Use at most $SECRET_KEY_MAX letters or numbers."
    else -> null
}

fun chatPasswordProblem(password: String): String? {
    if (password.isEmpty()) return "Enter a chat password."
    val n = charCount(password)
    return when {
        n < CHAT_PASSWORD_MIN -> "Use at least $CHAT_PASSWORD_MIN characters."
        n > CHAT_PASSWORD_MAX -> "Use at most $CHAT_PASSWORD_MAX characters."
        else -> null
    }
}

/**
 * Whether text submitted from chat search could be a secret key — only then
 * does the search box also try it, quietly. Surrounding spaces are not part
 * of a key. Anything else is never sent: a wrong key counts toward the
 * account's lockout even when nothing is set up.
 */
fun couldBeSecretKey(text: String): Boolean {
    val t = text.trim()
    return t.length in SECRET_KEY_MIN..SECRET_KEY_MAX && t.all { it.isKeyChar() }
}

/** A form's problems, by field; empty means it may be sent. */
data class SecretsProblems(
    val secretKey: String? = null,
    val chatPassword: String? = null,
    val confirmPassword: String? = null,
    val form: String? = null,
) {
    val none: Boolean get() = secretKey == null && chatPassword == null && confirmPassword == null && form == null
}

const val SECRETS_MUST_DIFFER_TEXT = "Your chat password must be different from your secret key."

/** First-time setup and reset: both secrets, the password confirmed, and the two different. */
fun setupProblems(secretKey: String, chatPassword: String, confirmPassword: String): SecretsProblems {
    val key = secretKeyProblem(secretKey)
    val pw = chatPasswordProblem(chatPassword)
    val confirm = if (pw == null && confirmPassword != chatPassword) "The passwords don't match." else null
    val differ = if (key == null && pw == null && secretKey == chatPassword) SECRETS_MUST_DIFFER_TEXT else null
    return SecretsProblems(secretKey = key, chatPassword = pw ?: differ, confirmPassword = confirm)
}

/**
 * Change: the current chat password, and at least one new secret. An empty
 * new value means "keep the current one".
 */
fun changeProblems(
    currentPassword: String,
    newSecretKey: String,
    newPassword: String,
    confirmPassword: String,
): SecretsProblems {
    val form = when {
        currentPassword.isEmpty() -> "Enter your current chat password."
        newSecretKey.isEmpty() && newPassword.isEmpty() -> "Enter a new secret key, a new chat password, or both."
        else -> null
    }
    var key = if (newSecretKey.isNotEmpty()) secretKeyProblem(newSecretKey) else null
    var pw = if (newPassword.isNotEmpty()) chatPasswordProblem(newPassword) else null
    val confirm = if (newPassword.isNotEmpty() && pw == null && confirmPassword != newPassword) {
        "The passwords don't match."
    } else {
        null
    }
    if (key == null && pw == null && newSecretKey.isNotEmpty() && newSecretKey == newPassword) {
        pw = SECRETS_MUST_DIFFER_TEXT
    }
    // Only the key changes: it must not equal the password that stays.
    if (key == null && newSecretKey.isNotEmpty() && newPassword.isEmpty() && newSecretKey == currentPassword) {
        key = "Your secret key must be different from your chat password."
    }
    return SecretsProblems(secretKey = key, chatPassword = pw, confirmPassword = confirm, form = form)
}

/** "15 minutes", "1 hour", "24 hours", "40 seconds". */
@Suppress("MagicNumber") // Seconds per minute and minutes per hour.
fun waitWords(seconds: Long): String {
    if (seconds < 60) return if (seconds <= 1) "1 second" else "$seconds seconds"
    val minutes = (seconds + 59) / 60
    if (minutes < 60) return if (minutes == 1L) "1 minute" else "$minutes minutes"
    val hours = (minutes + 59) / 60
    return if (hours == 1L) "1 hour" else "$hours hours"
}

fun lockedMessage(retryAfterSec: Long?): String =
    if (retryAfterSec == null || retryAfterSec <= 0) {
        "Too many wrong tries. Try again later."
    } else {
        "Too many wrong tries. Try again in ${waitWords(retryAfterSec)}."
    }

fun wrongPasswordMessage(attemptsLeft: Int?): String = when {
    attemptsLeft == null -> "That password isn't right."
    attemptsLeft <= 0 -> "That password isn't right. You're out of tries for now."
    attemptsLeft == 1 -> "That password isn't right. 1 try left."
    else -> "That password isn't right. $attemptsLeft tries left."
}

/** What a settings form shows for a failed save. */
fun settingsErrorMessage(error: HiddenError): String = when (error) {
    is HiddenError.WrongPassword -> wrongPasswordMessage(error.attemptsLeft)
    is HiddenError.Locked -> lockedMessage(error.retryAfterSec)
    HiddenError.SecretsMustDiffer -> SECRETS_MUST_DIFFER_TEXT
    HiddenError.StepUpRequired -> "Confirm it's you again, then try once more."
    HiddenError.InvalidSecretKey -> secretKeyProblem("") ?: "Check your secret key."
    HiddenError.InvalidChatPassword -> "Check your chat password: 6 to 64 characters."
    is HiddenError.Invalid -> error.message ?: "Those don't meet the rules. Check them and try again."
    HiddenError.NotConfigured -> "Set up hidden chats first."
    HiddenError.Unavailable, HiddenError.NotFound -> "Hidden chats aren't available right now. Try again later."
    HiddenError.ChatLocked -> "Open the chat with your chat password first."
    HiddenError.Network -> "You're offline. Check your connection and try again."
    is HiddenError.Unknown -> "Couldn't save. Try again."
}

/** What the chat-password prompt shows when opening failed. */
fun openErrorMessage(error: HiddenError): String = when (error) {
    is HiddenError.WrongPassword -> wrongPasswordMessage(error.attemptsLeft)
    is HiddenError.Locked -> lockedMessage(error.retryAfterSec)
    HiddenError.NotFound -> "Hidden chats locked again. Enter your secret key first."
    HiddenError.Network -> "You're offline. Check your connection and try again."
    else -> "Couldn't check your chat password. Try again."
}
