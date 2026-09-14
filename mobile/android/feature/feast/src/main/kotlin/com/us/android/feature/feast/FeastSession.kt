package com.us.android.feature.feast

import com.us.android.core.food.network.FeastAddressDto
import com.us.android.feature.feast.restaurant.Serviceability
import com.us.android.feature.feast.restaurant.ServiceabilityRules
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What Feast's screens share for the life of the process: the delivery address
 * the customer picked, and the serviceability refusals the server has returned.
 *
 * Nothing money-relevant lives here — checkout keeps its own saved state — so
 * losing this to process death only means re-picking an address (the default
 * address is chosen again) and re-learning a refusal on the next attempt.
 */
@Singleton
class FeastSession @Inject constructor() {

    private val _address = MutableStateFlow<FeastAddressDto?>(null)
    val address: StateFlow<FeastAddressDto?> = _address.asStateFlow()

    private val refusals = MutableStateFlow<Map<String, Refusal>>(emptyMap())

    fun selectAddress(address: FeastAddressDto?) {
        _address.value = address
    }

    /** Keeps the picked address if it still exists, else the default, else the first. */
    fun reconcileAddresses(addresses: List<FeastAddressDto>) {
        _address.update { current ->
            addresses.firstOrNull { it.id == current?.id }
                ?: addresses.firstOrNull { it.isDefault }
                ?: addresses.firstOrNull()
        }
    }

    /** Records a server refusal for [restaurantId], for the address it was made against. */
    fun recordRefusal(restaurantId: String, addressId: String?, blocked: Serviceability.Blocked) {
        refusals.update { it + (restaurantId to Refusal(addressId, blocked)) }
    }

    /**
     * The server's standing refusal for [restaurantId], if one applies to the
     * currently picked address. An address-dependent refusal (out of range)
     * stops applying when the customer picks another address.
     */
    fun refusalFor(restaurantId: String): Serviceability.Blocked? {
        val refusal = refusals.value[restaurantId] ?: return null
        val addressDependent = refusal.blocked.code in ServiceabilityRules.ADDRESS_CODES
        return if (addressDependent && refusal.addressId != _address.value?.id) null else refusal.blocked
    }

    fun clearRefusal(restaurantId: String) {
        refusals.update { it - restaurantId }
    }

    private data class Refusal(val addressId: String?, val blocked: Serviceability.Blocked)
}
