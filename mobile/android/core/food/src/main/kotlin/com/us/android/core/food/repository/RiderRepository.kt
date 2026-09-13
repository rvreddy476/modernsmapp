package com.us.android.core.food.repository

import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryAvailabilityRequest
import com.us.android.core.food.network.DeliveryCodeRequest
import com.us.android.core.food.network.DeliveryDocumentDto
import com.us.android.core.food.network.DeliveryDocumentRequest
import com.us.android.core.food.network.DeliveryEarningsDto
import com.us.android.core.food.network.DeliveryKycDto
import com.us.android.core.food.network.DeliveryLocationDto
import com.us.android.core.food.network.DeliveryLocationRequest
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.network.DeliveryPartnerDto
import com.us.android.core.food.network.DeliveryPartnerRequest
import com.us.android.core.food.network.DigiLockerCallbackRequest
import com.us.android.core.food.network.DigiLockerStartDto
import com.us.android.core.food.network.OfferResponseDto
import com.us.android.core.food.network.RejectOfferRequest
import com.us.android.core.food.network.RiderApi
import com.us.android.core.food.network.VerifyDeliveryDto
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/** A rider's own assignment steps. PICKED_UP and DELIVERED are deliberately absent. */
enum class RiderAssignmentStep {
    /** ASSIGNED → ACCEPTED. */
    ACCEPT,

    /** Release the job back to dispatch. */
    REJECT,

    /** ACCEPTED → ARRIVED_AT_RESTAURANT. */
    ARRIVED_AT_RESTAURANT,

    /** PICKED_UP → ARRIVED_AT_CUSTOMER. */
    ARRIVED_AT_CUSTOMER,
}

/** The delivery partner's data layer. Typed failures, never exceptions — see [foodCall]. */
@Suppress("TooManyFunctions")
@Singleton
class RiderRepository @Inject constructor(
    private val api: RiderApi,
    private val json: Json,
) {

    /** Success(null) when the caller has no profile yet (404 FOOD_DELIVERY_PROFILE_NOT_FOUND). */
    suspend fun profile(): FoodResult<DeliveryPartnerDto?> =
        foodCall(json) { api.profile() }.orNullOn(CODE_PROFILE_NOT_FOUND)

    suspend fun createProfile(request: DeliveryPartnerRequest): FoodResult<DeliveryPartnerDto> =
        foodCall(json) { api.createProfile(request) }

    suspend fun updateProfile(request: DeliveryPartnerRequest): FoodResult<DeliveryPartnerDto> =
        foodCall(json) { api.updateProfile(request) }

    suspend fun startDigiLocker(): FoodResult<DigiLockerStartDto> = foodCall(json) { api.startDigiLocker() }

    suspend fun completeDigiLocker(state: String, code: String): FoodResult<DeliveryKycDto> =
        foodCall(json) { api.completeDigiLocker(DigiLockerCallbackRequest(state = state, code = code)) }

    /** Failure(NotFound) when the caller has no profile yet. */
    suspend fun kycStatus(): FoodResult<DeliveryKycDto> = foodCall(json) { api.kycStatus() }

    suspend fun addDocument(request: DeliveryDocumentRequest): FoodResult<DeliveryDocumentDto> =
        foodCall(json) { api.addDocument(request) }

    suspend fun setAvailability(online: Boolean): FoodResult<DeliveryPartnerDto> =
        foodCall(json) { api.setAvailability(DeliveryAvailabilityRequest(online)) }

    suspend fun postLocation(request: DeliveryLocationRequest): FoodResult<DeliveryLocationDto> =
        foodCall(json) { api.postLocation(request) }

    suspend fun offers(): FoodResult<List<DeliveryOfferDto>> =
        foodCall(json) { api.offers() }.map { it.offers.orEmpty() }

    suspend fun acceptOffer(offerId: String): FoodResult<OfferResponseDto> = foodCall(json) { api.acceptOffer(offerId) }

    suspend fun rejectOffer(offerId: String, reason: String? = null): FoodResult<OfferResponseDto> =
        foodCall(json) { api.rejectOffer(offerId, RejectOfferRequest(reason?.takeIf { it.isNotBlank() })) }

    /** Success(null) when the rider holds no job (404 FOOD_DELIVERY_CURRENT_NOT_FOUND). */
    suspend fun currentAssignment(): FoodResult<DeliveryAssignmentDto?> =
        foodCall(json) { api.currentAssignment() }.orNullOn(CODE_CURRENT_NOT_FOUND)

    /** [idempotencyKey] must be reused when the SAME tap is retried. */
    suspend fun step(assignmentId: String, step: RiderAssignmentStep, idempotencyKey: String): FoodResult<DeliveryAssignmentDto> =
        foodCall(json) {
            when (step) {
                RiderAssignmentStep.ACCEPT -> api.acceptAssignment(assignmentId, idempotencyKey)
                RiderAssignmentStep.REJECT -> api.rejectAssignment(assignmentId, idempotencyKey)
                RiderAssignmentStep.ARRIVED_AT_RESTAURANT -> api.arrivedAtRestaurant(assignmentId, idempotencyKey)
                RiderAssignmentStep.ARRIVED_AT_CUSTOMER -> api.arrivedAtCustomer(assignmentId, idempotencyKey)
            }
        }

    suspend fun verifyDelivery(assignmentId: String, code: String): FoodResult<VerifyDeliveryDto> =
        foodCall(json) { api.verifyDelivery(assignmentId, DeliveryCodeRequest(code.trim())) }

    suspend fun earnings(): FoodResult<DeliveryEarningsDto> = foodCall(json) { api.earnings() }

    suspend fun history(): FoodResult<List<DeliveryAssignmentDto>> = foodCall(json) { api.history() }.map { it.items }

    private fun <T> FoodResult<T>.orNullOn(code: String): FoodResult<T?> = when (this) {
        is FoodResult.Success -> this
        is FoodResult.Failure -> if (error.code == code) FoodResult.Success(null) else this
    }

    private companion object {
        const val CODE_PROFILE_NOT_FOUND = "FOOD_DELIVERY_PROFILE_NOT_FOUND"
        const val CODE_CURRENT_NOT_FOUND = "FOOD_DELIVERY_CURRENT_NOT_FOUND"
    }
}
