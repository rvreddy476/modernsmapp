package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.PayoutAccount
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Where the seller is paid.
 *
 * Either a bank account or a UPI id satisfies the server. Both are offered
 * because a small seller often has only one, and demanding the other is how a
 * shop stalls at the last step of onboarding.
 */
data class PayoutForm(
    val accountHolderName: String = "",
    val accountNumber: String = "",
    val ifscCode: String = "",
    val bankName: String = "",
    val upiId: String = "",
    val saving: Boolean = false,
    val error: String? = null,
    val saved: Boolean = false,
) {
    /** A bank account needs BOTH halves — a number with no IFSC cannot be paid. */
    val hasBank: Boolean
        get() = accountNumber.trim().length >= MIN_ACCOUNT_DIGITS &&
            ifscCode.trim().length == IFSC_LENGTH

    /**
     * A UPI id is `handle@provider`.
     *
     * Checked only for that shape. Whether the handle exists is a question
     * only the payment provider can answer, and pretending otherwise here
     * would reject valid ids from providers this build has never heard of.
     */
    val hasUpi: Boolean
        get() = upiId.trim().let { it.contains('@') && !it.startsWith('@') && !it.endsWith('@') }

    /**
     * Whether a half-filled bank section is blocking.
     *
     * A seller who typed an account number and no IFSC has not "chosen UPI" —
     * they have started something and stopped, and the screen should say so
     * rather than silently ignoring the field.
     */
    val bankPartiallyFilled: Boolean
        get() = !hasBank && (accountNumber.isNotBlank() || ifscCode.isNotBlank())

    val isComplete: Boolean
        get() = accountHolderName.trim().isNotEmpty() &&
            (hasBank || hasUpi) &&
            !bankPartiallyFilled

    fun toAccount() = PayoutAccount(
        accountHolderName = accountHolderName,
        // The server requires an account_number even for a UPI-only seller.
        // Sending the UPI id there would put a handle in a bank-account column
        // that a payout run reads as digits, so it is sent empty and the UPI
        // id carries the payment detail.
        accountNumber = if (hasBank) accountNumber else "",
        bankName = bankName.takeIf { hasBank },
        ifscCode = ifscCode.takeIf { hasBank },
        upiId = upiId.takeIf { hasUpi },
    )
}

private const val MIN_ACCOUNT_DIGITS = 6
private const val IFSC_LENGTH = 11

@HiltViewModel
class PayoutViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _form = MutableStateFlow(PayoutForm())
    val form: StateFlow<PayoutForm> = _form.asStateFlow()

    init {
        // Prefill the account holder from the shop name: for an individual
        // seller they are usually the same, and a name that has to match the
        // bank's records exactly is one worth showing rather than asking for
        // cold.
        viewModelScope.launch {
            when (val r = repo.sellerProfile()) {
                is CommerceResult.Success ->
                    _form.value = _form.value.copy(accountHolderName = r.value.storeName)

                is CommerceResult.Failure -> Unit // the form starts empty
            }
        }
    }

    fun update(transform: (PayoutForm) -> PayoutForm) {
        _form.value = transform(_form.value).copy(error = null, saved = false)
    }

    fun save(onSaved: () -> Unit) {
        val form = _form.value
        if (!form.isComplete || form.saving) return

        _form.value = form.copy(saving = true, error = null)
        viewModelScope.launch {
            when (val r = repo.savePayout(form.toAccount())) {
                is CommerceResult.Failure ->
                    _form.value = form.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    _form.value = form.copy(saving = false, saved = true)
                    onSaved()
                }
            }
        }
    }
}
