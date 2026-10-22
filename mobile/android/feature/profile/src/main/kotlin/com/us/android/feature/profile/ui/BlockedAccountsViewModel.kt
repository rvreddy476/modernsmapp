package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.BlockedAccountsSource
import com.us.android.core.profile.data.RestrictedAccount
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything the Blocked accounts page draws. */
data class BlockedAccountsUiState(
    val loading: Boolean = true,
    val error: String? = null,
    val refreshing: Boolean = false,
    val blocked: List<RestrictedAccount> = emptyList(),
    val muted: List<RestrictedAccount> = emptyList(),
    /** "Unblock Ana?" — asked first, as the web does. */
    val confirmingUnblock: RestrictedAccount? = null,
) {
    val isEmpty: Boolean get() = !loading && error == null && blocked.isEmpty() && muted.isEmpty()
}

/**
 * Settings › Privacy › Blocked accounts (web parity, 2026-10-22): the
 * accounts the viewer blocked, and — because the same graph route returns
 * them — the ones they muted, each with its undo. Unblock asks first, then
 * takes the row out at once and puts it back where it was if the server
 * refuses; Unmute does the same without asking (a mute is quiet both ways).
 */
@HiltViewModel
class BlockedAccountsViewModel @Inject constructor(
    private val source: BlockedAccountsSource,
    private val session: SessionStateProvider,
) : ViewModel() {

    private val _state = MutableStateFlow(BlockedAccountsUiState())
    val state: StateFlow<BlockedAccountsUiState> = _state.asStateFlow()

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val inFlight = mutableSetOf<String>()

    init {
        refresh()
    }

    fun refresh() {
        val viewerId = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()
        val hadRows = _state.value.blocked.isNotEmpty() || _state.value.muted.isNotEmpty()
        _state.update { it.copy(refreshing = hadRows, loading = !hadRows, error = null) }
        viewModelScope.launch {
            val result = if (viewerId.isBlank()) {
                AppResult.Failure(AppError.AuthFailed())
            } else {
                source.load(viewerId)
            }
            when (result) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        loading = false,
                        refreshing = false,
                        blocked = result.data.blocked,
                        muted = result.data.muted,
                    )
                }
                is AppResult.Failure -> if (hadRows) {
                    _state.update { it.copy(loading = false, refreshing = false) }
                    _message.value = UsMessage(errorText(result.error, "We couldn't refresh this list."))
                } else {
                    _state.update {
                        it.copy(loading = false, refreshing = false, error = errorText(result.error, LOAD_FAILED))
                    }
                }
            }
        }
    }

    fun askUnblock(account: RestrictedAccount) = _state.update { it.copy(confirmingUnblock = account) }

    fun cancelUnblock() = _state.update { it.copy(confirmingUnblock = null) }

    /** The confirmed unblock: the row goes at once; a refusal puts it back and says so. */
    fun unblock() {
        val account = _state.value.confirmingUnblock ?: return
        _state.update { it.copy(confirmingUnblock = null) }
        undo(
            account = account,
            take = { s -> s.blocked },
            put = { s, rows -> s.copy(blocked = rows) },
            call = { source.unblock(account.userId) },
            done = "${account.label()} is unblocked",
            failed = "Couldn't unblock ${account.label()}. Try again.",
        )
    }

    fun unmute(account: RestrictedAccount) = undo(
        account = account,
        take = { s -> s.muted },
        put = { s, rows -> s.copy(muted = rows) },
        call = { source.unmute(account.userId) },
        done = "${account.label()} is unmuted",
        failed = "Couldn't unmute ${account.label()}. Try again.",
    )

    fun dismissMessage() {
        _message.value = null
    }

    @Suppress("LongParameterList")
    private fun undo(
        account: RestrictedAccount,
        take: (BlockedAccountsUiState) -> List<RestrictedAccount>,
        put: (BlockedAccountsUiState, List<RestrictedAccount>) -> BlockedAccountsUiState,
        call: suspend () -> AppResult<Unit>,
        done: String,
        failed: String,
    ) {
        if (!inFlight.add(account.userId)) return
        val index = take(_state.value).indexOfFirst { it.userId == account.userId }
        if (index < 0) {
            inFlight.remove(account.userId)
            return
        }
        _state.update { s -> put(s, take(s).filterNot { it.userId == account.userId }) }
        viewModelScope.launch {
            val result = call()
            inFlight.remove(account.userId)
            when (result) {
                is AppResult.Success -> _message.value = UsMessage(done, UsMessageType.Success)
                is AppResult.Failure -> {
                    _state.update { s ->
                        val rows = take(s)
                        if (rows.any { it.userId == account.userId }) {
                            s
                        } else {
                            put(s, rows.toMutableList().apply { add(index.coerceIn(0, size), account) })
                        }
                    }
                    _message.value = UsMessage(errorText(result.error, failed))
                }
            }
        }
    }

    private fun errorText(error: AppError, fallback: String): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        is AppError.RateLimited -> "Too many changes at once. Wait a moment and try again."
        else -> fallback
    }

    private companion object {
        const val LOAD_FAILED = "We couldn't load your blocked accounts."
    }
}

/** What a row is called: the name, else the @handle, else a neutral word. */
internal fun RestrictedAccount.label(): String =
    displayName.ifBlank { username.takeIf { it.isNotBlank() }?.let { "@$it" } ?: "This account" }
