package com.us.android.feature.chat.ui.hidden

import android.content.Context
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.hidden.HiddenChats
import com.us.android.core.chat.hidden.HiddenError
import com.us.android.core.chat.hidden.HiddenResult
import com.us.android.core.chat.hidden.openErrorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.crypto.Cipher

/** The password prompt for one row. */
data class OpenPrompt(
    val conversation: Conversation,
    val busy: Boolean = false,
    val error: String? = null,
    /** Locked out, or out of tries: the Open button waits. */
    val blocked: Boolean = false,
)

data class HiddenListUiState(
    val viewerId: String = "",
    val rows: List<Conversation>? = null,
    val loading: Boolean = false,
    val prompt: OpenPrompt? = null,
    /** Fingerprint or face can open on this device (a sealed password exists and the sensor is ready). */
    val biometricReady: Boolean = false,
    /** Fingerprint or face could be turned on with this password entry. */
    val biometricOffer: Boolean = false,
    /** Opened: the thread to show, handed over once. */
    val openThread: Conversation? = null,
)

/**
 * The hidden list (behind the secret key) and opening a hidden chat: the
 * chat password, or fingerprint/face after the password was entered once on
 * this device. The password is held only for the length of one call (and,
 * when sealing it for biometrics, until that prompt ends) — never in saved
 * state.
 */
@HiltViewModel
class HiddenChatsListViewModel @Inject constructor(
    @ApplicationContext private val context: Context,
    private val hidden: HiddenChats,
) : ViewModel() {

    private val _state = MutableStateFlow(HiddenListUiState(viewerId = hidden.viewerId))
    val state: StateFlow<HiddenListUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch { hidden.rows.collect { rows -> _state.update { it.copy(rows = rows) } } }
        // The reveal from search already read the list; read it here only when it did not.
        if (hidden.rows.value == null) refresh()
    }

    fun refresh() = viewModelScope.launch {
        _state.update { it.copy(loading = true) }
        hidden.refreshList()
        _state.update { it.copy(loading = false) }
    }

    fun lockNow() = hidden.lockNow()

    /** A row tapped: straight in while the chats are open, else the prompt. */
    fun select(conversation: Conversation) {
        hidden.touch()
        if (hidden.tokens.open() != null) {
            _state.update { it.copy(openThread = conversation) }
            return
        }
        val strong = biometricStrongAvailable()
        val sealed = hidden.unlockVault.has(hidden.viewerId)
        _state.update {
            it.copy(
                prompt = OpenPrompt(conversation),
                biometricReady = strong && sealed,
                biometricOffer = strong && !sealed,
            )
        }
    }

    fun dismissPrompt() = _state.update { it.copy(prompt = null) }

    fun consumeOpen() = _state.update { it.copy(openThread = null) }

    /**
     * The chat password. When [rememberWithBiometric], a successful open is
     * followed by one fingerprint/face prompt that seals the password into
     * the Keystore; the chat opens whatever that prompt answers.
     */
    fun submitPassword(password: String, rememberWithBiometric: Boolean, activity: FragmentActivity?) {
        val prompt = _state.value.prompt ?: return
        if (password.isEmpty() || prompt.busy || prompt.blocked) return
        _state.update { it.copy(prompt = prompt.copy(busy = true, error = null)) }
        viewModelScope.launch {
            when (val r = hidden.open(password)) {
                is HiddenResult.Ok -> {
                    if (rememberWithBiometric && activity != null && _state.value.biometricOffer) {
                        sealForBiometric(activity, password) { finishOpen(prompt.conversation) }
                    } else {
                        finishOpen(prompt.conversation)
                    }
                }
                is HiddenResult.Err -> refuse(prompt, r.error)
            }
        }
    }

    /** Fingerprint or face: unseal the password this device keeps, then open with it. */
    fun openWithBiometric(activity: FragmentActivity) {
        val prompt = _state.value.prompt ?: return
        if (prompt.busy || prompt.blocked) return
        val userId = hidden.viewerId
        val cipher = hidden.unlockVault.openCipher(userId)
        if (cipher == null) {
            // The enrolment changed (or the key is gone): the password it is.
            _state.update { it.copy(biometricReady = false, biometricOffer = biometricStrongAvailable()) }
            return
        }
        authenticate(activity, cipher, title = "Open hidden chats", onSuccess = { authorised ->
            val password = hidden.unlockVault.open(userId, authorised)
            if (password == null) {
                _state.update { it.copy(biometricReady = false) }
                return@authenticate
            }
            _state.update { it.copy(prompt = prompt.copy(busy = true, error = null)) }
            viewModelScope.launch {
                when (val r = hidden.openWithSealedPassword(password)) {
                    is HiddenResult.Ok -> finishOpen(prompt.conversation)
                    is HiddenResult.Err -> {
                        if (r.error is HiddenError.WrongPassword) {
                            _state.update { it.copy(biometricReady = false, biometricOffer = biometricStrongAvailable()) }
                        }
                        refuse(prompt, r.error)
                    }
                }
            }
        }, onEnd = {})
    }

    private fun sealForBiometric(activity: FragmentActivity, password: String, then: () -> Unit) {
        val cipher = hidden.unlockVault.sealCipher()
        if (cipher == null) {
            then()
            return
        }
        authenticate(
            activity,
            cipher,
            title = "Use fingerprint or face next time",
            onSuccess = { authorised -> hidden.unlockVault.seal(hidden.viewerId, authorised, password) },
            onEnd = then,
        )
    }

    private fun finishOpen(conversation: Conversation) {
        _state.update { it.copy(prompt = null, openThread = conversation) }
    }

    private fun refuse(prompt: OpenPrompt, error: HiddenError) {
        val blocked = error is HiddenError.Locked || (error is HiddenError.WrongPassword && error.attemptsLeft == 0)
        // The reveal token went with the refusal: the list re-locks and the guard leaves.
        val gone = error == HiddenError.NotFound
        _state.update {
            it.copy(prompt = if (gone) null else prompt.copy(busy = false, error = openErrorMessage(error), blocked = blocked))
        }
    }

    private fun biometricStrongAvailable(): Boolean =
        BiometricManager.from(context).canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG) ==
            BiometricManager.BIOMETRIC_SUCCESS

    /** One BiometricPrompt bound to [cipher]: STRONG biometrics only, the password as the way out. */
    private fun authenticate(
        activity: FragmentActivity,
        cipher: Cipher,
        title: String,
        onSuccess: (Cipher) -> Unit,
        onEnd: () -> Unit,
    ) {
        val prompt = BiometricPrompt(
            activity,
            ContextCompat.getMainExecutor(activity),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    result.cryptoObject?.cipher?.let(onSuccess)
                    onEnd()
                }

                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                    onEnd()
                }
            },
        )
        prompt.authenticate(
            BiometricPrompt.PromptInfo.Builder()
                .setTitle(title)
                .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
                .setNegativeButtonText("Use chat password")
                .build(),
            BiometricPrompt.CryptoObject(cipher),
        )
    }
}
