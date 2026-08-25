package com.us.android.feature.chat.ui

import android.content.Context
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.lock.ChatLockManager
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ChatLockUiState(
    val enabled: Boolean = false,
    val interval: String = "",
    val intervals: List<String> = emptyList(),
    val biometricEnabled: Boolean = true,
    val biometricAvailable: Boolean = false,
    val lockoutSeconds: Long = 0,
    val error: String? = null,
    val confirmingReset: Boolean = false,
)

@HiltViewModel
class ChatLockViewModel @Inject constructor(
    @ApplicationContext private val context: Context,
    private val manager: ChatLockManager,
    private val store: ChatStore,
) : ViewModel() {

    val locked: StateFlow<Boolean> = manager.locked

    private val _state = MutableStateFlow(currentState())
    val state: StateFlow<ChatLockUiState> = _state.asStateFlow()

    init {
        // Tick the lockout countdown so the throttle message stays honest.
        viewModelScope.launch {
            while (true) {
                delay(MILLIS_PER_SECOND)
                val remaining = manager.lockoutRemainingMillis() / MILLIS_PER_SECOND
                if (remaining != _state.value.lockoutSeconds) {
                    _state.update { it.copy(lockoutSeconds = remaining) }
                }
            }
        }
    }

    fun unlockWithPin(pin: String) {
        if (manager.unlockWithPin(pin)) {
            _state.update { it.copy(error = null, confirmingReset = false) }
        } else {
            _state.update {
                it.copy(
                    error = "That PIN doesn't match.",
                    lockoutSeconds = manager.lockoutRemainingMillis() / 1_000,
                )
            }
        }
    }

    fun promptDeviceAuth(activity: FragmentActivity) {
        if (!manager.biometricEnabled) return
        val prompt = BiometricPrompt(
            activity,
            ContextCompat.getMainExecutor(activity),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    manager.unlockWithDeviceAuth()
                }
            },
        )
        prompt.authenticate(
            BiometricPrompt.PromptInfo.Builder()
                .setTitle("Unlock chat")
                .setAllowedAuthenticators(
                    BiometricManager.Authenticators.BIOMETRIC_STRONG or
                        BiometricManager.Authenticators.DEVICE_CREDENTIAL,
                )
                .build(),
        )
    }

    fun startForgotFlow() = _state.update { it.copy(confirmingReset = true) }

    fun cancelForgotFlow() = _state.update { it.copy(confirmingReset = false) }

    /** No backdoor: reset clears the lock AND this device's cached chat data. */
    fun confirmReset() = viewModelScope.launch {
        manager.resetForgotten { store.wipeForLogout() }
        _state.value = currentState()
    }

    fun enable(pin: String) {
        runCatching { manager.enable(pin) }
            .onFailure { _state.update { s -> s.copy(error = "PIN must be at least 6 digits.") } }
        _state.value = currentState()
    }

    fun disable(pin: String) {
        if (!manager.disable(pin)) {
            _state.update { it.copy(error = "That PIN doesn't match.") }
            return
        }
        _state.value = currentState()
    }

    fun setInterval(label: String) {
        ChatLockManager.LockInterval.entries.firstOrNull { it.label == label }?.let {
            manager.interval = it
        }
        _state.value = currentState()
    }

    fun setBiometricEnabled(enabled: Boolean) {
        manager.setBiometricEnabled(enabled)
        _state.value = currentState()
    }

    private fun currentState() = ChatLockUiState(
        enabled = manager.isEnabled,
        interval = manager.interval.label,
        intervals = ChatLockManager.LockInterval.entries.map { it.label },
        biometricEnabled = manager.biometricEnabled,
        biometricAvailable = BiometricManager.from(context).canAuthenticate(
            BiometricManager.Authenticators.BIOMETRIC_STRONG or
                BiometricManager.Authenticators.DEVICE_CREDENTIAL,
        ) == BiometricManager.BIOMETRIC_SUCCESS,
        lockoutSeconds = manager.lockoutRemainingMillis() / MILLIS_PER_SECOND,
    )

    private companion object {
        const val MILLIS_PER_SECOND = 1_000L
    }
}
