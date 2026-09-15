package com.us.android.feature.dating.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.dating.network.AttachPhotoRequest
import com.us.android.feature.dating.network.BlockRequest
import com.us.android.feature.dating.network.BlockedDto
import com.us.android.feature.dating.network.ClosedDto
import com.us.android.feature.dating.network.ConsentRequest
import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.DataExportDto
import com.us.android.feature.dating.network.DatingApi
import com.us.android.feature.dating.network.DatingPhotoDto
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.DeleteProfileRequest
import com.us.android.feature.dating.network.ExplainDto
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.PanicDto
import com.us.android.feature.dating.network.PanicRequest
import com.us.android.feature.dating.network.PassDto
import com.us.android.feature.dating.network.PassRequest
import com.us.android.feature.dating.network.PauseRequest
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.PreferencesRequest
import com.us.android.feature.dating.network.PremiumCatalogueDto
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.PremiumPaymentDto
import com.us.android.feature.dating.network.PremiumPurchaseRequest
import com.us.android.feature.dating.network.PremiumPurchaseResultDto
import com.us.android.feature.dating.network.PrivacyDto
import com.us.android.feature.dating.network.PrivacyUpdateRequest
import com.us.android.feature.dating.network.PromptAnswerDto
import com.us.android.feature.dating.network.PromptAnswerRequest
import com.us.android.feature.dating.network.PromptCatalogItemDto
import com.us.android.feature.dating.network.PulseTodayDto
import com.us.android.feature.dating.network.ReportRequest
import com.us.android.feature.dating.network.ReportResultDto
import com.us.android.feature.dating.network.SelfieChallengeDto
import com.us.android.feature.dating.network.SelfieResultDto
import com.us.android.feature.dating.network.SelfieSubmitRequest
import com.us.android.feature.dating.network.ShareLocationDto
import com.us.android.feature.dating.network.ShareLocationRequest
import com.us.android.feature.dating.network.SharedLocationDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.network.SparkDeclineDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.network.SparkRequest
import com.us.android.feature.dating.network.StashDto
import com.us.android.feature.dating.network.StashRequest
import com.us.android.feature.dating.network.StatusDto
import com.us.android.feature.dating.network.StopShareDto
import com.us.android.feature.dating.network.TrustedContactDto
import com.us.android.feature.dating.network.TrustedContactRequest
import com.us.android.feature.dating.network.TrustedContactsDto
import com.us.android.feature.dating.network.UpdatePhotoRequest
import com.us.android.feature.dating.network.UpsertProfileRequest
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.Json
import okhttp3.ResponseBody
import retrofit2.Response
import java.io.IOException
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Dating's data layer. Every expected 4xx/5xx is a [DatingResult.Failure];
 * nothing throws except cancellation.
 *
 * The payment status source that adapts [purchasePayment] lives in
 * `premium/DatingPayments.kt`, beside the rest of the `:core:payments` wiring.
 */
