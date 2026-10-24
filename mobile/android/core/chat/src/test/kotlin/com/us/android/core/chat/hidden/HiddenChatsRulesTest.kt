package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** What a secret key and a chat password may be (contract 24 Oct 2026), and the words for a refusal. */
class HiddenChatsRulesTest {

    @Test
    fun `a secret key is 4 to 12 ASCII letters or digits`() {
        assertThat(secretKeyProblem("abc1")).isNull()
        assertThat(secretKeyProblem("ABCdef123456")).isNull()
        assertThat(secretKeyProblem("")).isEqualTo("Enter a secret key.")
        assertThat(secretKeyProblem("ab1")).isEqualTo("Use at least 4 letters or numbers.")
        assertThat(secretKeyProblem("abcdefghij123")).isEqualTo("Use at most 12 letters or numbers.")
        assertThat(secretKeyProblem("ab cd")).isEqualTo("Use letters and numbers only, with no spaces.")
        assertThat(secretKeyProblem("abc-1")).isEqualTo("Use letters and numbers only, with no spaces.")
    }

    @Test
    fun `a letter outside ASCII is not a key character`() {
        // Char.isLetterOrDigit would pass these; the server would not.
        assertThat(secretKeyProblem("caféx")).isNotNull()
        assertThat(secretKeyProblem("abc٣")).isNotNull()
        assertThat(couldBeSecretKey("caféx")).isFalse()
    }

    @Test
    fun `a chat password is 6 to 64 characters counted as a person counts them`() {
        assertThat(chatPasswordProblem("123456")).isNull()
        assertThat(chatPasswordProblem("")).isEqualTo("Enter a chat password.")
        assertThat(chatPasswordProblem("12345")).isEqualTo("Use at least 6 characters.")
        assertThat(chatPasswordProblem("a".repeat(64))).isNull()
        assertThat(chatPasswordProblem("a".repeat(65))).isEqualTo("Use at most 64 characters.")
        // Five emoji are ten UTF-16 units but five characters: too short.
        assertThat(chatPasswordProblem("😀😀😀😀😀")).isEqualTo("Use at least 6 characters.")
        assertThat(chatPasswordProblem("😀".repeat(64))).isNull()
        // Spaces are allowed in a password.
        assertThat(chatPasswordProblem("a b c d")).isNull()
    }

    @Test
    fun `setup needs both secrets, the password twice, and the two different`() {
        assertThat(setupProblems("abcd12", "secret-pass", "secret-pass").none).isTrue()
        assertThat(setupProblems("abcd12", "secret-pass", "other").confirmPassword).isEqualTo("The passwords don't match.")
        val same = setupProblems("abcd1234", "abcd1234", "abcd1234")
        assertThat(same.chatPassword).isEqualTo(SECRETS_MUST_DIFFER_TEXT)
        assertThat(same.none).isFalse()
        val bad = setupProblems("ab", "123", "123")
        assertThat(bad.secretKey).isNotNull()
        assertThat(bad.chatPassword).isNotNull()
    }

    @Test
    fun `change needs the current password and at least one new secret`() {
        assertThat(changeProblems("", "abcd", "", "").form).isEqualTo("Enter your current chat password.")
        assertThat(changeProblems("current1", "", "", "").form)
            .isEqualTo("Enter a new secret key, a new chat password, or both.")
        assertThat(changeProblems("current1", "abcd", "", "").none).isTrue()
        assertThat(changeProblems("current1", "", "newpass1", "newpass1").none).isTrue()
        assertThat(changeProblems("current1", "", "newpass1", "nope").confirmPassword).isNotNull()
        // A new key equal to the new password, or to the password that stays.
        assertThat(changeProblems("current1", "abcd1234", "abcd1234", "abcd1234").chatPassword)
            .isEqualTo(SECRETS_MUST_DIFFER_TEXT)
        assertThat(changeProblems("abcd1234", "abcd1234", "", "").secretKey)
            .isEqualTo("Your secret key must be different from your chat password.")
    }

    @Test
    fun `only key-shaped text from search is ever tried as a key`() {
        assertThat(couldBeSecretKey("abcd")).isTrue()
        assertThat(couldBeSecretKey("  abcd12  ")).isTrue()
        assertThat(couldBeSecretKey("abc")).isFalse()
        assertThat(couldBeSecretKey("abcdefghijklm")).isFalse()
        assertThat(couldBeSecretKey("hello world")).isFalse()
        assertThat(couldBeSecretKey("ana@x")).isFalse()
        assertThat(couldBeSecretKey("")).isFalse()
    }

    @Test
    fun `lockout and wrong-password words`() {
        assertThat(waitWords(1)).isEqualTo("1 second")
        assertThat(waitWords(40)).isEqualTo("40 seconds")
        assertThat(waitWords(60)).isEqualTo("1 minute")
        assertThat(waitWords(900)).isEqualTo("15 minutes")
        assertThat(waitWords(3600)).isEqualTo("1 hour")
        assertThat(waitWords(86_400)).isEqualTo("24 hours")
        assertThat(lockedMessage(900)).isEqualTo("Too many wrong tries. Try again in 15 minutes.")
        assertThat(lockedMessage(null)).isEqualTo("Too many wrong tries. Try again later.")
        assertThat(wrongPasswordMessage(3)).isEqualTo("That password isn't right. 3 tries left.")
        assertThat(wrongPasswordMessage(1)).isEqualTo("That password isn't right. 1 try left.")
        assertThat(wrongPasswordMessage(0)).isEqualTo("That password isn't right. You're out of tries for now.")
        assertThat(wrongPasswordMessage(null)).isEqualTo("That password isn't right.")
    }

    @Test
    fun `the settings page says the contract's own copy`() {
        assertThat(HIDDEN_CHATS_COPY).isEqualTo(
            "Hidden chats protect your chats from people using your phone or computer. " +
                "They don't change how messages are encrypted.",
        )
    }
}
