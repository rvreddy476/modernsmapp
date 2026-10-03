package com.us.android.feature.dating.safety

import android.content.Context
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The "Did this bother you?" answers given on this device (mechanic M13), by
 * message id, so reopening a Pulse chat — or the app — neither asks again nor
 * records a second answer. Kept for the signed-in account and dropped at
 * sign-out ([DatingTeardown]). A port so the kindness checks test on the JVM.
 */
interface KindAnswerStore {
    /** The answer given about [messageId]: true bothered, false not; null when never answered. */
    suspend fun answer(messageId: String): Boolean?

    suspend fun remember(messageId: String, bothered: Boolean)

    /** Sign-out: nothing answered by one account is kept for the next. */
    suspend fun clear()
}

/**
 * [KindAnswerStore] in a private SharedPreferences file. Only opaque message
 * ids and a yes/no are stored — never a text. Bounded: past [MAX_ANSWERS] the
 * oldest answers go first.
 */
@Singleton
class PrefsKindAnswerStore @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : KindAnswerStore {

    private val prefs by lazy { context.getSharedPreferences(FILE, Context.MODE_PRIVATE) }

    override suspend fun answer(messageId: String): Boolean? = withContext(io) {
        prefs.getString(messageId, null)?.let(::decode)
    }

    override suspend fun remember(messageId: String, bothered: Boolean): Unit = withContext(io) {
        synchronized(this@PrefsKindAnswerStore) {
            val editor = prefs.edit().putString(messageId, encode(bothered, System.currentTimeMillis()))
            val all = prefs.all
            val overflow = all.size + (if (messageId in all) 0 else 1) - MAX_ANSWERS
            if (overflow > 0) {
                all.entries
                    .filter { it.key != messageId }
                    .sortedBy { (it.value as? String)?.substringAfter(SEPARATOR)?.toLongOrNull() ?: 0L }
                    .take(overflow)
                    .forEach { editor.remove(it.key) }
            }
            editor.apply()
        }
    }

    override suspend fun clear(): Unit = withContext(io) {
        prefs.edit().clear().apply()
    }

    private fun encode(bothered: Boolean, at: Long): String = "${if (bothered) YES else NO}$SEPARATOR$at"

    private fun decode(value: String): Boolean? = when (value.substringBefore(SEPARATOR)) {
        YES -> true
        NO -> false
        else -> null
    }

    private companion object {
        const val FILE = "dating_kind_answers"
        const val MAX_ANSWERS = 1_000
        const val SEPARATOR = ':'
        const val YES = "1"
        const val NO = "0"
    }
}