@Suppress("TooManyFunctions")
@Singleton
class DatingRepository @Inject constructor(
    private val api: DatingApi,
    val json: Json,
) {

    /**
     * The first call Dating makes, and the access probe: consents exist for
     * every user whether or not they have a profile, so ANY 404 here is the
     * gateway's pilot gate — [DatingError.NotAvailable].
     */
    suspend fun access(): DatingResult<ConsentsDto> = when (val result = call { api.consents() }) {
        is DatingResult.Failure ->
            if ((result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND) {
                DatingResult.Failure(DatingError.NotAvailable)
            } else {
                result
            }
        is DatingResult.Success -> result
    }

    suspend fun consents(): DatingResult<ConsentsDto> = call { api.consents() }

    suspend fun setConsent(type: String, granted: Boolean): DatingResult<ConsentsDto> =
        call { api.setConsent(type, ConsentRequest(granted)) }

    /** Null when there is no profile yet (404 "profile not found"). */
    suspend fun profile(): DatingResult<DatingProfileDto?> = when (val result = call { api.profile() }) {
        is DatingResult.Success -> result
        is DatingResult.Failure ->
            if ((result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND) DatingResult.Success(null) else result
    }

    suspend fun upsertProfile(request: UpsertProfileRequest): DatingResult<DatingProfileDto> =
        call { api.upsertProfile(request) }

    suspend fun setPaused(paused: Boolean): DatingResult<DatingProfileDto> = call { api.pause(PauseRequest(paused)) }

    suspend fun deleteProfile(reason: String?): DatingResult<StatusDto> =
        call { api.deleteProfile(DeleteProfileRequest(reason?.takeIf { it.isNotBlank() })) }

    suspend fun privacy(): DatingResult<PrivacyDto> = call { api.privacy() }

    suspend fun updatePrivacy(request: PrivacyUpdateRequest): DatingResult<PrivacyDto> = call { api.updatePrivacy(request) }

    suspend fun preferences(): DatingResult<PreferencesDto> = call { api.preferences() }

    suspend fun updatePreferences(request: PreferencesRequest): DatingResult<PreferencesDto> =
        call { api.updatePreferences(request) }

    suspend fun myPhotos(): DatingResult<List<DatingPhotoDto>> = list { api.myPhotos() }

    suspend fun attachPhoto(mediaId: String, primary: Boolean): DatingResult<DatingPhotoDto> =
        call { api.attachPhoto(AttachPhotoRequest(mediaId = mediaId, isPrimary = primary.takeIf { it })) }

    suspend fun makePrimary(photoId: String): DatingResult<DatingPhotoDto> =
        call { api.updatePhoto(photoId, UpdatePhotoRequest(isPrimary = true)) }

    suspend fun deletePhoto(photoId: String): DatingResult<StatusDto> = call { api.deletePhoto(photoId) }

    suspend fun promptCatalog(): DatingResult<List<PromptCatalogItemDto>> = list { api.promptCatalog() }

    suspend fun prompts(): DatingResult<List<PromptAnswerDto>> = list { api.prompts() }

    suspend fun answerPrompt(promptId: Int, answer: String): DatingResult<PromptAnswerDto> =
        call { api.answerPrompt(promptId, PromptAnswerRequest(answer)) }

    suspend fun deletePrompt(promptId: Int): DatingResult<StatusDto> = call { api.deletePrompt(promptId) }

    suspend fun selfieChallenge(): DatingResult<SelfieChallengeDto> = call { api.selfieChallenge() }

    suspend fun submitSelfie(challengeId: String, videoMediaId: String): DatingResult<SelfieResultDto> =
        call { api.submitSelfie(SelfieSubmitRequest(challengeId, videoMediaId)) }

    suspend fun pulseToday(): DatingResult<PulseTodayDto> = datingRawCall(json) { api.pulseToday() }

    suspend fun explain(userId: String): DatingResult<ExplainDto> = call { api.explain(userId) }

    suspend fun pass(candidateId: String): DatingResult<PassDto> = call { api.pass(candidateId, PassRequest()) }

    /**
     * A spark on [toUserId]'s primary photo. Accepting an incoming spark is the
     * same call aimed at its sender: there is no accept route.
     */
    suspend fun spark(toUserId: String, note: String? = null): DatingResult<SparkCreatedDto> =
        call {
            api.spark(
                SparkRequest(
                    toUserId = toUserId,
                    targetKind = TARGET_PHOTO,
                    targetRef = PRIMARY_PHOTO_REF,
                    note = note?.trim()?.takeIf { it.isNotEmpty() },
                ),
            )
        }

    suspend fun incomingSparks(): DatingResult<List<SparkDto>> = list { api.incomingSparks() }

    suspend fun declineSpark(sparkId: String): DatingResult<SparkDeclineDto> = call { api.declineSpark(sparkId) }

    suspend fun stash(candidateId: String): DatingResult<StashDto> = call { api.stash(StashRequest(candidateId)) }

    suspend fun matches(): DatingResult<List<MatchDto>> = list { api.matches() }

    suspend fun match(matchId: String): DatingResult<MatchDto> = call { api.match(matchId) }

    suspend fun unmatch(matchId: String): DatingResult<ClosedDto> = call { api.closeMatch(matchId) }

    suspend fun block(userId: String): DatingResult<BlockedDto> = call { api.block(BlockRequest(userId)) }

    suspend fun report(request: ReportRequest): DatingResult<ReportResultDto> = call { api.report(request) }

    suspend fun panic(latitude: Double?, longitude: Double?): DatingResult<PanicDto> =
        call { api.panic(PanicRequest(latitude, longitude)) }

    suspend fun trustedContacts(): DatingResult<TrustedContactsDto> = call { api.trustedContacts() }

    suspend fun addTrustedContact(userId: String): DatingResult<TrustedContactDto> =
        call { api.addTrustedContact(userId, TrustedContactRequest(shareLocationOnPanic = true)) }

    suspend fun removeTrustedContact(userId: String): DatingResult<Unit> =
        call { api.removeTrustedContact(userId) }.map { }

    suspend fun shareLocation(request: ShareLocationRequest): DatingResult<ShareLocationDto> =
        call { api.shareLocation(request) }

    suspend fun stopShare(shareId: String): DatingResult<StopShareDto> = call { api.stopShare(shareId) }

    suspend fun sharedLocation(shareId: String): DatingResult<SharedLocationDto> = call { api.sharedLocation(shareId) }

    suspend fun requestExport(): DatingResult<DataExportDto> = call { api.requestExport() }

    suspend fun exports(): DatingResult<List<DataExportDto>> = list { api.exports() }

    /** Streams the owner's export into [write]. The body is never kept in memory or on disk by the app. */
    suspend fun downloadExport(exportId: String, write: (ResponseBody) -> Unit): DatingResult<Unit> = try {
        val response: Response<ResponseBody> = api.downloadExport(exportId)
        val body = response.body()
        if (response.isSuccessful && body != null) {
            body.use(write)
            DatingResult.Success(Unit)
        } else {
            DatingResult.Failure(DatingError.from(response.code(), response.errorBody()?.string(), json))
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: IOException) {
        DatingResult.Failure(DatingError.Network(e))
    }

    suspend fun premiumCatalogue(): DatingResult<PremiumCatalogueDto> = call { api.premiumCatalogue() }

    /** [idempotencyKey] is minted ONCE per buyer decision and reused on every resend. */
    suspend fun purchase(productId: String, idempotencyKey: String): DatingResult<PremiumPurchaseResultDto> =
        call { api.purchase(PremiumPurchaseRequest(product = productId, idempotencyKey = idempotencyKey)) }

    suspend fun purchasePayment(purchaseId: String): DatingResult<PremiumPaymentDto> =
        call { api.purchasePayment(purchaseId) }

    suspend fun premiumMe(): DatingResult<PremiumMeDto> = call { api.premiumMe() }

    fun newIdempotencyKey(): String = UUID.randomUUID().toString()

    private suspend fun <T> call(block: suspend () -> Response<ApiEnvelope<T>>): DatingResult<T> = datingCall(json, block = block)

    private suspend fun <T> list(block: suspend () -> Response<ApiEnvelope<List<T>>>): DatingResult<List<T>> =
        datingCall(json, allowNullData = true, empty = emptyList(), block = block)

    companion object {
        private const val HTTP_NOT_FOUND = 404
        const val TARGET_PHOTO = "photo"
        const val PRIMARY_PHOTO_REF = "0"
    }
}
